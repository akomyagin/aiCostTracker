// Package cli assembles aiCostTracker's command surface with spf13/cobra.
//
// Commands:
//   - report  — fetch each enabled provider's usage for a period and print a table
//   - history — show stored trends over time from the local SQLite snapshots
//   - version — print build metadata
//
// main is thin; all wiring lives here. Dependencies (config load, store opener,
// provider factory) are injected via App so the command tree is unit-testable
// with fakes and no network/disk.
package cli

import (
	"context"
	"io"
	"time"

	"github.com/akomyagin/aiCostTracker/internal/config"
	"github.com/akomyagin/aiCostTracker/internal/provider"
	"github.com/akomyagin/aiCostTracker/internal/storage"

	"github.com/spf13/cobra"
)

// App holds the injectable dependencies of the CLI. Production wiring uses the
// real config loader, SQLite opener and provider factory; tests substitute fakes.
type App struct {
	// LoadConfig loads and validates configuration.
	LoadConfig func() (config.Config, error)
	// OpenStore opens the history store at the given path.
	OpenStore func(path string) (storage.Store, error)
	// NewProvider builds an adapter for the given provider id from config.
	NewProvider func(id string, cfg config.Config) (provider.ProviderUsageSource, error)
	// Now returns the current time (injectable for deterministic period math).
	Now func() time.Time

	Out    io.Writer
	ErrOut io.Writer

	Version, Commit, Date string
}

// DefaultApp returns an App wired to the real config/storage/provider stack.
func DefaultApp(out, errOut io.Writer, version, commit, date string) *App {
	return &App{
		LoadConfig:  config.Load,
		OpenStore:   storage.Open,
		NewProvider: newProvider,
		Now:         time.Now,
		Out:         out,
		ErrOut:      errOut,
		Version:     version,
		Commit:      commit,
		Date:        date,
	}
}

// newProvider is the production provider factory: it maps a provider id to its
// adapter, passing the admin key, base URL and retry budget from config.
func newProvider(id string, cfg config.Config) (provider.ProviderUsageSource, error) {
	pc := cfg.Providers[id]
	opts := provider.Options{
		AdminKey:   pc.AdminKey,
		BaseURL:    pc.BaseURL,
		MaxRetries: cfg.MaxRetries,
	}
	switch id {
	case "anthropic":
		return provider.NewAnthropic(opts), nil
	case "openai":
		return provider.NewOpenAI(opts), nil
	case "openrouter":
		return provider.NewOpenRouter(opts), nil
	default:
		return nil, &unknownProviderError{id: id}
	}
}

type unknownProviderError struct{ id string }

func (e *unknownProviderError) Error() string { return "unknown provider: " + e.id }

// Root builds the cobra command tree for the app.
func (a *App) Root() *cobra.Command {
	root := &cobra.Command{
		Use:   "aicost",
		Short: "Aggregate AI provider usage & cost into one local dashboard",
		Long: "aicost pulls usage & cost from each AI provider's official usage API and\n" +
			"prints one aggregated table — one glance instead of visiting 3-5 billing\n" +
			"consoles. Snapshots are stored locally in SQLite so you can review trends.\n" +
			"Pure CLI: no server, no telemetry, your spend data never leaves the machine.\n" +
			"\n" +
			adminKeyHelp,
		Example: "  # Report the last 7 days across all enabled providers\n" +
			"  export AICOST_ANTHROPIC_ADMIN_KEY=sk-ant-admin-...\n" +
			"  export AICOST_OPENAI_ADMIN_KEY=sk-admin-...\n" +
			"  aicost report --period=7d\n" +
			"\n" +
			"  # Show stored history without hitting the network\n" +
			"  aicost history --period=month",
		SilenceUsage:  true, // don't dump usage on runtime errors
		SilenceErrors: true, // we print errors ourselves in Execute
	}
	root.SetOut(a.Out)
	root.SetErr(a.ErrOut)

	root.AddCommand(a.reportCmd())
	root.AddCommand(a.historyCmd())
	root.AddCommand(a.versionCmd())
	return root
}

// Execute runs the CLI with the given args (excluding the program name) and
// returns a process exit code.
func Execute(ctx context.Context, args []string, out, errOut io.Writer, version, commit, date string) int {
	app := DefaultApp(out, errOut, version, commit, date)
	root := app.Root()
	root.SetArgs(args)
	if err := root.ExecuteContext(ctx); err != nil {
		// cobra returns flag/parse errors here too; print to stderr.
		if _, werr := io.WriteString(errOut, "error: "+err.Error()+"\n"); werr != nil {
			return 1
		}
		return 1
	}
	return 0
}
