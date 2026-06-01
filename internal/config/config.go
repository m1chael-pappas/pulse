// Package config loads pulse's user configuration from
// $XDG_CONFIG_HOME/pulse/config.toml (or the OS-native equivalent).
//
// Design rules:
//   - Missing file → defaults, no error. First run writes a commented
//     template so the user can discover what's configurable.
//   - Unknown keys are tolerated (TOML "strict" off) so older binaries
//     don't choke on newer config files.
//   - Every value has a sensible zero default; nothing here should panic
//     a fresh user.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config is the root of pulse's TOML config.
type Config struct {
	ClaudeCode ClaudeCodeConfig `toml:"claudecode"`
	GCal       GCalConfig       `toml:"gcal"`
	MacCal     MacCalConfig     `toml:"maccal"`
	GitHub     GitHubConfig     `toml:"github"`
}

// GitHubConfig configures the GitHub PR tile. Uses the gh CLI's auth
// so no token belongs here. Enabled is a pointer so we can distinguish
// "not set in config" (default → on) from "explicitly off".
type GitHubConfig struct {
	Enabled *bool    `toml:"enabled"`
	Limit   int      `toml:"limit"`
	Repos   []string `toml:"repos"`    // optional: ["owner/name", ...]
	Orgs    []string `toml:"orgs"`     // optional: ["SafetyCulture", ...]
	ShowAll bool     `toml:"show_all"` // show every open PR (not just yours)
}

// IsEnabled returns whether the tile should appear. Missing key = on,
// explicit false = off.
func (g GitHubConfig) IsEnabled() bool {
	if g.Enabled == nil {
		return true
	}
	return *g.Enabled
}

// MacCalConfig configures the macOS Calendar.app provider. Works with any
// account already synced into Calendar.app (Google, iCloud, Exchange).
// First run triggers a TCC permission prompt; after Allow it's silent.
type MacCalConfig struct {
	// Enabled toggles the tile. macOS-only — silently ignored elsewhere.
	// Pointer so "not set" defaults to enabled; explicit false turns it off.
	Enabled *bool `toml:"enabled"`

	// Calendars restricts to specific calendar names. Empty = all visible.
	Calendars []string `toml:"calendars"`

	// Lookahead caps event count and time window.
	Lookahead     int `toml:"lookahead"`
	LookaheadDays int `toml:"lookahead_days"`
}

// IsEnabled returns whether the maccal tile should appear.
func (m MacCalConfig) IsEnabled() bool {
	if m.Enabled == nil {
		return true
	}
	return *m.Enabled
}

// GCalConfig configures the Google Calendar provider via a public iCal
// subscription URL. Pulse never touches Google OAuth — the secret URL
// granted by Google Calendar Settings → Integrate Calendar is all we use.
type GCalConfig struct {
	// ICSURL is the "Secret address in iCal format" from Google Calendar.
	// Empty disables the gcal tile entirely.
	ICSURL string `toml:"ics_url"`

	// Lookahead caps how many future events to surface (default 6).
	Lookahead int `toml:"lookahead"`

	// LookaheadDays caps the time window (default 7).
	LookaheadDays int `toml:"lookahead_days"`
}

// ClaudeCodeConfig configures the Claude Code provider.
type ClaudeCodeConfig struct {
	// Note controls the footer line on the tile:
	//   "prompt"  → "<project> — <last user prompt>" (default)
	//   "project" → "<project>" only (hides prompt text)
	//   "off"     → no footer
	Note string `toml:"note"`

	// Optional USD budget overrides. Leave at 0 to render the time-elapsed
	// bar instead. Anthropic doesn't publish per-window dollar caps for
	// Max plans, so pulse never invents one — but if you've decided what a
	// fair budget is for you, set it here.
	SessionBudget float64 `toml:"session_budget"`
	DailyBudget   float64 `toml:"daily_budget"`
	MonthBudget   float64 `toml:"month_budget"`
}

// Path returns the absolute path to pulse's config file.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "pulse", "config.toml"), nil
}

// LoadResult holds everything the loader can communicate to its caller.
type LoadResult struct {
	Config     Config
	Path       string
	FirstRun   bool // true when the loader had to create the config file
}

// Load reads the config file. On first run (file missing) it writes the
// default template and returns the zero-value Config plus FirstRun=true
// so the caller can print a setup banner. Missing file is not an error.
func Load() (LoadResult, error) {
	out := LoadResult{}
	path, err := Path()
	if err != nil {
		return out, err
	}
	out.Path = path

	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if writeErr := writeDefault(path); writeErr != nil {
			return out, fmt.Errorf("write default config: %w", writeErr)
		}
		out.FirstRun = true
		return out, nil
	}
	if err != nil {
		return out, err
	}

	if _, err := toml.Decode(string(b), &out.Config); err != nil {
		return out, fmt.Errorf("parse %s: %w", path, err)
	}
	return out, nil
}

func writeDefault(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(defaultTemplate), 0o644)
}

const defaultTemplate = `# pulse config — auto-generated on first run.
# Safe to edit. Unknown keys are ignored, so newer pulse versions stay
# compatible with older config files.

[claudecode]
# Footer note privacy:
#   "off"     → no footer at all (default — keeps prompts out of screenshots)
#   "project" → project name only, hides prompt text
#   "prompt"  → "<project> — <last user prompt>"
note = "off"

# Optional USD budgets. Leave at 0 to show a time-elapsed bar instead.
# Set a number if you've decided what a fair budget looks like for you.
session_budget = 0
daily_budget   = 0
month_budget   = 0

[maccal]
# Read upcoming events from macOS Calendar.app. Works with any account
# Calendar.app is already syncing (Google via System Settings → Internet
# Accounts, iCloud, Exchange). First run pops a one-time permission
# prompt — click Allow once and it's silent forever.
enabled        = true
calendars      = []   # empty = every visible calendar; or e.g. ["Work", "Home"]
lookahead      = 6
lookahead_days = 7

[gcal]
# Fallback for non-macOS hosts: a Google Calendar "Secret address in iCal
# format". Most work calendars disable this surface — prefer [maccal] on
# a Mac. Anyone with this URL can read your events.
ics_url        = ""
lookahead      = 6
lookahead_days = 7

[github]
# Open PRs you authored + ones awaiting your review. Uses the gh CLI's
# auth — run 'gh auth login' first if you haven't.
enabled  = true
limit    = 5   # max PRs per section
# Scope: combine repos and orgs (OR'd by GitHub). Leave both empty to
# search all of GitHub.
repos    = []  # e.g. ["SafetyCulture/safetyculture-webverse"]
orgs    = []   # e.g. ["SafetyCulture"]
# When true, show every open PR in the scoped repos/orgs sorted by recent
# activity — useful for whole-repo awareness. When false (default), only
# show ones you authored or are asked to review.
show_all = false
`
