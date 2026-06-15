package claudecode

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// logLine is the minimal projection of a Claude Code JSONL entry that we need.
// Fields we don't read are ignored by the JSON decoder.
type logLine struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	CWD       string          `json:"cwd"`
	SessionID string          `json:"sessionId"`
	Message   *messagePayload `json:"message"`
}

type messagePayload struct {
	Model   string          `json:"model"`
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	Usage   *usagePayload   `json:"usage"`
}

type usagePayload struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

type aggregate struct {
	costToday         float64
	costMonth         float64
	cost30d           float64
	tokensInputToday  int64
	tokensOutputToday int64
	tokensCacheToday  int64
	tokens30d         int64
	sessionStart      time.Time
	sessionCost       float64
	sessionCalls      int
	sessions          map[string]struct{}
	lastEvent         time.Time
	lastPrompt        string
	lastProject       string
	costByModel       map[string]float64
	costByProject     map[string]float64
	costByDay         map[string]float64 // key = YYYY-MM-DD
}

func walk(root string, now time.Time) (aggregate, error) {
	startDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	startMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	start30d := startDay.AddDate(0, 0, -29)
	earliest := start30d
	if startMonth.Before(earliest) {
		earliest = startMonth
	}
	sessionCutoff := now.Add(-5 * time.Hour)

	a := aggregate{
		sessions:      map[string]struct{}{},
		costByModel:   map[string]float64{},
		costByProject: map[string]float64{},
		costByDay:     map[string]float64{},
	}

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		// Skip files untouched in the window we care about.
		if info.ModTime().Before(earliest) {
			return nil
		}
		processFile(path, &a, startDay, earliest, start30d, sessionCutoff)
		return nil
	})
	return a, err
}

func processFile(path string, a *aggregate, startDay, earliest, start30d, sessionCutoff time.Time) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	// Assistant lines usually omit cwd, so inherit from the most recent
	// user line in this file.
	var fileProject string

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var ll logLine
		if err := json.Unmarshal(sc.Bytes(), &ll); err != nil {
			continue
		}
		if ll.Timestamp == "" {
			continue
		}
		ts, err := time.Parse(time.RFC3339Nano, ll.Timestamp)
		if err != nil {
			continue
		}
		if ts.Before(earliest) {
			continue
		}

		switch ll.Type {
		case "assistant":
			if ll.Message == nil || ll.Message.Usage == nil {
				continue
			}
			u := ll.Message.Usage
			c := costFor(ll.Message.Model, u.InputTokens, u.OutputTokens, u.CacheCreationInputTokens, u.CacheReadInputTokens)
			day := ts.Format("2006-01-02")
			a.costByDay[day] += c
			if !ts.Before(start30d) {
				a.cost30d += c
				a.tokens30d += u.InputTokens + u.OutputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
			}
			// Month bucket = current calendar month only.
			monthStart := time.Date(ts.Year(), ts.Month(), 1, 0, 0, 0, 0, ts.Location())
			if monthStart.Equal(time.Date(startDay.Year(), startDay.Month(), 1, 0, 0, 0, 0, startDay.Location())) {
				a.costMonth += c
			}
			a.costByModel[modelLabel(ll.Message.Model)] += c
			if fileProject != "" {
				a.costByProject[fileProject] += c
			}
			if !ts.Before(startDay) {
				a.costToday += c
				a.tokensInputToday += u.InputTokens
				a.tokensOutputToday += u.OutputTokens
				a.tokensCacheToday += u.CacheCreationInputTokens + u.CacheReadInputTokens
			}
			if !ts.Before(sessionCutoff) {
				a.sessionCost += c
				a.sessionCalls++
				if a.sessionStart.IsZero() || ts.Before(a.sessionStart) {
					a.sessionStart = ts
				}
			}
			if ll.SessionID != "" && !ts.Before(startDay) {
				a.sessions[ll.SessionID] = struct{}{}
			}
		case "user":
			if ll.CWD != "" {
				fileProject = projectFromCWD(ll.CWD)
			}
			if ll.Message != nil {
				if prompt := decodePrompt(ll.Message.Content); prompt != "" && ts.After(a.lastEvent) {
					a.lastEvent = ts
					a.lastPrompt = prompt
					if fileProject != "" {
						a.lastProject = fileProject
					}
				}
			}
		}
	}
	_ = io.EOF
}

// modelLabel shortens long internal IDs to something readable in a small tile.
func modelLabel(m string) string {
	if m == "" {
		return "unknown"
	}
	switch {
	case strings.HasPrefix(m, "claude-opus-4-8"):
		return "Opus 4.8"
	case strings.HasPrefix(m, "claude-opus-4-7"):
		return "Opus 4.7"
	case strings.HasPrefix(m, "claude-opus-4-6"):
		return "Opus 4.6"
	case strings.HasPrefix(m, "claude-sonnet-4-6"):
		return "Sonnet 4.6"
	case strings.HasPrefix(m, "claude-haiku-4-5"):
		return "Haiku 4.5"
	}
	return m
}

// decodePrompt handles both string and array-of-blocks content shapes.
func decodePrompt(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return clip(strings.TrimSpace(s))
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				return clip(strings.TrimSpace(b.Text))
			}
		}
	}
	return ""
}

func clip(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 120 {
		return s[:117] + "…"
	}
	return s
}

func projectFromCWD(cwd string) string {
	if cwd == "" {
		return ""
	}
	return filepath.Base(cwd)
}
