# pulse

A terminal dashboard for your dev day. Pluggable data sources — start with AI usage/cost, calendar, and recent activity; extend to anything (GitHub, Linear, deploys, Slack digests…).

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea) + [Lip Gloss](https://github.com/charmbracelet/lipgloss).

## Run

```bash
brew install go            # if you don't have Go
go mod tidy
go run ./cmd/pulse
```

Keys: `tab` / `shift+tab` cycle panels, `q` or `ctrl+c` quits.

## Adding a data source

Every feed implements `internal/sources.Source`:

```go
type Source interface {
    Name() string
    Refresh(ctx context.Context) (Snapshot, error)
    Interval() time.Duration
}
```

1. Create `internal/sources/<name>/` with a type that implements the interface.
2. Add a panel under `internal/ui/panels/` that renders its `Snapshot`.
3. Wire it into `ui.NewApp` in `internal/ui/app.go`.

## Roadmap

- [ ] Anthropic Admin API (usage + cost reports)
- [ ] Claude Code session parser (`~/.claude/projects/**/*.jsonl`)
- [ ] Google Calendar (OAuth loopback flow)
- [ ] OpenAI usage
- [ ] GitHub PR / CI status
- [ ] Linear / Jira tickets
- [ ] Cross-platform release via [goreleaser](https://goreleaser.com/)
- [ ] Homebrew tap for one-line install

## Layout

```
cmd/pulse/                entrypoint
internal/
  ui/                     Bubble Tea root model
    panels/               one model per grid cell
  sources/                pluggable data feeds
    anthropic/  claudecode/  gcal/
  config/                 ~/.config/pulse/config.toml
  cache/                  local offline cache
```
