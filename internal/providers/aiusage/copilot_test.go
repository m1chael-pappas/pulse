package aiusage

import (
	"testing"
	"time"

	"github.com/m1chael-pappas/pulse/internal/providers"
)

func TestParseCopilotAccount(t *testing.T) {
	body := []byte(`{
  "copilot_plan": "business",
  "quota_reset_date_utc": "2026-12-01T00:00:00.000Z",
  "quota_snapshots": {
    "chat": {"credits_used": 0},
    "premium_interactions": {"credits_used": 5320}
  }
}`)
	got, err := parseCopilotAccount(body)
	if err != nil {
		t.Fatal(err)
	}
	want := copilotAccount{Plan: "business", SpendUSD: 53.20, ResetsAt: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)}
	if got.Plan != want.Plan || got.SpendUSD != want.SpendUSD || !got.ResetsAt.Equal(want.ResetsAt) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestParseCopilotModels(t *testing.T) {
	got, err := parseCopilotModels([]byte(`[{"model":"gemini-4-argon","tokens":10968246,"nano_aiu":270200000000}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Tokens != 10968246 || got[0].SpendUSD < 2.70 || got[0].SpendUSD > 2.71 {
		t.Fatalf("got %+v", got)
	}
	if empty, err := parseCopilotModels([]byte("\n")); err != nil || empty != nil {
		t.Fatalf("empty output: got %+v, %v", empty, err)
	}
}

func TestWithModelRowsSortsBySpend(t *testing.T) {
	snap := providers.Snapshot{Sections: []providers.Section{{Rows: []providers.BreakdownEntry{{Label: "claude", Value: 5}}}}}
	got := withModelRows(snap, []providers.BreakdownEntry{{Label: "gemini", Value: 9}})
	rows := got.Sections[0].Rows
	if len(rows) != 2 || rows[0].Label != "gemini" || rows[1].Label != "claude" {
		t.Fatalf("got %+v", rows)
	}
}
