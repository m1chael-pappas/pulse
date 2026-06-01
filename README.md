# pulse

A terminal dashboard for your dev day. One tile per data source — Claude Code usage, system stats, GitHub PRs, your calendar — refreshing live.

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea) + [Lip Gloss](https://github.com/charmbracelet/lipgloss).

## Install

```bash
brew install go             # if you don't have Go
git clone https://github.com/michaelpappas/pulse.git
cd pulse
make install                # → ~/go/bin/pulse, plus a PATH hint if needed
pulse doctor                # verify setup
```

If `pulse: command not found` after install, add Go's bin dir to your PATH once:

```bash
echo 'export PATH="$HOME/go/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

## Development

```bash
make dev          # run from source (no install) — use this while iterating
make install      # reinstall the global `pulse` binary
make check        # vet + tests
make help         # list all targets
```

`make dev` compiles fresh every run, so source changes are picked up immediately. `make install` snapshots the current code into the global binary — re-run when you want to update what `pulse` (from anywhere) points to.

## First run

```bash
pulse
```

First launch writes a default config to your OS config dir (`~/Library/Application Support/pulse/` on macOS, `~/.config/pulse/` on Linux). You can find it with:

```bash
pulse config          # prints the path
```

Keys inside the TUI: `tab` / `shift+tab` cycles focus, `r` refreshes, `q` quits.

## What you get out of the box

| Tile | Data source | Auth |
|---|---|---|
| **Claude** | `~/.claude/projects/*.jsonl` + `api.anthropic.com/api/oauth/usage` | macOS Keychain (Claude CLI) |
| **System** | CPU / memory / disk / load via gopsutil | none |
| **GitHub** | Open PRs (yours + review queue) via `gh` CLI | `gh auth login` |
| **Calendar** | macOS Calendar.app via `icalBuddy` | macOS Calendar permission |

Optional dependencies (install only what you want to use):

```bash
brew install gh ical-buddy
```

## Adding a data source

Every feed implements `providers.Provider`:

```go
type Provider interface {
    Name() string
    Refresh(ctx context.Context) Snapshot
    Interval() time.Duration
    PreferredWidth() int
}
```

1. Create `internal/providers/<name>/` with a type that implements the interface.
2. Wire it into `ui.NewApp` in `internal/ui/app.go`.
3. Add a config struct to `internal/config/config.go` if it needs settings.

Snapshots are rendered by the shared tile renderer in `internal/ui/tile/`, so you don't write any UI code — just populate the fields (Windows, Sections, Stats, Events, History, Breakdown) and the tile draws them.

## Subcommands

```bash
pulse                 # run the TUI
pulse config          # print the config file path
pulse doctor          # check PATH, deps, calendar/keychain access
pulse usage           # one-shot fetch of Claude OAuth usage (raw JSON)
pulse --help          # this help
```

## Layout

```
cmd/pulse/                  entrypoint
internal/
  ui/                       Bubble Tea root model + grid layout
    tile/                   shared tile renderer
  providers/                pluggable data feeds
    claudecode/  system/  github/  maccal/  gcal/
  config/                   config.toml loader (~/.config/pulse/)
```
