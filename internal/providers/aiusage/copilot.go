package aiusage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// creditUSD is the price of one GitHub AI credit. See ./COPILOT.md.
const creditUSD = 0.01

type copilotAccount struct {
	Plan     string
	SpendUSD float64
	ResetsAt time.Time
}

type copilotModel struct {
	Model    string  `json:"model"`
	Tokens   int64   `json:"tokens"`
	NanoAIU  float64 `json:"nano_aiu"`
	SpendUSD float64 `json:"-"`
}

// fetchCopilotAccount returns the plan, the credits spent in the current
// billing month across all Copilot clients, and the reset time. It uses
// the gh CLI's auth.
func fetchCopilotAccount(ctx context.Context) (copilotAccount, error) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, "gh", "api", "/copilot_internal/user").Output()
	if err != nil {
		return copilotAccount{}, err
	}
	return parseCopilotAccount(out)
}

func parseCopilotAccount(body []byte) (copilotAccount, error) {
	var resp struct {
		Plan      string    `json:"copilot_plan"`
		ResetsAt  time.Time `json:"quota_reset_date_utc"`
		Snapshots map[string]struct {
			CreditsUsed float64 `json:"credits_used"`
		} `json:"quota_snapshots"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return copilotAccount{}, fmt.Errorf("decode copilot account: %w", err)
	}
	credits := 0.0
	for _, s := range resp.Snapshots {
		credits += s.CreditsUsed
	}
	return copilotAccount{Plan: resp.Plan, SpendUSD: credits * creditUSD, ResetsAt: resp.ResetsAt}, nil
}

// fetchCopilotModels returns the top four models by spend that the
// Copilot CLI used since the given time. It reads the CLI's local SQLite
// store through the sqlite3 binary. A missing store returns no models.
func fetchCopilotModels(ctx context.Context, since time.Time) ([]copilotModel, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	db := filepath.Join(home, ".copilot", "session-store.db")
	if _, err := os.Stat(db); err != nil {
		return nil, nil
	}
	query := fmt.Sprintf(`select coalesce(nullif(copilot_usage_model, ''), model) as model,
  sum(coalesce(input_tokens, 0) + coalesce(output_tokens, 0)) as tokens,
  sum(coalesce(total_nano_aiu, 0)) as nano_aiu
from assistant_usage_events
where created_at >= '%s'
group by 1 order by 3 desc limit 4`, since.UTC().Format(time.RFC3339))

	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, "sqlite3", "-readonly", "-json", db, query).Output()
	if err != nil {
		return nil, err
	}
	return parseCopilotModels(out)
}

func parseCopilotModels(body []byte) ([]copilotModel, error) {
	if strings.TrimSpace(string(body)) == "" {
		return nil, nil
	}
	var models []copilotModel
	if err := json.Unmarshal(body, &models); err != nil {
		return nil, fmt.Errorf("decode copilot usage: %w", err)
	}
	for i := range models {
		models[i].SpendUSD = models[i].NanoAIU / 1e9 * creditUSD
	}
	return models, nil
}
