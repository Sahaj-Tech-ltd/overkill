package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/db"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tokenizer"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/Sahaj-Tech-ltd/overkill/internal/walls"
	"github.com/Sahaj-Tech-ltd/overkill/internal/web"
	"strings"
)

var (
	webListen string
	webToken  string
	webNoAuth bool
	webOpen   bool
)

var webCmd = &cobra.Command{
	Use:   "web",
	Short: "Launch the Overkill browser UI",
	Long: `Serve the Overkill browser UI over HTTP. Same agent backend as the TUI;
designed for low-overhead access from a phone or another laptop on the LAN.

Default bind is 127.0.0.1:8420. Use --listen 0.0.0.0:8420 to expose to LAN —
in that case make absolutely sure you keep the bearer token private.`,
	RunE: runWeb,
}

func runWeb(cmd *cobra.Command, args []string) error {
	loadedCfg := cfg
	if loadedCfg == nil {
		loadedCfg = config.Default()
	}

	// Resolve database connection string.
	connString := loadedCfg.DatabaseURL
	if connString == "" {
		connString = os.Getenv("DATABASE_URL")
	}
	if connString == "" {
		return fmt.Errorf("DATABASE_URL required for Postgres backend — set it in ~/.overkill/config.toml or the environment")
	}

	// Open Postgres and run migrations via internal/db.
	database, err := db.Open(connString)
	if err != nil {
		return fmt.Errorf("db open: %w", err)
	}
	defer database.Close()

	if err := db.Migrate(database); err != nil {
		return fmt.Errorf("db migrate: %w", err)
	}

	// Session store (Postgres).
	sstore := session.NewPostgresStore(database)

	// Resolve provider / model from config (same logic as run.go).
	providerCfg, modelName := resolveProvider()
	if providerCfg == nil {
		return fmt.Errorf("no provider configured — run 'overkill config init' first")
	}

	apiKey := providerCfg.APIKey
	if apiKey == "" {
		apiKey = os.Getenv(providerEnvVar(providerCfg.Name))
	}

	provider, err := providers.NewProvider(providers.FactoryConfig{
		Name:    providerCfg.Name,
		Type:    providerCfg.Type,
		APIKey:  apiKey,
		BaseURL: providerCfg.BaseURL,
		Headers: providerCfg.Headers,
	})
	if err != nil {
		return fmt.Errorf("creating provider: %w", err)
	}

	// Build tool registry from the shared factory.
	// Core tools (shell, fs, git, grep, web, patch, pty, etc.) are always
	// registered; infra tools (browser, LSP, memory, etc.) are skipped because
	// their deps are nil in the web context.
	cwd, _ := os.Getwd()
	toolReg := tools.NewDefaultRegistry(tools.FactoryDeps{
		CWD: cwd,
		OuroborosWall: func() *walls.OuroborosWall {
			if loadedCfg.Ouroboros.Enabled {
				ouroCfg := loadedCfg.Ouroboros
				ouroKey := ouroCfg.APIKey
				if ouroKey == "" {
					ouroKey = os.Getenv(strings.ToUpper(ouroCfg.Provider) + "_API_KEY")
				}
				if ouroKey != "" && ouroCfg.Provider != "" {
					ouroProv, err := providers.NewProvider(providers.FactoryConfig{
						Name:    ouroCfg.Provider,
						Type:    ouroCfg.Provider,
						APIKey:  ouroKey,
						BaseURL: ouroCfg.BaseURL,
					})
					if err == nil {
						return walls.NewOuroborosWall(walls.OuroborosConfig{
							Enabled:      true,
							Provider:     ouroProv,
							Model:        ouroCfg.Model,
							StrictMode:   ouroCfg.StrictMode,
							SystemPrompt: ouroCfg.SystemPrompt,
						})
					}
				}
			}
			return walls.NewOuroborosWall(walls.OuroborosConfig{})
		}(),
	})

	// Build the agent.
	a := agent.New(agent.Config{
		Provider:     provider,
		Tools:        toolReg,
		Tokenizer:    tokenizer.NewEstimator(),
		Model:        modelName,
		MaxTokens:    200000,
		SystemPrompt: buildSystemPrompt(loadedCfg),
		MaxSteps:     30,
		Scanners: []security.Scanner{
			security.NewCommandScanner(
				security.WithProjectPath(cwd),
				security.WithExtraDenyPatterns(loadedCfg.Security.DenyPatterns),
				security.WithForbiddenPaths(loadedCfg.Security.ForbiddenPaths),
				security.WithMaxCommandLen(loadedCfg.Security.MaxCommandLen),
			),
			security.NewInjectionScanner(),
		},
	})

	// Fetch the models.dev catalog for the model picker.
	catalog, _ := providers.FetchCatalog(cmd.Context())

	// Resolve bearer token.
	tok := webToken
	if tok == "" {
		tok = os.Getenv("OVERKILL_WEB_TOKEN")
	}
	if tok == "" {
		// Read from ~/.overkill/web-token if it exists.
		if homeDir, herr := config.ConfigDir(); herr == nil {
			if b, rerr := os.ReadFile(homeDir + "/web-token"); rerr == nil && len(b) > 0 {
				tok = string(b)
				if tok[len(tok)-1] == '\n' {
					tok = tok[:len(tok)-1]
				}
			}
		}
	}

	// Build and start the web server.
	srv := web.NewServer(web.Config{
		Addr:         webListen,
		Token:        tok,
		NoAuth:       webNoAuth,
		Agent:        a,
		Store:        sstore,
		Catalog:      catalog,
		PairingStore: security.NewPairingStore(pairingDir()),
		Provider:     providerCfg.Name,
		Version:      Version,
	})

	if err := srv.Start(); err != nil {
		return fmt.Errorf("web server: %w", err)
	}

	log.Printf("Overkill web UI listening on http://%s", srv.Addr())

	// Auto-open the browser if requested.
	if webOpen {
		openURL("http://" + srv.Addr())
	}

	// Wait for SIGINT/SIGTERM.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	log.Printf("Received %v, shutting down...", sig)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

func openURL(rawURL string) {
	// Best-effort browser open — don't fail the command if it doesn't work.
	_ = exec.Command("xdg-open", rawURL).Start()
}

func init() {
	webCmd.Flags().StringVar(&webListen, "listen", "127.0.0.1:8420", "bind address")
	webCmd.Flags().StringVar(&webToken, "token", "", "bearer token (default: ~/.overkill/web-token)")
	webCmd.Flags().BoolVar(&webNoAuth, "no-auth", false, "disable bearer auth (localhost only)")
	webCmd.Flags().BoolVar(&webOpen, "open", false, "open the URL in the default browser")
	rootCmd.AddCommand(webCmd)
}
