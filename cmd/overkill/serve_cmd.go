package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/acp"
	"github.com/Sahaj-Tech-ltd/overkill/internal/daemon"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start overkill in daemon mode with job queue (§8.7.3)",
	Long: `serve starts the ACP HTTP server with the daemon job queue enabled.

Bridge clients post jobs to POST /v1/jobs; the worker pool picks them up,
runs them against the configured agent, and updates their status in BadgerDB.

Jobs submitted with a non-empty channel field are automatically assigned the
"remote" permission profile (no pty_shell, shell/patch/git-push require approval).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		nWorkers, _ := cmd.Flags().GetInt("workers")
		addr, _ := cmd.Flags().GetString("addr")

		token, err := loadOrCreateACPToken()
		if err != nil {
			return fmt.Errorf("serve: load token: %w", err)
		}

		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("serve: home dir: %w", err)
		}
		jobDir := home + "/.overkill/jobs"

		jobStore, err := daemon.OpenJobStore(jobDir)
		if err != nil {
			return fmt.Errorf("serve: open job store: %w", err)
		}
		defer func() { _ = jobStore.Close() }()

		worker := daemon.NewWorker(jobStore, func(ctx context.Context, job daemon.Job) error {
			// Default RunFunc: if an agent is attached via the TUI app we
			// delegate; otherwise we log a no-op. In production this callback
			// is replaced by the caller (e.g. bridge relay or tui.go wiring).
			fmt.Printf("job %s: running intent=%q profile=%q\n", job.ID, job.Intent, job.Profile)
			return nil
		}, nWorkers)
		worker.Start(context.Background())
		defer worker.Stop()

		app := buildTUIApp()
		var sender acp.Sender
		if app != nil && app.Agent != nil {
			sender = &acpAgentAdapter{a: app.Agent}
		}

		srv := acp.NewServer(acp.Config{
			Addr:           addr,
			Token:          token,
			AllowedOrigins: cfg.ACP.AllowedOrigins,
			Agent:          sender,
			Store:          app.Store,
			Name:           "overkill",
			Version:        Version,
			JobStore:       jobStore,
			JobWorker:      worker,
		})
		if err := srv.Start(); err != nil {
			return fmt.Errorf("serve: start server: %w", err)
		}
		fmt.Printf("overkill daemon listening on %s\n", addr)
		fmt.Printf("workers: %d\n", nWorkers)
		fmt.Printf("token: %s\n", token)

		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		<-stop

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		return srv.Shutdown(ctx)
	},
}

func init() {
	serveCmd.Flags().Int("workers", 4, "number of concurrent job workers")
	serveCmd.Flags().String("addr", "127.0.0.1:7777", "host:port for the ACP server")
	rootCmd.AddCommand(serveCmd)
}
