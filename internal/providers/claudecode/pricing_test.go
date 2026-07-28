package claudecode

import (
	"math"
	"testing"
)

func TestCanonicalModel(t *testing.T) {
	cases := map[string]string{
		"claude-fable-5":            "claude-fable-5",
		"claude-opus-5":             "claude-opus-5",
		"claude-sonnet-5":           "claude-sonnet-5",
		"claude-opus-5[1m]":         "claude-opus-5",    // context-window suffix
		"claude-haiku-4-5-20251001": "claude-haiku-4-5", // dated variant
		"opus":                      "claude-opus-5",    // bare tier aliases
		"sonnet":                    "claude-sonnet-5",
		"haiku":                     "claude-haiku-4-5",
		"<synthetic>":               "",
		"":                          "",
	}
	for in, want := range cases {
		if got := canonicalModel(in); got != want {
			t.Errorf("canonicalModel(%q) = %q, want %q", in, got, want)
		}
	}
}

// The models that were previously missing from the table fell through to
// Sonnet rates; the Opus 4.x rows were priced at the old $15/$75.
func TestPricedModelsAreDistinct(t *testing.T) {
	oneM := int64(1_000_000)
	cases := map[string]float64{
		"claude-fable-5":   10, // was billed as Sonnet ($3)
		"claude-opus-5":    5,  // was billed as Sonnet ($3)
		"claude-sonnet-5":  3,
		"claude-opus-4-8":  5, // was $15
		"claude-opus-4-7":  5, // was $15
		"claude-opus-4-6":  5, // was $15
		"claude-haiku-4-5": 1,
	}
	for model, wantInputCost := range cases {
		got := costFor(model, oneM, 0, 0, 0)
		if math.Abs(got-wantInputCost) > 1e-9 {
			t.Errorf("costFor(%q, 1M input) = %.4f, want %.4f", model, got, wantInputCost)
		}
	}
}

func TestUnknownModelFallsBack(t *testing.T) {
	got := costFor("claude-something-new", 1_000_000, 0, 0, 0)
	if math.Abs(got-3) > 1e-9 {
		t.Errorf("unknown model = %.4f, want Sonnet fallback 3.0000", got)
	}
}

func TestModelLabels(t *testing.T) {
	cases := map[string]string{
		"claude-fable-5":    "Fable 5",
		"claude-opus-5":     "Opus 5",
		"claude-opus-5[1m]": "Opus 5",
		"claude-sonnet-5":   "Sonnet 5",
		"claude-opus-4-8":   "Opus 4.8",
		"opus":              "Opus 5",
		"":                  "unknown",
		"<synthetic>":       "<synthetic>", // unrecognised IDs pass through
	}
	for in, want := range cases {
		if got := modelLabel(in); got != want {
			t.Errorf("modelLabel(%q) = %q, want %q", in, got, want)
		}
	}
}
