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

	"github.com/akomyagin/aiCostTracker/internal/cli"
)

// Build metadata, injected via -ldflags "-X main.version=… -X main.commit=… -X main.date=…".
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	// Ctrl-C / SIGTERM cancels in-flight provider HTTP calls once wired in Этап 1.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	_ = ctx // consumed by commands starting in Этап 1

	os.Exit(cli.Execute(os.Args[1:], os.Stdout, os.Stderr, version, commit, date))
}
