package claudecode

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"
	"time"
)

// keychainService is the macOS Keychain service name Claude CLI writes its
// OAuth credentials under. Confirmed by inspecting an actual install.
const keychainService = "Claude Code-credentials"

// ErrKeychainUnavailable is returned when the macOS `security` CLI isn't
// available (non-macOS, or stripped image).
var ErrKeychainUnavailable = errors.New("macOS security CLI unavailable")

// ErrKeychainNotFound is returned when no matching keychain item exists.
// Most likely cause: user hasn't logged in to Claude Code yet, or is on a
// non-macOS host.
var ErrKeychainNotFound = errors.New("no Claude Code credential in Keychain")

// ErrKeychainDenied is returned when the user dismissed the Keychain access
// prompt. They need to re-run and pick "Always Allow".
var ErrKeychainDenied = errors.New("Keychain access denied — re-run and pick Always Allow")

// readKeychainCredential returns the raw JSON blob Claude CLI stored in
// Keychain, looking up by service + (optionally) account.
//
// The first call ever will trigger a Keychain access prompt that the user
// must approve. "Always Allow" makes future calls silent.
func readKeychainCredential(ctx context.Context, account string) ([]byte, error) {
	if runtime.GOOS != "darwin" {
		return nil, ErrKeychainUnavailable
	}
	if _, err := exec.LookPath("/usr/bin/security"); err != nil {
		return nil, ErrKeychainUnavailable
	}

	args := []string{"find-generic-password", "-s", keychainService, "-w"}
	if account != "" {
		args = append(args, "-a", account)
	}

	// Hard timeout so a stuck prompt doesn't hang the UI.
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	out, err := exec.CommandContext(cctx, "/usr/bin/security", args...).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			stderr := strings.ToLower(string(exitErr.Stderr))
			switch {
			case strings.Contains(stderr, "could not be found"):
				return nil, ErrKeychainNotFound
			case strings.Contains(stderr, "user canceled") ||
				strings.Contains(stderr, "user denied") ||
				strings.Contains(stderr, "interaction is not allowed"):
				return nil, ErrKeychainDenied
			}
		}
		if errors.Is(cctx.Err(), context.DeadlineExceeded) {
			return nil, ErrKeychainDenied // most likely the prompt
		}
		return nil, err
	}
	return []byte(strings.TrimSpace(string(out))), nil
}

// keychainAccountCandidates returns the Keychain account names Claude Code may
// have stored the primary credential under, most-likely first.
//
// Claude Code 2.1.x (≈2.1.175) split its Keychain layout: the primary
// claudeAiOauth blob moved to an account keyed on the OS username, while the
// older shared "claude-code-user" account is left holding only mcpOAuth (the
// per-MCP-server tokens). Older builds scoped by email or stored everything
// under the unscoped item. We try the OS username and email first, then fall
// back to an unscoped lookup — but callers MUST validate that the returned
// blob actually contains claudeAiOauth, since the unscoped/legacy item now
// resolves to the mcpOAuth-only payload on current builds.
func keychainAccountCandidates(email string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	if u, err := user.Current(); err == nil {
		add(u.Username)
	}
	add(os.Getenv("USER"))
	add(email)
	add("claude-code-user") // legacy account name, explicit
	out = append(out, "")   // unscoped fallback — may return the mcpOAuth-only item
	return out
}

// readOAuthCredential walks the candidate Keychain accounts and returns the
// first credential that actually carries a claudeAiOauth access token. This
// skips the stale mcpOAuth-only item that current Claude Code builds leave
// under the legacy "claude-code-user" account.
func readOAuthCredential(ctx context.Context, email string) (oauthCredential, error) {
	var lastErr error = ErrKeychainNotFound
	for _, acct := range keychainAccountCandidates(email) {
		blob, err := readKeychainCredential(ctx, acct)
		if err != nil {
			lastErr = err
			continue
		}
		cred, err := parseOAuthCredential(blob)
		if err != nil {
			// e.g. a blob with only mcpOAuth and no claudeAiOauth — keep looking.
			lastErr = err
			continue
		}
		return cred, nil
	}
	return oauthCredential{}, lastErr
}
