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

// Load reads the config file. On first run (file missing) it writes the
// default template and returns the zero-value Config. Caller should not
// treat first-run as an error.
func Load() (Config, string, error) {
	path, err := Path()
	if err != nil {
		return Config{}, "", err
	}

	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if writeErr := writeDefault(path); writeErr != nil {
			return Config{}, path, fmt.Errorf("write default config: %w", writeErr)
		}
		return Config{}, path, nil
	}
	if err != nil {
		return Config{}, path, err
	}

	var cfg Config
	if _, err := toml.Decode(string(b), &cfg); err != nil {
		return Config{}, path, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, path, nil
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
#   "prompt"  → "<project> — <last user prompt>" (default)
#   "project" → project name only, hides prompt text
#   "off"     → no footer at all
note = "prompt"

# Optional USD budgets. Leave at 0 to show a time-elapsed bar instead.
# Set a number if you've decided what a fair budget looks like for you.
session_budget = 0
daily_budget   = 0
month_budget   = 0
`
