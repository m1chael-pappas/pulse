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

	"github.com/michaelpappas/pulse/internal/providers"
)

type Provider struct {
	// Limit caps PRs per section (authored + review queue). Default 5.
	Limit int
}

func New(limit int) *Provider {
	if limit <= 0 {
		limit = 5
	}
	return &Provider{Limit: limit}
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

	authored, review, err := fetchPRs(ctx, login, p.Limit)
	if err != nil {
		snap.Status = providers.StatusWarn
		snap.Err = err
		snap.Subtitle = "query failed: " + clipErr(err.Error())
		return snap
	}

	if section := buildSection("Your PRs", authored); section != nil {
		snap.Sections = append(snap.Sections, *section)
	}
	if section := buildSection("Review queue", review); section != nil {
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

	// Flag worst-case CI on one of YOUR PRs as a status warning.
	for _, pr := range authored {
		if pr.Checks == "FAILURE" || pr.Checks == "ERROR" {
			snap.Status = providers.StatusIncident
			break
		}
		if pr.Checks == "PENDING" {
			snap.Status = providers.StatusWarn
		}
	}

	return snap
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

const prsQuery = `
query($authored: String!, $review: String!, $limit: Int!) {
  authored: search(query: $authored, type: ISSUE, first: $limit) {
    nodes { ... on PullRequest {
      number title url isDraft createdAt updatedAt
      repository { nameWithOwner }
      statusCheckRollup { state }
    } }
  }
  review: search(query: $review, type: ISSUE, first: $limit) {
    nodes { ... on PullRequest {
      number title url isDraft createdAt updatedAt
      repository { nameWithOwner }
      statusCheckRollup { state }
    } }
  }
}`

func fetchPRs(ctx context.Context, login string, limit int) (authored, review []pr, err error) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	authoredQuery := fmt.Sprintf("is:pr is:open author:%s archived:false", login)
	reviewQuery := fmt.Sprintf("is:pr is:open review-requested:%s archived:false", login)

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

// buildSection turns a PR list into a labelled Section. Each PR row's
// label embeds the CI icon, the PR number, a short repo, and the title;
// the value cell holds the age ("3h", "2d") so it right-aligns cleanly.
func buildSection(title string, prs []pr) *providers.Section {
	if len(prs) == 0 {
		return nil
	}
	rows := make([]providers.BreakdownEntry, 0, len(prs))
	for _, pr := range prs {
		icon := checkIcon(pr.Checks, pr.IsDraft)
		repo := shortRepo(pr.Repository.NameWithOwner)
		// "✓ #1106 webverse · feat(ui): import 128 icons…"
		label := fmt.Sprintf("%s #%d %s · %s", icon, pr.Number, repo, pr.Title)
		rows = append(rows, providers.BreakdownEntry{
			Label: label,
			Value: float64(time.Since(pr.UpdatedAt).Seconds()),
			Unit:  unitAge,
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
		return "○"
	}
	switch strings.ToUpper(state) {
	case "SUCCESS":
		return "✓"
	case "FAILURE", "ERROR":
		return "✗"
	case "PENDING", "EXPECTED":
		return "⋯"
	default:
		return "·"
	}
}

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
