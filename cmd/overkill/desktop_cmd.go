package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/api"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/db"
	"github.com/Sahaj-Tech-ltd/overkill/internal/localdb"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/spf13/cobra"
)

type desktopOptions struct{ PostgresBin, DataDir string }
type desktopReady struct {
	Type     string `json:"type"`
	Protocol int    `json:"protocol"`
	Address  string `json:"address"`
	Token    string `json:"token"`
}

func newDesktopCommand() *cobra.Command {
	options := desktopOptions{}
	command := &cobra.Command{Use: "desktop-server", Hidden: true, Args: cobra.NoArgs, SilenceUsage: true, RunE: func(cmd *cobra.Command, _ []string) error {
		runCtx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		input, ok := cmd.InOrStdin().(io.ReadCloser)
		if !ok {
			input = io.NopCloser(cmd.InOrStdin())
		}
		return runDesktopServer(runCtx, cfg, options, input, cmd.OutOrStdout())
	}}
	command.Flags().StringVar(&options.PostgresBin, "postgres-bin", "", "absolute bundled native PostgreSQL binary directory")
	command.Flags().StringVar(&options.DataDir, "data-dir", "", "absolute private desktop PostgreSQL cluster directory")
	return command
}

func init() { rootCmd.AddCommand(newDesktopCommand()) }

// ownDesktopInput owns the child stdin for this process session. EOF means the
// desktop parent has exited; application cancellation also unblocks its reader.
func ownDesktopInput(ctx context.Context, input io.ReadCloser, cancel context.CancelFunc) (func(), error) {
	input, err := cancellableDesktopInput(input)
	if err != nil {
		return nil, err
	}
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = input.Close() })
	go func() { defer close(done); _, _ = io.Copy(io.Discard, input); cancel() }()
	return func() { stop(); _ = input.Close(); <-done }, nil
}

func runDesktopServer(ctx context.Context, loaded *config.Config, options desktopOptions, input io.ReadCloser, output io.Writer) (resultErr error) {
	// PersistentPreRunE has already resolved this into the in-memory provider
	// config. Do not let tools, language servers, MCP servers or PostgreSQL
	// inherit the credential through their subprocess environments.
	if err := os.Unsetenv("OVERKILL_DESKTOP_PROVIDER_KEY"); err != nil {
		return fmt.Errorf("desktop-server: could not isolate provider credentials")
	}
	if !filepath.IsAbs(options.PostgresBin) || !filepath.IsAbs(options.DataDir) {
		return fmt.Errorf("desktop-server: --postgres-bin and --data-dir require absolute paths")
	}
	if loaded == nil {
		loaded = config.Default()
	}
	// Use the canonical root config/skill initialization, with desktop-only
	// transport policy. Desktop launches no gateway bots, bridge, or auto-push.
	desktopCfg := *loaded
	desktopCfg.Gateways = config.GatewayConfig{}
	desktopCfg.Sync.AutoPush = false
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopInput, err := ownDesktopInput(runCtx, input, cancel)
	if err != nil {
		return fmt.Errorf("desktop-server: parent lifetime pipe: %w", err)
	}
	defer stopInput()
	manager, err := localdb.NewManager(localdb.Config{BinDir: options.PostgresBin, DataDir: options.DataDir, StartupTimeout: 30 * time.Second, ShutdownTimeout: 10 * time.Second})
	if err != nil {
		return fmt.Errorf("desktop-server: database setup: %w", err)
	}
	// Database lifetime extends past API/agent drain and pool closure. Only
	// startup shares cancellation, before any application resources exist.
	databaseCtx, stopDatabase := context.WithCancel(context.Background())
	defer stopDatabase()
	cancelStartup := context.AfterFunc(runCtx, stopDatabase)
	info, err := manager.Start(databaseCtx)
	cancelStartup()
	if err != nil {
		return fmt.Errorf("desktop-server: database startup: %w", err)
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		resultErr = errors.Join(resultErr, manager.Stop(shutdown))
	}()
	if err := runCtx.Err(); err != nil {
		return nil
	}
	databaseFailure := make(chan error, 1)
	go func() {
		if err := manager.Wait(runCtx); err != nil && runCtx.Err() == nil {
			databaseFailure <- err
			cancel()
		}
	}()
	database, err := db.OpenContext(runCtx, info.DSN())
	if err != nil {
		return fmt.Errorf("desktop-server: database connection failed")
	}
	defer func() { resultErr = errors.Join(resultErr, database.Close()) }()
	if err := db.Migrate(database); err != nil {
		return fmt.Errorf("desktop-server: database migration: %w", err)
	}
	store, err := session.NewPostgresStore(database)
	if err != nil {
		return fmt.Errorf("desktop-server: session store: %w", err)
	}
	home, err := config.ConfigDir()
	if err != nil {
		return err
	}
	services, err := newRuntimeServices(&desktopCfg, database, home)
	if err != nil {
		return fmt.Errorf("desktop-server: runtime initialization: %w", err)
	}
	services.Alarm = nil
	serverConfig := services.APIConfig(store)
	serverConfig.ListenAddr = "127.0.0.1:0"
	serverConfig.SyncManager = nil
	srv := api.NewServer(serverConfig)
	defer srv.Close()
	err = serveDesktopAPI(runCtx, cancel, srv, output)
	select {
	case failure := <-databaseFailure:
		return errors.Join(err, failure)
	default:
		return err
	}
}

// serveDesktopAPI emits exactly one line after the authenticated listener is
// live. Credentials travel only over the inherited parent stdout pipe.
func serveDesktopAPI(ctx context.Context, cancel context.CancelFunc, srv *api.Server, output io.Writer) error {
	done := make(chan error, 1)
	go func() { done <- srv.Start(ctx) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		cancel()
		return <-done
	case <-srv.Ready():
	}
	if ctx.Err() != nil {
		cancel()
		return <-done
	}
	if err := json.NewEncoder(output).Encode(desktopReady{Type: "ready", Protocol: 1, Address: srv.Addr(), Token: srv.AuthToken()}); err != nil {
		cancel()
		<-done
		return fmt.Errorf("desktop-server: parent readiness pipe failed")
	}
	return <-done
}
