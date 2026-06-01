package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/m1chael-pappas/pulse/internal/config"
	"github.com/m1chael-pappas/pulse/internal/doctor"
	"github.com/m1chael-pappas/pulse/internal/providers/claudecode"
	"github.com/m1chael-pappas/pulse/internal/ui"
)

const usageHelp = `pulse — terminal dashboard

  pulse              run the TUI (default)
  pulse config       print the config file path
  pulse doctor       check PATH, deps, and config
  pulse usage        fetch /api/oauth/usage once and print raw JSON
  pulse --help       show this help
`

func main() {
	res, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pulse: %v\n", err)
		os.Exit(1)
	}

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "config":
			fmt.Println(res.Path)
			return
		case "doctor":
			os.Exit(doctor.Run())
		case "usage":
			if err := runUsageProbe(); err != nil {
				fmt.Fprintf(os.Stderr, "pulse usage: %v\n", err)
				os.Exit(1)
			}
			return
		case "-h", "--help", "help":
			fmt.Print(usageHelp)
			return
		}
	}

	if res.FirstRun {
		printFirstRunBanner(res.Path)
	}

	p := tea.NewProgram(ui.NewApp(res.Config), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "pulse: %v\n", err)
		os.Exit(1)
	}
}

// printFirstRunBanner runs once: right after the loader auto-creates the
// config. The banner stays on the user's scrollback after they quit so
// they can copy the setup commands later.
func printFirstRunBanner(cfgPath string) {
	fmt.Println("Welcome to pulse 👋")
	fmt.Println()
	fmt.Println("First run — a default config was written to:")
	fmt.Println("  " + cfgPath)
	fmt.Println()
	fmt.Println("If you installed pulse via `go install`, you may need to add Go's")
	fmt.Println("bin directory to your PATH so `pulse` works from anywhere:")
	fmt.Println(`  echo 'export PATH="$HOME/go/bin:$PATH"' >> ~/.zshrc && source ~/.zshrc`)
	fmt.Println()
	fmt.Println("Optional CLI deps for richer tiles:")
	fmt.Println("  brew install gh ical-buddy")
	fmt.Println()
	fmt.Println("Run `pulse doctor` any time to verify setup.")
	fmt.Println("Press Enter to launch the TUI…")
	bufio := []byte{0}
	_, _ = os.Stdin.Read(bufio)
}

// runUsageProbe is a one-shot debug helper: hits /api/oauth/usage with the
// Keychain credential, prints the raw JSON. Useful for confirming whether
// the endpoint works for a given plan tier (Max vs Enterprise) and for
// diagnosing 429s without sitting through the TUI's backoff window.
func runUsageProbe() error {
	ctx := context.Background()
	body, err := claudecode.FetchUsageRaw(ctx)
	if err != nil {
		return err
	}
	// Pretty-print if it parses as JSON; otherwise dump as-is.
	var pretty any
	if json.Unmarshal(body, &pretty) == nil {
		out, _ := json.MarshalIndent(pretty, "", "  ")
		fmt.Println(string(out))
		return nil
	}
	fmt.Println(string(body))
	return nil
}
