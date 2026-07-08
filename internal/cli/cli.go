// Package cli assembles aiCostTracker's command surface.
//
// The Этап-0 skeleton uses only the standard library (flag) so `go build ./...`
// is green with zero external modules. Этап 1 replaces this with spf13/cobra +
// viper (matching the gitl portfolio convention) once dependencies are vendored;
// the command contracts below stay the same.
//
// Commands (contract, implemented over Этап 1 / Фаза 2):
//   - report  — fetch each enabled provider's usage for a period and print a table
//   - history — show stored trends over time from the local SQLite snapshots
//   - version — print build metadata
package cli

import (
	"flag"
	"fmt"
	"io"
)

// Execute runs the CLI with the given args (excluding the program name) and
// writes to out/errOut. version/commit/date are injected from main via ldflags.
//
// Full command wiring — Этап 1. For now it prints usage and the version so the
// binary is runnable from Этап 0.
func Execute(args []string, out, errOut io.Writer, version, commit, date string) int {
	fs := flag.NewFlagSet("aicost", flag.ContinueOnError)
	fs.SetOutput(errOut)
	showVersion := fs.Bool("version", false, "print version and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *showVersion {
		fmt.Fprintf(out, "aicost %s (commit %s, built %s)\n", version, commit, date)
		return 0
	}

	fmt.Fprintln(out, "aicost — aggregate AI provider usage & cost into one local dashboard")
	fmt.Fprintln(out, "commands (Этап 1+): report | history | version")
	fmt.Fprintln(out, "run 'aicost --version' for build info")
	return 0
}
