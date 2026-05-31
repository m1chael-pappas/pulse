package claudecode

import (
	"context"
	"errors"
	"os/exec"
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
