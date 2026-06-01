package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway"
	bridgegw "github.com/Sahaj-Tech-ltd/overkill/internal/gateway/bridge"
	"github.com/Sahaj-Tech-ltd/overkill/internal/hooks"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tokenizer"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/Sahaj-Tech-ltd/overkill/internal/walls"
	"strings"
)

var bridgeCmd = &cobra.Command{
	Use:   "bridge",
	Short: "Run the Overkill HTTP bridge for SMS/iMessage sidecars",
	Long: `Start the HTTP bridge gateway that any chat sidecar can plug into.

Sidecars (Twilio SMS, BlueBubbles iMessage, Baileys WhatsApp, etc.) POST
inbound messages to /v1/in and SSE-read streamed replies from /v1/out.
The bridge runs on loopback by default — expose behind a reverse proxy
if you need remote sidecars to connect.

Configure in [gateways.bridge]:
  enabled = true
  listen = "127.0.0.1:7799"
  token = "shared-secret"

Sidecar sub-configs are reference-only (Overkill doesn't call Twilio/BlueBubbles):
  [gateways.bridge.twilio]
  enabled = true
  account_sid = "..."
  auth_token = "..."
  from_number = "+15551234567"

  [gateways.bridge.imessage]
  enabled = true
  server_url = "http://10.0.1.5:1234"
  password = "..."`,
	RunE: runBridge,
}

func runBridge(cmd *cobra.Command, args []string) error {
	logger := log.New(os.Stderr, "[bridge] ", log.LstdFlags)

	// Resolve provider.
	providerCfg, modelName := resolveProvider()
	if providerCfg == nil {
		providerCfg = detectProviderFromEnv()
	}
	if providerCfg == nil {
		return fmt.Errorf("no provider configured; set an API key env var or configure providers in config.toml")
	}

	apiKey := providerCfg.APIKey
	if apiKey == "" {
		apiKey = os.Getenv(providerEnvVar(providerCfg.Name))
	}

	prov, err := providers.NewProvider(providers.FactoryConfig{
		Name:    providerCfg.Name,
		Type:    providerCfg.Type,
		APIKey:  apiKey,
		BaseURL: providerCfg.BaseURL,
		Headers: providerCfg.Headers,
	})
	if err != nil {
		return fmt.Errorf("creating provider: %w", err)
	}

	// Tool registry.
	cwd, _ := os.Getwd()

	// Load config (needed for Ouroboros, gateways, system prompt).
	cfg, err := config.Load("")
	if err != nil {
		logger.Printf("warning: config load: %v (continuing with defaults)", err)
		cfg = &config.Config{}
	}

	toolReg := tools.NewDefaultRegistry(tools.FactoryDeps{
		CWD: cwd,
		OuroborosWall: func() *walls.OuroborosWall {
			if cfg.Ouroboros.Enabled {
				ouroCfg := cfg.Ouroboros
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

	logger.Printf("provider: %s, model: %s", providerCfg.Name, modelName)

	// Build agent.
	ag := agent.New(agent.Config{
		Provider:    prov,
		Tools:       toolReg,
		Compressors: tools.NewCompressorRegistry(),
		Hooks:       hooks.NewRegistry(),
		Scanners: []security.Scanner{
			security.NewCommandScanner(
				security.WithProjectPath(cwd),
			),
			security.NewInjectionScanner(),
		},
		Tokenizer:    tokenizer.NewEstimator(),
		Steering:     agent.NewSteeringQueue(agent.SteeringDrainAll),
		Model:        modelName,
		MaxTokens:    200000,
		SystemPrompt: buildSystemPrompt(cfg),
	})

	// Gateway session router.
	homeDir, _ := config.ConfigDir()
	routerPath := filepath.Join(homeDir, "gateway-router.json")
	router, err := gateway.NewSessionRouter(routerPath)
	if err != nil {
		logger.Printf("warning: session router init: %v (continuing without persistence)", err)
		router = nil
	}

	// Gateway dispatcher.
	disp := gateway.NewDispatcher(ag, router)
	disp.Logger = logger

	// Bridge config.
	listen := "127.0.0.1:7799"
	token := ""
	if cfg != nil && cfg.Gateways.Bridge.Enabled {
		if cfg.Gateways.Bridge.Listen != "" {
			listen = cfg.Gateways.Bridge.Listen
		}
		token = cfg.Gateways.Bridge.Token
	}

	bridge := bridgegw.New(disp, token, listen)
	bridge.Logger = logger

	// Signal handling.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		logger.Printf("shutting down...")
		cancel()
	}()

	logger.Printf("starting bridge on %s", listen)
	logger.Printf("sidecars POST to /v1/in, SSE-read from /v1/out?channel=<name>")
	if token != "" {
		logger.Printf("auth enabled (Bearer token)")
	}

	if err := bridge.Run(ctx); err != nil && err != context.Canceled {
		return fmt.Errorf("bridge: %w", err)
	}

	return nil
}

func init() {
	rootCmd.AddCommand(bridgeCmd)
}
