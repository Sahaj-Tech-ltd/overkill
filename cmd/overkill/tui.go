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

	"github.com/Sahaj-Tech-ltd/overkill/internal/api"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/cost"
	"github.com/Sahaj-Tech-ltd/overkill/internal/db"
	"github.com/Sahaj-Tech-ltd/overkill/internal/learning"
	"github.com/Sahaj-Tech-ltd/overkill/internal/multimodal"
	"github.com/Sahaj-Tech-ltd/overkill/internal/personality"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/speculative"
	syncpkg "github.com/Sahaj-Tech-ltd/overkill/internal/sync"
	termpkg "github.com/Sahaj-Tech-ltd/overkill/internal/term"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	imagegen "github.com/Sahaj-Tech-ltd/overkill/internal/tools/imagegen"
	messaging "github.com/Sahaj-Tech-ltd/overkill/internal/tools/messaging"
	ttspkg "github.com/Sahaj-Tech-ltd/overkill/internal/tools/tts"
	"github.com/Sahaj-Tech-ltd/overkill/internal/walls"

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
func runInkTUI(cmd *cobra.Command, args []string) error {
	// P1: term background probe — detect dark/light mode before launching TUI.
	if dark, err := termProbeBackground(); err == nil {
		if dark {
			os.Setenv("OVERKILL_THEME", "dark")
		} else {
			os.Setenv("OVERKILL_THEME", "light")
		}
	}

	repoRoot, err := findRepoRoot()
	if err != nil {
		return fmt.Errorf("can't find repo root: %w", err)
	}

	loadedCfg := cfg
	if loadedCfg == nil {
		loadedCfg = config.Default()
	}

	// Resolve database connection string.
	connString := loadedCfg.DatabaseURL
	if connString == "" {
		connString = os.Getenv("DATABASE_URL")
	}

	// Open Postgres for session and learning stores via internal/db.
	database, err := db.Open(connString)
	if err != nil {
		return fmt.Errorf("db: %w", err)
	}
	defer database.Close()

	if err := db.Migrate(database); err != nil {
		return fmt.Errorf("db migrate: %w", err)
	}

	// Session store (Postgres).
	sstore := session.NewPostgresStore(database)

	cwd, _ := os.Getwd()
	// Wire sub-agent system: router, registry, manager.
	subagentInfra := setupSubagentSystem(loadedCfg)

	reg := tools.NewDefaultRegistry(tools.FactoryDeps{
		CWD: cwd,
		ExtraTools: []tools.Tool{
			ttspkg.New(loadedCfg.TTS),
			messaging.New(loadedCfg.Gateways),
			imagegen.New(loadedCfg.ImageGen),
		},
		SubagentManager: subagentInfra.Manager,
		// PCA-9: RegressionBank — persisted behavioral regression tests.
		// Falls back to in-memory store if Postgres setup fails.
		RegressionBank: func() *walls.RegressionBank {
			if store, err := walls.NewPostgresRegressionStore(database); err == nil {
				return walls.NewRegressionBank(store, nil)
			}
			return walls.NewRegressionBank(walls.NewMemRegressionStore(), nil)
		}(),
		// PCA-10: MultimodalRegistry — file content extraction (PDF, DOCX, audio, images).
		MultimodalRegistry: multimodal.DefaultRegistry(nil),
	})

	// Learning-from-corrections store (§6.5).
	var learningStore *learning.Store
	if ls, err := learning.NewStore(connString, 1000); err == nil {
		learningStore = ls
		defer ls.Close()
	}

	// Memo the Elephant — thinking indicator phrase engine.
	memoEngine := personality.NewMemoEngine(database)

	// P2: speculative read cache for the TUI path.
	readCache := speculative.NewReadCache(speculative.Options{})

	// Best-effort sync manager — only when the user enabled it in config.
	// Mirrors the CLI path in run.go.
	var syncMgr *syncpkg.Manager
	if loadedCfg != nil && loadedCfg.Sync.AutoPush && loadedCfg.Sync.Backend != "" {
		if be, berr := syncpkg.NewBackend(loadedCfg.Sync); berr == nil && be != nil {
			syncMgr = syncpkg.NewManager(sstore, be)
		}
	}

	// Cost tracker — powers session.usage RPC and the /usage command.
	var costTracker cost.Tracker
	if ct, cerr := cost.NewPostgresTracker(database, loadedCfg.Cost); cerr == nil {
		costTracker = ct
	} else {
		log.Printf("cost tracker init failed: %v (usage tracking disabled)", cerr)
	}

	apiServer := api.NewServer(api.ServerConfig{
		Config:            loadedCfg,
		SessionStore:      sstore,
		Tools:             reg,
		LearningStore:     learningStore,
		FeatureManager:    featureMgr,
		ExtensionsManager: extensionsMgr,
		ReadCache:         readCache,
		MemoEngine:        memoEngine,
		SyncManager:       syncMgr,
		HotReloadBus:      hotReloadBus,
		SubagentManager:   &subagentManagerAdapter{mgr: subagentInfra.Manager},
		CostTracker:       costTracker,
	})

	// Start API server in background.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- apiServer.Start(ctx)
	}()

	// Wait for the server to bind a port using a ready-channel retry loop
	// instead of a fixed sleep which races on slow systems.
	apiReadyCh := make(chan string, 1)
	go func() {
		for i := 0; i < 50; i++ { // 50 * 10ms = 500ms max wait
			addr := apiServer.Addr()
			if addr != "" && addr != "http://localhost:0" {
				apiReadyCh <- addr
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	var apiAddr string
	select {
	case apiAddr = <-apiReadyCh:
	case <-time.After(2 * time.Second):
		return fmt.Errorf("API server failed to bind a port within timeout")
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

// termProbeBackground queries the terminal for its background color.
func termProbeBackground() (bool, error) {
	return termpkg.QueryBackground(200 * time.Millisecond)
}
