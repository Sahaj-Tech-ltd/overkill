package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/deprecated/bubbletea-tui"
	"github.com/Sahaj-Tech-ltd/overkill/internal/acp"
	"github.com/Sahaj-Tech-ltd/overkill/internal/api"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
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

// runTUI is the default command (just running `overkill` with no args).
// Now launches the Ink TUI.
func runTUI(cmd *cobra.Command, args []string) error {
	return runInkTUI(cmd, args)
}

// buildTUIApp was the old Bubble Tea app builder. Returns nil — these features
// (ACP, gateway, Slack, web) need Ink equivalents built later.
func buildTUIApp() *tui.App {
	return nil
}

// acpAgentAdapter satisfies acp.Sender for the deprecated Bubble Tea TUI.
type acpAgentAdapter struct {
	a interface{}
}

func (a *acpAgentAdapter) StreamACP(ctx context.Context, userInput string) (<-chan acp.AgentEvent, error) {
	return nil, fmt.Errorf("ACP not available: Bubble Tea TUI deprecated")
}
func (a *acpAgentAdapter) Model() string     { return "" }
func (a *acpAgentAdapter) SessionID() string { return "" }

// runInkTUI starts the JSON-RPC API server on a random port, then launches
// the Ink TUI frontend. The old Bubble Tea TUI lives in deprecated/bubbletea-tui/.
func runInkTUI(cmd *cobra.Command, args []string) error {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return fmt.Errorf("can't find repo root: %w", err)
	}

	loadedCfg := cfg
	if loadedCfg == nil {
		loadedCfg = config.Default()
	}

	// Use BadgerDB in ~/.overkill/sessions/
	sstore, err := session.NewBadgerStore(filepath.Join(os.Getenv("HOME"), ".overkill", "sessions"))
	if err != nil {
		return fmt.Errorf("session store: %w", err)
	}
	defer sstore.Close()

	reg := tools.NewRegistry()

	apiServer := api.NewServer(api.ServerConfig{
		Config:       loadedCfg,
		SessionStore: sstore,
		Tools:        reg,
	})

	// Start API server in background.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- apiServer.Start(ctx)
	}()

	// Wait for the server to bind a port.
	time.Sleep(300 * time.Millisecond)

	apiAddr := apiServer.Addr()
	if apiAddr == "http://localhost:0" {
		return fmt.Errorf("API server failed to bind a port")
	}
	log.Printf("API ready at %s", apiAddr)

	// Launch the Ink TUI.
	tuiPath := filepath.Join(repoRoot, "tui")
	tuiProc := exec.Command("npm", "start")
	tuiProc.Dir = tuiPath
	tuiProc.Env = append(os.Environ(),
		"OVERKILL_API_URL="+apiAddr,
		"FORCE_COLOR=1",
	)
	tuiProc.Stdin = os.Stdin
	tuiProc.Stdout = os.Stdout
	tuiProc.Stderr = os.Stderr

	if err := tuiProc.Start(); err != nil {
		cancel()
		return fmt.Errorf("launching Ink TUI: %w (did you run 'npm install' in %s?)", err, tuiPath)
	}

	// Wait for TUI or signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	doneCh := make(chan error, 1)
	go func() {
		doneCh <- tuiProc.Wait()
	}()

	select {
	case err := <-doneCh:
		if err != nil {
			log.Printf("TUI exited: %v", err)
		}
	case sig := <-sigCh:
		log.Printf("Received %v, shutting down...", sig)
		tuiProc.Process.Signal(sig)
		<-doneCh
	}

	cancel()
	<-errCh // wait for API server to shut down
	return nil
}

// findRepoRoot walks up from cwd looking for go.mod.
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return os.Getwd()
}
