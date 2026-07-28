package claudecode

import "strings"

// Per-million-token USD prices. Estimates only — the Anthropic Admin API is
// the source of truth for billed amounts. Update as models change.
type pricing struct {
	input        float64
	output       float64
	cacheWrite5m float64
	cacheWrite1h float64
	cacheRead    float64
}

// Cache multipliers are fixed across models: a 5m write is 1.25x input, a 1h
// write is 2x, and a read is 0.1x. The columns are spelled out rather than
// derived so a model that breaks the pattern can just override its row.
var modelPricing = map[string]pricing{
	"claude-fable-5":  {input: 10, output: 50, cacheWrite5m: 12.5, cacheWrite1h: 20, cacheRead: 1},
	"claude-mythos-5": {input: 10, output: 50, cacheWrite5m: 12.5, cacheWrite1h: 20, cacheRead: 1},
	"claude-opus-5":   {input: 5, output: 25, cacheWrite5m: 6.25, cacheWrite1h: 10, cacheRead: 0.5},
	"claude-opus-4-8": {input: 5, output: 25, cacheWrite5m: 6.25, cacheWrite1h: 10, cacheRead: 0.5},
	"claude-opus-4-7": {input: 5, output: 25, cacheWrite5m: 6.25, cacheWrite1h: 10, cacheRead: 0.5},
	"claude-opus-4-6": {input: 5, output: 25, cacheWrite5m: 6.25, cacheWrite1h: 10, cacheRead: 0.5},
	// Sonnet 5 has an introductory rate of $2/$10 through 2026-08-31; these
	// are the standard rates it reverts to, so estimates run slightly high
	// until then.
	"claude-sonnet-5":   {input: 3, output: 15, cacheWrite5m: 3.75, cacheWrite1h: 6, cacheRead: 0.3},
	"claude-sonnet-4-6": {input: 3, output: 15, cacheWrite5m: 3.75, cacheWrite1h: 6, cacheRead: 0.3},
	"claude-haiku-4-5":  {input: 1, output: 5, cacheWrite5m: 1.25, cacheWrite1h: 2, cacheRead: 0.1},
}

// canonicalModel maps a raw model string from the JSONL logs onto a key in
// modelPricing. Claude Code writes several shapes:
//
//	claude-opus-5              exact
//	claude-opus-5[1m]          context-window suffix
//	claude-haiku-4-5-20251001  dated variant
//	opus / sonnet / haiku      bare tier aliases (older log format)
//
// Bare aliases resolve to the current model of that tier. Returns "" when
// nothing matches, which callers treat as unknown.
func canonicalModel(m string) string {
	if i := strings.IndexByte(m, '['); i >= 0 {
		m = m[:i]
	}
	switch m {
	case "opus":
		return "claude-opus-5"
	case "sonnet":
		return "claude-sonnet-5"
	case "haiku":
		return "claude-haiku-4-5"
	}
	if _, ok := modelPricing[m]; ok {
		return m
	}
	// Longest prefix wins so a dated variant lands on its base model rather
	// than on whichever shorter key happened to match first.
	best := ""
	for k := range modelPricing {
		if strings.HasPrefix(m, k) && len(k) > len(best) {
			best = k
		}
	}
	return best
}

func costFor(model string, in, out, cacheCreate, cacheRead int64) float64 {
	p, ok := modelPricing[canonicalModel(model)]
	if !ok {
		p = modelPricing["claude-sonnet-4-6"]
	}
	const m = 1_000_000.0
	return (float64(in)*p.input +
		float64(out)*p.output +
		float64(cacheCreate)*p.cacheWrite5m +
		float64(cacheRead)*p.cacheRead) / m
}
