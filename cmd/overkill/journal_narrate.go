package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/journal"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/pkg/tui"
)

// writeJournalNarrative is the TUI session-end hook for §4.19. All
// errors are logged but never surfaced — the user is on their way out,
// they don't need to see a model timeout right then. Bounded 60s so
// a stuck model doesn't wedge exit.
func writeJournalNarrative(app *tui.App) {
	if app == nil || app.Journal == nil || app.Agent == nil {
		return
	}
	sid := app.Agent.SessionID()
	if sid == "" {
		return
	}
	provider, modelName := buildNarrateProvider()
	if provider == nil {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	jdir := filepath.Join(home, ".overkill", "journal")
	summ := journal.NewSummarizer(app.Journal, provider, modelName)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	path, _, err := summ.NarrateSession(ctx, jdir, sid)
	if err != nil {
		log.Printf("journal narrate: %v", err)
		return
	}
	if path != "" {
		log.Printf("journal narrate: wrote %s", path)
	}
}

// buildNarrateProvider resolves a provider + model from the active
// config. Returns nil when no provider is configured — narrator
// degrades to no-op rather than blocking exit. Mirrors run.go's
// provider-resolve path so the diary uses the same model the user
// has been talking to.
func buildNarrateProvider() (providers.Provider, string) {
	pc, model := resolveProvider()
	if pc == nil {
		return nil, ""
	}
	apiKey := pc.APIKey
	if apiKey == "" {
		apiKey = os.Getenv(providerEnvVar(pc.Name))
	}
	p, err := providers.NewProvider(providers.FactoryConfig{
		Name:    pc.Name,
		Type:    pc.Type,
		APIKey:  apiKey,
		BaseURL: pc.BaseURL,
		Headers: pc.Headers,
	})
	if err != nil {
		return nil, ""
	}
	return p, model
}

// overkillJournalCmd surfaces `overkill journal narrate` so users can
// regenerate a day's diary on demand (cron-driven post-mortem, manual
// catch-up after a crash).
var overkillJournalCmd = &cobra.Command{
	Use:   "journal",
	Short: "Manage the flight-recorder journal",
}

var journalNarrateCmd = &cobra.Command{
	Use:   "narrate <session-id>",
	Short: "Render a structured diary entry for a session",
	Long: `Reads the flight-recorder entries for <session-id>, calls the
configured model with the §4.19 diary system prompt, and writes the
result to ~/.overkill/journal/entries/<YYYY-MM-DD>.md. Idempotent —
multiple sessions on the same day append under "## session <id>"
sub-sections rather than overwriting.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sid := args[0]
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		jdir := filepath.Join(home, ".overkill", "journal")
		rec := journal.NewFlightRecorder(jdir, sid)

		provider, modelName := buildNarrateProvider()
		if provider == nil {
			return fmt.Errorf("no provider configured — run `overkill setup` first")
		}
		summ := journal.NewSummarizer(rec, provider, modelName)

		ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
		defer cancel()
		path, narrative, err := summ.NarrateSession(ctx, jdir, sid)
		if err != nil {
			return err
		}
		if path == "" {
			fmt.Printf("%sno journal entries for session %q%s\n", colorDim, sid, colorReset)
			return nil
		}
		fmt.Printf("%s✓ wrote %s%s\n\n%s\n", colorGreen, path, colorReset, narrative)
		return nil
	},
}

func init() {
	overkillJournalCmd.AddCommand(journalNarrateCmd)
	rootCmd.AddCommand(overkillJournalCmd)
}
