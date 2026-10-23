package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/api"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	termpkg "github.com/Sahaj-Tech-ltd/overkill/internal/term"

	"github.com/spf13/cobra"
)

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Launch the Ink terminal UI",
	RunE:  runInkTUI,
}

func init() {
	rootCmd.AddCommand(tuiCmd)
}

// runInkTUI starts the JSON-RPC API server on a random port, then launches
// the Ink TUI frontend.
func runInkTUI(cmd *cobra.Command, args []string) (resultErr error) {
	// P1: term background probe — detect dark/light mode before launching TUI.
	if dark, err := termProbeBackground(); err == nil {
		if dark {
			os.Setenv("OVERKILL_THEME", "dark")
		} else {
			os.Setenv("OVERKILL_THEME", "light")
		}
	}

	tuiProc, err := frontendCommand()
	if err != nil {
		return err
	}

	loadedCfg := cfg
	if loadedCfg == nil {
		loadedCfg = config.Default()
	}

	signalCtx, stopSignals := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	databaseOwner, err := openCommandDatabase(signalCtx, cmd, loadedCfg, true)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, databaseOwner.Close()) }()
	database := databaseOwner.DB

	// Session store (Postgres).
	sstore, err := session.NewPostgresStore(database)
	if err != nil {
		return fmt.Errorf("session store: %w", err)
	}

	homeDir, err := config.ConfigDir()
	if err != nil {
		return err
	}
	services, err := newRuntimeServices(loadedCfg, database, homeDir)
	if err != nil {
		return err
	}
	apiServer := api.NewServer(services.APIConfig(sstore))
	defer apiServer.Close()

	return launchFrontend(databaseOwner.Context, apiServer, tuiProc)
}

func termProbeBackground() (bool, error) {
	return termpkg.QueryBackground(200 * time.Millisecond)
}
