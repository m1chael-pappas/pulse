package claudecode

import (
	"encoding/json"
	"errors"
	"time"
)

// oauthCredential is the Claude CLI Keychain payload shape.
// Wrapper key is "claudeAiOauth".
type oauthCredential struct {
	AccessToken      string   `json:"accessToken"`
	RefreshToken     string   `json:"refreshToken"`
	ExpiresAtMillis  int64    `json:"expiresAt"`
	Scopes           []string `json:"scopes"`
	SubscriptionType string   `json:"subscriptionType"`
}

func (c oauthCredential) ExpiresAt() time.Time {
	if c.ExpiresAtMillis == 0 {
		return time.Time{}
	}
	return time.UnixMilli(c.ExpiresAtMillis)
}

func (c oauthCredential) Expired() bool {
	exp := c.ExpiresAt()
	return exp.IsZero() || time.Now().After(exp)
}

func parseOAuthCredential(blob []byte) (oauthCredential, error) {
	var wrapper struct {
		ClaudeAIOAuth *oauthCredential `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(blob, &wrapper); err != nil {
		return oauthCredential{}, err
	}
	if wrapper.ClaudeAIOAuth == nil || wrapper.ClaudeAIOAuth.AccessToken == "" {
		return oauthCredential{}, errors.New("credential blob has no claudeAiOauth.accessToken")
	}
	return *wrapper.ClaudeAIOAuth, nil
}
