// Package doctor runs a startup health check: confirms PATH is set up,
// optional CLI deps are present, and config is loadable. Output is a
// human-readable checklist so new users can self-serve setup problems.
package doctor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/m1chael-pappas/pulse/internal/providers/maccal"
)

type result struct {
	name   string
	status status
	hint   string
}

type status int

const (
	statusOK status = iota
	statusWarn
	statusFail
)

func (s status) icon() string {
	switch s {
	case statusOK:
		return "✓"
	case statusWarn:
		return "!"
	default:
		return "✗"
	}
}

// Run prints a checklist and returns exit code (0 = all checks passed,
// 1 = at least one failed; warnings don't fail the run).
func Run() int {
	results := []result{
		checkPATH(),
		checkGh(),
		checkIcalBuddy(),
		checkConfig(),
	}

	exit := 0
	fmt.Println("pulse doctor")
	fmt.Println(strings.Repeat("─", 40))
	for _, r := range results {
		fmt.Printf("  %s  %s\n", r.status.icon(), r.name)
		if r.hint != "" {
			for _, line := range strings.Split(r.hint, "\n") {
				fmt.Printf("       %s\n", line)
			}
		}
		if r.status == statusFail {
			exit = 1
		}
	}
	fmt.Println()
	if exit == 0 {
		fmt.Println("All checks passed.")
	} else {
		fmt.Println("Some checks failed — follow the hints above.")
	}
	return exit
}

// checkPATH confirms the directory holding the pulse binary is on $PATH.
// Without this, users can't type `pulse` from any directory — only
// /full/path/to/pulse works, and the binary feels broken.
func checkPATH() result {
	exe, err := os.Executable()
	if err != nil {
		return result{name: "binary on PATH", status: statusWarn, hint: "couldn't locate binary: " + err.Error()}
	}
	exe, _ = filepath.EvalSymlinks(exe)
	dir := filepath.Dir(exe)

	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if abs, err := filepath.EvalSymlinks(p); err == nil && abs == dir {
			return result{name: "binary on PATH (" + dir + ")", status: statusOK}
		}
	}

	hint := fmt.Sprintf("`pulse` is at %s but that dir is not on $PATH.\n", dir)
	hint += "Add it once:\n"
	hint += "  echo 'export PATH=\"" + dir + ":$PATH\"' >> ~/.zshrc\n"
	hint += "  source ~/.zshrc"
	return result{name: "binary on PATH", status: statusFail, hint: hint}
}

func checkGh() result {
	r := result{name: "gh CLI (for GitHub tile)"}
	path, err := exec.LookPath("gh")
	if err != nil {
		r.status = statusWarn
		r.hint = "not installed — GitHub tile will be inert.\nInstall: brew install gh && gh auth login"
		return r
	}
	out, err := exec.Command(path, "auth", "status").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "Logged in") {
		r.status = statusWarn
		r.hint = "installed but not signed in.\nRun: gh auth login"
		return r
	}
	r.status = statusOK
	r.hint = path
	return r
}

func checkIcalBuddy() result {
	r := result{name: "icalBuddy (for Calendar tile)"}
	if runtime.GOOS != "darwin" {
		r.status = statusWarn
		r.hint = "macOS-only; skipping."
		return r
	}
	for _, name := range []string{"icalBuddy", "icalbuddy"} {
		if path, err := exec.LookPath(name); err == nil {
			r.status = statusOK
			r.hint = path
			// Listing the names removes the guesswork from
			// [maccal].calendars — the value must be a calendar name, which
			// is often not the account it syncs from.
			if cals := maccal.VisibleCalendars(context.Background()); len(cals) > 0 {
				r.hint += "\nVisible calendars: " + strings.Join(cals, ", ") +
					"\nUse these exact names in [maccal].calendars ([] = all)."
			}
			return r
		}
	}
	r.status = statusWarn
	r.hint = "not installed — Calendar tile will be inert.\nInstall: brew install ical-buddy"
	return r
}

func checkConfig() result {
	r := result{name: "config file"}
	dir, err := os.UserConfigDir()
	if err != nil {
		r.status = statusFail
		r.hint = "no user config dir: " + err.Error()
		return r
	}
	path := filepath.Join(dir, "pulse", "config.toml")
	info, err := os.Stat(path)
	if err != nil {
		r.status = statusWarn
		r.hint = "no config yet — will be auto-created on first `pulse` run.\nLocation: " + path
		return r
	}
	r.status = statusOK
	r.hint = fmt.Sprintf("%s (%d bytes)", path, info.Size())
	return r
}
