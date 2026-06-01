// Package github surfaces the user's open GitHub PRs — both ones they
// authored and ones awaiting their review — with CI check status and age.
//
// Uses the `gh` CLI's GraphQL passthrough so we inherit whichever auth
// the user has already set up (`gh auth login` / `gh auth status`).
// Zero new credentials, zero config.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/m1chael-pappas/pulse/internal/providers"
)

type Provider struct {
	// Limit caps PRs per section. Default 5.
	Limit int
	// Repos and Orgs scope the GitHub-wide search. Empty = no scoping.
	Repos []string
	Orgs  []string
	// ShowAll switches the tile from "yours + review queue" to "every
	// open PR in the scoped repos/orgs, sorted by recent activity".
	ShowAll bool
}

func New(limit int, repos, orgs []string, showAll bool) *Provider {
	if limit <= 0 {
		limit = 5
	}
	return &Provider{Limit: limit, Repos: repos, Orgs: orgs, ShowAll: showAll}
}

func (p *Provider) Name() string             { return "GitHub" }
func (p *Provider) Interval() time.Duration  { return 60 * time.Second }
func (p *Provider) PreferredWidth() int      { return 64 }

func (p *Provider) Refresh(ctx context.Context) providers.Snapshot {
	snap := providers.Snapshot{Name: p.Name(), Status: providers.StatusOK}

	if _, err := exec.LookPath("gh"); err != nil {
		snap.Status = providers.StatusUnknown
		snap.Subtitle = "install gh CLI: brew install gh"
		return snap
	}

	login, err := currentLogin(ctx)
	if err != nil {
		snap.Status = providers.StatusWarn
		snap.Subtitle = "not signed in — run `gh auth login`"
		snap.Err = err
		return snap
	}
	snap.Header = "@" + login

	scope := p.scopeQuery()

	if p.ShowAll {
		all, err := fetchAllOpen(ctx, p.Limit, scope)
		if err != nil {
			snap.Status = providers.StatusWarn
			snap.Err = err
			snap.Subtitle = "query failed: " + clipErr(err.Error())
			return snap
		}
		if section := buildSection("Open PRs", all, login); section != nil {
			snap.Sections = append(snap.Sections, *section)
		}
		switch len(all) {
		case 0:
			snap.Subtitle = "no open PRs in scope"
		case 1:
			snap.Subtitle = "1 open PR in scope"
		default:
			snap.Subtitle = fmt.Sprintf("%d open PRs in scope", len(all))
		}
		setStatusFromMine(&snap, all, login)
		return snap
	}

	authored, review, err := fetchPRs(ctx, login, p.Limit, scope)
	if err != nil {
		snap.Status = providers.StatusWarn
		snap.Err = err
		snap.Subtitle = "query failed: " + clipErr(err.Error())
		return snap
	}
	if section := buildSection("Your PRs", authored, login); section != nil {
		snap.Sections = append(snap.Sections, *section)
	}
	if section := buildSection("Review queue", review, login); section != nil {
		snap.Sections = append(snap.Sections, *section)
	}

	total := len(authored) + len(review)
	switch total {
	case 0:
		snap.Subtitle = "no open PRs · all clear"
	case 1:
		snap.Subtitle = "1 PR needs attention"
	default:
		snap.Subtitle = fmt.Sprintf("%d PRs need attention", total)
	}
	setStatusFromMine(&snap, authored, login)
	return snap
}

// setStatusFromMine flags the tile red/amber when one of the user's own
// PRs in the result set has failing or pending CI.
func setStatusFromMine(snap *providers.Snapshot, prs []pr, login string) {
	for _, pr := range prs {
		if pr.Author.Login != login {
			continue
		}
		switch pr.Checks {
		case "FAILURE", "ERROR":
			snap.Status = providers.StatusIncident
			return
		case "PENDING":
			snap.Status = providers.StatusWarn
		}
	}
}

// pr captures the fields we render. JSON tags match the GraphQL field
// shape so we can decode the response directly.
type pr struct {
	Number     int       `json:"number"`
	Title      string    `json:"title"`
	URL        string    `json:"url"`
	IsDraft    bool      `json:"isDraft"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
	Author     struct {
		Login string `json:"login"`
	} `json:"author"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	StatusCheckRollup struct {
		State string `json:"state"`
	} `json:"statusCheckRollup"`
	// Computed.
	Checks string `json:"-"`
}

func currentLogin(ctx context.Context) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, "gh", "api", "user", "--jq", ".login").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

const prFields = `
  number title url isDraft createdAt updatedAt
  author { login }
  repository { nameWithOwner }
  statusCheckRollup { state }
`

var prsQuery = `
query($authored: String!, $review: String!, $limit: Int!) {
  authored: search(query: $authored, type: ISSUE, first: $limit) {
    nodes { ... on PullRequest {` + prFields + `} }
  }
  review: search(query: $review, type: ISSUE, first: $limit) {
    nodes { ... on PullRequest {` + prFields + `} }
  }
}`

var allOpenQuery = `
query($q: String!, $limit: Int!) {
  search(query: $q, type: ISSUE, first: $limit) {
    nodes { ... on PullRequest {` + prFields + `} }
  }
}`

// scopeQuery returns the trailing 'repo:foo/bar org:foo' fragment that
// restricts a GitHub search to the user's configured repos / orgs.
// Multiple values are OR'd by GitHub's search engine.
func (p *Provider) scopeQuery() string {
	parts := make([]string, 0, len(p.Repos)+len(p.Orgs))
	for _, r := range p.Repos {
		if r = strings.TrimSpace(r); r != "" {
			parts = append(parts, "repo:"+r)
		}
	}
	for _, o := range p.Orgs {
		if o = strings.TrimSpace(o); o != "" {
			parts = append(parts, "org:"+o)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " ")
}

func fetchPRs(ctx context.Context, login string, limit int, scope string) (authored, review []pr, err error) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	authoredQuery := fmt.Sprintf("is:pr is:open author:%s archived:false%s", login, scope)
	reviewQuery := fmt.Sprintf("is:pr is:open review-requested:%s archived:false%s", login, scope)

	out, err := exec.CommandContext(cctx, "gh", "api", "graphql",
		"-f", "query="+prsQuery,
		"-f", "authored="+authoredQuery,
		"-f", "review="+reviewQuery,
		"-F", fmt.Sprintf("limit=%d", limit),
	).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, nil, fmt.Errorf("gh: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, nil, err
	}

	var resp struct {
		Data struct {
			Authored struct{ Nodes []pr } `json:"authored"`
			Review   struct{ Nodes []pr } `json:"review"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, nil, fmt.Errorf("decode gh response: %w", err)
	}
	if len(resp.Errors) > 0 {
		return nil, nil, fmt.Errorf("gh graphql: %s", resp.Errors[0].Message)
	}
	for i := range resp.Data.Authored.Nodes {
		resp.Data.Authored.Nodes[i].Checks = resp.Data.Authored.Nodes[i].StatusCheckRollup.State
	}
	for i := range resp.Data.Review.Nodes {
		resp.Data.Review.Nodes[i].Checks = resp.Data.Review.Nodes[i].StatusCheckRollup.State
	}
	return resp.Data.Authored.Nodes, resp.Data.Review.Nodes, nil
}

// fetchAllOpen returns every open PR in the configured scope, sorted
// by recent activity. Used when [github].show_all is true.
func fetchAllOpen(ctx context.Context, limit int, scope string) ([]pr, error) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	query := "is:pr is:open archived:false sort:updated-desc" + scope
	out, err := exec.CommandContext(cctx, "gh", "api", "graphql",
		"-f", "query="+allOpenQuery,
		"-f", "q="+query,
		"-F", fmt.Sprintf("limit=%d", limit*2), // give a bit more for whole-repo view
	).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("gh: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, err
	}
	var resp struct {
		Data struct {
			Search struct{ Nodes []pr } `json:"search"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("decode gh response: %w", err)
	}
	if len(resp.Errors) > 0 {
		return nil, fmt.Errorf("gh graphql: %s", resp.Errors[0].Message)
	}
	for i := range resp.Data.Search.Nodes {
		resp.Data.Search.Nodes[i].Checks = resp.Data.Search.Nodes[i].StatusCheckRollup.State
	}
	return resp.Data.Search.Nodes, nil
}

// buildSection turns a PR list into a labelled Section. Each PR row's
// label embeds the CI icon, an optional "(you)" marker when the user
// authored the PR, the PR number, a short repo, and the title; the
// value cell holds the age ("3h", "2d") so it right-aligns cleanly.
// The URL field carries the github.com link so the tile renderer can
// emit OSC 8 escapes that make rows cmd-clickable in modern terminals.
func buildSection(title string, prs []pr, login string) *providers.Section {
	if len(prs) == 0 {
		return nil
	}
	rows := make([]providers.BreakdownEntry, 0, len(prs))
	for _, pr := range prs {
		repo := shortRepo(pr.Repository.NameWithOwner)
		who := ""
		if pr.Author.Login != "" && pr.Author.Login != login {
			who = " @" + pr.Author.Login
		}
		// e.g. "#1106 webverse @teammate · feat(ui): import 128 icons…"
		// (the green/red ✓/✗ glyph is rendered separately by the tile)
		label := fmt.Sprintf("#%d %s%s · %s", pr.Number, repo, who, pr.Title)
		rows = append(rows, providers.BreakdownEntry{
			Glyph: checkIcon(pr.Checks, pr.IsDraft),
			Label: label,
			Value: float64(time.Since(pr.UpdatedAt).Seconds()),
			Unit:  unitAge,
			URL:   pr.URL,
		})
	}
	return &providers.Section{
		Title: fmt.Sprintf("%s (%d)", title, len(prs)),
		Rows:  rows,
	}
}

// unitAge is a UnitCount alias rendered as a human duration by the tile.
const unitAge providers.Unit = "age"

func checkIcon(state string, isDraft bool) string {
	if isDraft {
		return dimStyle.Render("○")
	}
	switch strings.ToUpper(state) {
	case "SUCCESS":
		return greenStyle.Render("✓")
	case "FAILURE", "ERROR":
		return redStyle.Render("✗")
	case "PENDING", "EXPECTED":
		return amberStyle.Render("⋯")
	default:
		return dimStyle.Render("·")
	}
}

// CI-state glyph palette. Same colors as the Claude Status tile so users
// learn one mapping (green = good, red = bad, amber = in flight).
var (
	greenStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Bold(true)
	redStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	amberStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
)

func shortRepo(full string) string {
	if i := strings.LastIndex(full, "/"); i >= 0 && i < len(full)-1 {
		return full[i+1:]
	}
	return full
}

func clipErr(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 60 {
		return s[:57] + "…"
	}
	return s
}
