package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/ethos/internal/agent"
	"github.com/Sahaj-Tech-ltd/ethos/internal/config"
	"github.com/Sahaj-Tech-ltd/ethos/internal/hooks"
	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
	"github.com/Sahaj-Tech-ltd/ethos/internal/security"
	"github.com/Sahaj-Tech-ltd/ethos/internal/session"
	syncpkg "github.com/Sahaj-Tech-ltd/ethos/internal/sync"
	"github.com/Sahaj-Tech-ltd/ethos/internal/tokenizer"
	"github.com/Sahaj-Tech-ltd/ethos/internal/tools"
)

var (
	modelOverride    string
	providerOverride string
	noPersonality    bool
	noBoot           bool
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the agent loop",
	RunE:  runAgent,
}

func runAgent(cmd *cobra.Command, args []string) error {
	providerCfg, modelName := resolveProvider()

	if providerCfg == nil {
		fmt.Printf("%s✗ No provider configured.%s\n", colorRed, colorReset)
		fmt.Println()
		fmt.Println("OpenCode-style: set an API key env var and ethos auto-detects it:")
		fmt.Println()
		fmt.Println("  export OPENAI_API_KEY=sk-...")
		fmt.Println("  export ANTHROPIC_API_KEY=sk-ant-...")
		fmt.Println("  export GEMINI_API_KEY=...")
		fmt.Println("  export DEEPSEEK_API_KEY=sk-...")
		fmt.Println()
		fmt.Println("Or add providers manually:")
		fmt.Println("  ethos config init")
		fmt.Println("  # edit ~/.ethos/config.toml")
		return fmt.Errorf("no provider configured")
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

	cwd, _ := os.Getwd()
	toolReg := tools.NewRegistry()
	toolReg.Register(tools.NewShellTool())
	toolReg.Register(tools.NewFSTool(cwd))
	toolReg.Register(tools.NewGitTool(cwd))
	toolReg.Register(tools.NewGrepTool(cwd))
	toolReg.Register(tools.NewWebTool())

	agentCfg := agent.Config{
		Provider:     provider,
		Tools:        toolReg,
		Compressors:  tools.NewCompressorRegistry(),
		Hooks:        hooks.NewRegistry(),
		Scanners:     []security.Scanner{security.NewCommandScanner(security.WithProjectPath(cwd))},
		Tokenizer:    tokenizer.NewEstimator(),
		Steering:     agent.NewSteeringQueue(agent.SteeringDrainAll),
		Model:        modelName,
		MaxTokens:    200000,
		SystemPrompt: buildSystemPrompt(cfg),
	}

	a := agent.New(agentCfg)

	// Best-effort sync manager — only when the user enabled it in config.
	// We only need it if auto-push is on; otherwise skip the open entirely.
	var syncMgr *syncpkg.Manager
	var sessionStore *session.BadgerStore
	if cfg != nil && cfg.Sync.AutoPush && cfg.Sync.Backend != "" {
		if home, herr := os.UserHomeDir(); herr == nil {
			dir := home + "/.ethos/sessions"
			_ = os.MkdirAll(dir, 0o755)
			if s, serr := session.NewBadgerStore(dir); serr == nil {
				sessionStore = s
				if be, berr := syncpkg.NewBackend(cfg.Sync); berr == nil && be != nil {
					syncMgr = syncpkg.NewManager(s, be)
				}
			}
		}
	}
	if sessionStore != nil {
		defer sessionStore.Close()
	}

	fmt.Printf("ethos > %s %s\n", providerCfg.Name, modelName)
	fmt.Printf("Type a message, /help for commands, Ctrl+D to exit.\n\n")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Printf("\n%sShutting down...%s\n", colorYellow, colorReset)
		cancel()
		os.Exit(0)
	}()

	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Printf("%s>%s ", colorGreen, colorReset)
		if !scanner.Scan() {
			fmt.Println("\nGoodbye.")
			return nil
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		if strings.HasPrefix(input, "/") {
			parts := strings.Fields(input)
			switch strings.ToLower(parts[0]) {
			case "/exit", "/quit":
				fmt.Println("Goodbye.")
				return nil
			case "/help":
				fmt.Println("  /exit /quit  - quit")
				fmt.Println("  /help        - this message")
				fmt.Println("  /clear       - clear history")
				fmt.Println("  /history     - show history")
				fmt.Println("  /model <id>  - switch model")
			case "/clear":
				a.ClearHistory()
				fmt.Printf("%sHistory cleared.%s\n", colorGreen, colorReset)
			case "/history":
				for i, msg := range a.History() {
					fmt.Printf("  %d [%s] %s\n", i+1, msg.Role, truncate(msg.Content, 200))
				}
			case "/model":
				if len(parts) > 1 {
					a.SetModel(parts[1])
					fmt.Printf("%sModel: %s%s\n", colorGreen, parts[1], colorReset)
				} else {
					fmt.Printf("Model: %s\n", a.Model())
				}
			default:
				fmt.Printf("%sUnknown: %s%s\n", colorYellow, parts[0], colorReset)
			}
			continue
		}

		fmt.Print("\r\033[K")
		result, err := a.Run(ctx, input)
		if err != nil {
			fmt.Printf("%s✗ %s%s\n", colorRed, err, colorReset)
			continue
		}

		if result.Blocked {
			fmt.Printf("%s✗ Blocked: %s%s\n", colorRed, result.BlockReason, colorReset)
		} else {
			fmt.Println(result.Response)
		}

		fmt.Printf("\n%s%d steps · %d tools · %d tokens%s\n\n",
			colorDim, result.Steps, result.ToolCalls, result.TotalTokens, colorReset)

		// Mirror TUI behaviour: optional non-blocking sync push after each
		// successful turn. Errors land on stderr so the user sees them
		// without breaking the REPL.
		syncpkg.AutoPushIfEnabled(cfg, syncMgr, a.SessionID(), func(err error) {
			fmt.Fprintf(os.Stderr, "%ssync auto-push failed: %s%s\n", colorYellow, err.Error(), colorReset)
		})
	}
}

func resolveProvider() (*config.ProviderConfig, string) {
	// First: check config file
	if providerOverride != "" {
		for i := range cfg.Providers {
			if cfg.Providers[i].Name == providerOverride {
				return &cfg.Providers[i], cfg.Providers[i].Models[0].ID
			}
		}
	}
	if len(cfg.Providers) > 0 {
		p := &cfg.Providers[0]
		model := cfg.Agent.DefaultModel
		if model == "" && len(p.Models) > 0 {
			model = p.Models[0].ID
		}
		if model == "" {
			model = "gpt-4o"
		}
		return p, model
	}

	// Second: auto-detect from env vars (like OpenCode)
	detected := detectProviderFromEnv()
	if detected != nil {
		return detected, ""
	}

	return nil, ""
}

var envProviders = []struct {
	name    string
	envVar  string
	typ     string
	baseURL string
}{
	{"openai", "OPENAI_API_KEY", "openai", "https://api.openai.com/v1"},
	{"anthropic", "ANTHROPIC_API_KEY", "anthropic", "https://api.anthropic.com"},
	{"gemini", "GEMINI_API_KEY", "gemini", "https://generativelanguage.googleapis.com/v1beta"},
	{"deepseek", "DEEPSEEK_API_KEY", "deepseek", "https://api.deepseek.com/v1"},
	{"openrouter", "OPENROUTER_API_KEY", "openrouter", "https://openrouter.ai/api/v1"},
	{"groq", "GROQ_API_KEY", "groq", "https://api.groq.com"},
	{"xai", "XAI_API_KEY", "xai", "https://api.x.ai"},
}

func detectProviderFromEnv() *config.ProviderConfig {
	for _, ep := range envProviders {
		if key := os.Getenv(ep.envVar); key != "" {
			return &config.ProviderConfig{
				Name:    ep.name,
				Type:    ep.typ,
				APIKey:  key,
				BaseURL: ep.baseURL,
			}
		}
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func buildSystemPrompt(cfg *config.Config) string {
	name := cfg.Agent.Name
	if name == "" {
		name = "Ethos"
	}
	return fmt.Sprintf(`You are %s, a vibe-coding agent with discipline.
You can run shell commands, read/write files, search code, and interact with git.
Be concise and direct. Never guess URLs. Follow existing code conventions.`, name)
}

func providerEnvVar(name string) string {
	for _, ep := range envProviders {
		if ep.name == name {
			return ep.envVar
		}
	}
	return strings.ToUpper(name) + "_API_KEY"
}

func init() {
	runCmd.Flags().StringVar(&modelOverride, "model", "", "override default model")
	runCmd.Flags().StringVar(&providerOverride, "provider", "", "override default provider")
	runCmd.Flags().BoolVar(&noPersonality, "no-personality", false, "disable personality engine")
	runCmd.Flags().BoolVar(&noBoot, "no-boot", false, "skip boot animation")
	rootCmd.AddCommand(runCmd)
}
