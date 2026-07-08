// Command aicost is the aiCostTracker CLI entrypoint.
//
// main is intentionally thin: it wires build metadata, sets up signal-based
// cancellation, and hands off to internal/cli. All logic lives in internal/*
// so nothing here is importable as a library (cf. gitl's cmd/ + internal/ rule).
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/akomyagin/aiCostTracker/internal/cli"
)

// Build metadata, injected via -ldflags "-X main.version=… -X main.commit=… -X main.date=…".
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	// Ctrl-C / SIGTERM cancels in-flight provider HTTP calls (propagated via ctx
	// through Fetch).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(cli.Execute(ctx, os.Args[1:], os.Stdout, os.Stderr, version, commit, date))
}
