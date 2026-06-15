package claudecode

// Per-million-token USD prices. Estimates only — the Anthropic Admin API is
// the source of truth for billed amounts. Update as models change.
type pricing struct {
	input        float64
	output       float64
	cacheWrite5m float64
	cacheWrite1h float64
	cacheRead    float64
}

var modelPricing = map[string]pricing{
	"claude-opus-4-8":   {input: 15, output: 75, cacheWrite5m: 18.75, cacheWrite1h: 30, cacheRead: 1.5},
	"claude-opus-4-7":   {input: 15, output: 75, cacheWrite5m: 18.75, cacheWrite1h: 30, cacheRead: 1.5},
	"claude-opus-4-6":   {input: 15, output: 75, cacheWrite5m: 18.75, cacheWrite1h: 30, cacheRead: 1.5},
	"claude-sonnet-4-6": {input: 3, output: 15, cacheWrite5m: 3.75, cacheWrite1h: 6, cacheRead: 0.3},
	"claude-haiku-4-5":  {input: 1, output: 5, cacheWrite5m: 1.25, cacheWrite1h: 2, cacheRead: 0.1},
}

func costFor(model string, in, out, cacheCreate, cacheRead int64) float64 {
	p, ok := modelPricing[model]
	if !ok {
		p = modelPricing["claude-sonnet-4-6"]
	}
	const m = 1_000_000.0
	return (float64(in)*p.input +
		float64(out)*p.output +
		float64(cacheCreate)*p.cacheWrite5m +
		float64(cacheRead)*p.cacheRead) / m
}
