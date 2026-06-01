package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Account is the currently logged-in Claude Code account, read from
// ~/.claude.json → oauthAccount. Claude Code doesn't tag per-session logs
// with the account that ran them, so this represents only "right now".
type Account struct {
	AccountUUID      string
	OrganizationUUID string
	OrganizationName string
	OrganizationType string // e.g. "claude_max", "claude_team"
	EmailAddress     string
	DisplayName      string
	RateLimitTier    string
	BillingType      string
}

// PlanLabel returns a human-readable plan name for the organizationType.
func (a Account) PlanLabel() string {
	switch a.OrganizationType {
	case "claude_max":
		return "Max"
	case "claude_team":
		return "Team"
	case "claude_enterprise":
		return "Enterprise"
	case "claude_free":
		return "Free"
	case "claude_pro":
		return "Pro"
	case "":
		return ""
	default:
		return a.OrganizationType
	}
}

func readAccount() (Account, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Account{}, err
	}
	path := filepath.Join(home, ".claude.json")
	f, err := os.Open(path)
	if err != nil {
		return Account{}, err
	}
	defer f.Close()

	var wrapper struct {
		OAuthAccount struct {
			AccountUUID               string `json:"accountUuid"`
			OrganizationUUID          string `json:"organizationUuid"`
			OrganizationName          string `json:"organizationName"`
			OrganizationType          string `json:"organizationType"`
			OrganizationRateLimitTier string `json:"organizationRateLimitTier"`
			EmailAddress              string `json:"emailAddress"`
			DisplayName               string `json:"displayName"`
			BillingType               string `json:"billingType"`
		} `json:"oauthAccount"`
	}
	if err := json.NewDecoder(f).Decode(&wrapper); err != nil {
		return Account{}, err
	}
	o := wrapper.OAuthAccount
	return Account{
		AccountUUID:      o.AccountUUID,
		OrganizationUUID: o.OrganizationUUID,
		OrganizationName: o.OrganizationName,
		OrganizationType: o.OrganizationType,
		EmailAddress:     o.EmailAddress,
		DisplayName:      o.DisplayName,
		RateLimitTier:    o.OrganizationRateLimitTier,
		BillingType:      o.BillingType,
	}, nil
}

// accountSwitch is one observed account being active at a point in time.
// Persisted to ~/.config/pulse/account-history.json so future runs can
// attribute past sessions (only those after pulse first ran).
type accountSwitch struct {
	AccountUUID      string    `json:"account_uuid"`
	OrganizationUUID string    `json:"organization_uuid"`
	OrganizationName string    `json:"organization_name"`
	EmailAddress     string    `json:"email"`
	ObservedAt       time.Time `json:"observed_at"`
}

func historyPath() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cfg, "pulse")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "account-history.json"), nil
}

// recordAccountIfChanged appends a switch entry when the active account
// differs from the most recent recorded one. Returns (changed, err).
// Best-effort — callers may safely ignore the error.
func recordAccountIfChanged(curr Account) (bool, error) {
	if curr.AccountUUID == "" {
		return false, nil
	}
	path, err := historyPath()
	if err != nil {
		return false, err
	}
	var history []accountSwitch
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &history)
	}
	if len(history) > 0 && history[len(history)-1].AccountUUID == curr.AccountUUID {
		return false, nil
	}
	history = append(history, accountSwitch{
		AccountUUID:      curr.AccountUUID,
		OrganizationUUID: curr.OrganizationUUID,
		OrganizationName: curr.OrganizationName,
		EmailAddress:     curr.EmailAddress,
		ObservedAt:       time.Now(),
	})
	b, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return false, err
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return false, fmt.Errorf("write account history: %w", err)
	}
	return true, nil
}
