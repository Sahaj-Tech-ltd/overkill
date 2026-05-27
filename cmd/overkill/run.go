package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/compaction"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/credit"
	"github.com/Sahaj-Tech-ltd/overkill/internal/drift"
	"github.com/Sahaj-Tech-ltd/overkill/internal/events"
	eventsinks "github.com/Sahaj-Tech-ltd/overkill/internal/events/sinks"
	"github.com/Sahaj-Tech-ltd/overkill/internal/extensions"
	"github.com/Sahaj-Tech-ltd/overkill/internal/hooks"
	"github.com/Sahaj-Tech-ltd/overkill/internal/hotreload"
	"github.com/Sahaj-Tech-ltd/overkill/internal/input"
	"github.com/Sahaj-Tech-ltd/overkill/internal/journal"
	"github.com/Sahaj-Tech-ltd/overkill/internal/lats"
	"github.com/Sahaj-Tech-ltd/overkill/internal/learning"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/speculative"
	syncpkg "github.com/Sahaj-Tech-ltd/overkill/internal/sync"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tokenizer"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	imagegen "github.com/Sahaj-Tech-ltd/overkill/internal/tools/imagegen"
	messaging "github.com/Sahaj-Tech-ltd/overkill/internal/tools/messaging"
	ttspkg "github.com/Sahaj-Tech-ltd/overkill/internal/tools/tts"
)

var (
	modelOverride    string
	providerOverride string
	noPersonality    bool
	noBoot           bool
	latsEnabled      bool
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
		fmt.Println("OpenCode-style: set an API key env var and overkill auto-detects it:")
		fmt.Println()
		fmt.Println("  export OPENAI_API_KEY=sk-...")
		fmt.Println("  export ANTHROPIC_API_KEY=sk-ant-...")
		fmt.Println("  export GEMINI_API_KEY=...")
		fmt.Println("  export DEEPSEEK_API_KEY=sk-...")
		fmt.Println()
		fmt.Println("Or add providers manually:")
		fmt.Println("  overkill config init")
		fmt.Println("  # edit ~/.overkill/config.toml")
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
	toolReg.Register(ttspkg.New(cfg.TTS))
	toolReg.Register(messaging.New(cfg.Gateways))
	toolReg.Register(imagegen.New(cfg.ImageGen))

	agentCfg := agent.Config{
		Provider:    provider,
		Tools:       toolReg,
		Compressors: tools.NewCompressorRegistry(),
		Hooks:       hooks.NewRegistry(),
		Scanners: []security.Scanner{
			security.NewCommandScanner(
				security.WithProjectPath(cwd),
				security.WithExtraDenyPatterns(cfg.Security.DenyPatterns),
				security.WithForbiddenPaths(cfg.Security.ForbiddenPaths),
				security.WithMaxCommandLen(cfg.Security.MaxCommandLen),
			),
			// InjectionScanner catches "ignore previous instructions" /
			// role-override patterns in tool inputs. Was implemented but
			// never wired into the pre-tool scanner list — every shell
			// invocation skipped the prompt-injection check.
			security.NewInjectionScanner(),
		},
		Tokenizer:    tokenizer.NewEstimator(),
		Steering:     agent.NewSteeringQueue(agent.SteeringDrainAll),
		Model:        modelName,
		MaxTokens:    200000,
		SystemPrompt: buildSystemPrompt(cfg),
	}

	a := agent.New(agentCfg)

	// Wire the learning-from-corrections store (§6.5). This persists
	// user corrections so future turns benefit from past feedback.
	connString := cfg.DatabaseURL
	if connString == "" {
		connString = os.Getenv("DATABASE_URL")
	}
	if connString != "" {
		if ls, err := learning.NewStore(connString, 1000); err == nil {
			a.SetLearningStore(ls)
			defer ls.Close()
		}
	}

	// P0: context compaction — wire LCM-based compactor.
	if compactProv := provider; compactProv != nil {
		compactor := compaction.NewAgentCompactor(compactProv, tokenizer.NewEstimator(), 20)
		a.SetCompactor(compactor, true)
	}

	// P0: hotreload — wire config file watcher.
	if hotReloadBus != nil {
		homeDir, _ := config.ConfigDir()
		if homeDir != "" {
			userYAML := filepath.Join(homeDir, "user.yaml")
			if _, err := hotreload.WireAgent(context.Background(), hotReloadBus, a, userYAML, hotreload.DiscardReporter()); err != nil {
				log.Printf("hotreload: wire agent: %v", err)
			}
		}
	}

	// P0: input classifier — shell vs NL routing.
	a.SetInputClassifier(func(raw string) agent.InputKind {
		return agent.InputKind(input.Classify(raw))
	})

	// P1: events/sinks — completion event emitter.
	emit := events.NewEmitter(eventsinks.NewLogSink(log.Default()))
	a.SetCompletionEmitter(emit, nil)

	// P1: feature flags.
	if featureMgr != nil {
		a.SetFeatureManager(featureMgr)
	}

	// P2: speculative read cache + prefetcher.
	readCache := speculative.NewReadCache(speculative.Options{})
	a.SetReadCache(readCache)
	prefetcher := speculative.NewPrefetcher(readCache, 2, 64)
	prefetcher.Start(2)
	defer prefetcher.Stop()

	// P2: extensions manager.
	if extensionsMgr != nil {
		a.SetExtensionsManager(wrapExtensions(extensionsMgr))
	}

	// Best-effort sync manager — only when the user enabled it in config.
	// We only need it if auto-push is on; otherwise skip the open entirely.
	var syncMgr *syncpkg.Manager
	var sessionStore *session.BadgerStore
	if cfg != nil && cfg.Sync.AutoPush && cfg.Sync.Backend != "" {
		if home, herr := os.UserHomeDir(); herr == nil {
			dir := home + "/.overkill/sessions"
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

	fmt.Printf("overkill > %s %s\n", providerCfg.Name, modelName)
	fmt.Printf("Type a message, /help for commands, Ctrl+D to exit.\n\n")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Printf("\n%sShutting down...%s\n", colorYellow, colorReset)
		finalizeSession(a, providerCfg.Name, modelName)
		cancel()
		os.Exit(0)
	}()

	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Printf("%s>%s ", colorGreen, colorReset)
		if !scanner.Scan() {
			fmt.Println("\nGoodbye.")
			finalizeSession(a, providerCfg.Name, modelName)
			return nil
		}

		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}

		// P0: classify input — route shell commands directly.
		if input.Classify(raw) == input.KindShell {
			fmt.Print("\r\033[K")
			fmt.Printf("%s$ %s%s\n", colorDim, raw, colorReset)
			// Execute shell command directly.
			runShellCommand(raw)
			continue
		}

		input := raw

		if strings.HasPrefix(input, "/") {
			parts := strings.Fields(input)
			switch strings.ToLower(parts[0]) {
			case "/exit", "/quit":
				fmt.Println("Goodbye.")
				finalizeSession(a, providerCfg.Name, modelName)
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

		var result *agent.RunResult
		var err error

		// P3: LATS — multi-branch tree search.
		if latsEnabled {
			branches := []lats.Branch{
				{ID: "direct", Approach: "direct solution"},
				{ID: "careful", Approach: "careful step-by-step analysis"},
			}
			runner := lats.RunnerFunc(func(ctx context.Context, branch lats.Branch, workdir string) (string, string, error) {
				r, e := a.Run(ctx, input)
				if e != nil {
					return "failed", "", e
				}
				return "completed", r.Response, nil
			})
			results, latsErr := lats.Race(context.Background(), branches, runner, nil, lats.Options{
				MaxBranches:    2,
				PerBranchTimeout: 5 * time.Minute,
				FallbackWorkdir: cwd,
			}, nil)
			if latsErr == nil && len(results) > 0 {
				result = &agent.RunResult{Response: results[0].Response}
				fmt.Printf("%sLATS winner: %s (score %.2f)%s\n", colorDim, results[0].Branch.Approach, results[0].Score, colorReset)
			} else {
				result, err = a.Run(ctx, input)
			}
		} else {
			result, err = a.Run(ctx, input)
		}
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

// finalizeSession runs cleanup hooks at session exit: journal summarizer,
// memory export, relationship arc persistence, credit folding, and drift
// detection. Best-effort only — errors are logged but never block exit.
func finalizeSession(a *agent.Agent, providerName, modelName string) {
	homeDir, err := config.ConfigDir()
	if err != nil {
		return
	}

	// Journal summarizer: fire sub-agent to write daily narrative.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		narrateCLISession(ctx, homeDir, a.SessionID(), providerName, modelName)
	}()

	// Memory export: write relationship arc + competence flags.
	go func() {
		exportMemoryIfNeeded(homeDir)
	}()

	// P3: credit assignment — fold session into analyzer.
	go func() {
		toolCalls, errs, recovs, turns, _ := a.SessionMetrics()
		actions := make([]credit.Action, 0)
		if toolCalls > 0 {
			actions = append(actions, credit.Action{Tag: "tool_call", Category: "tool"})
		}
		if errs > 0 {
			actions = append(actions, credit.Action{Tag: "error", Category: "error"})
		}
		if recovs > 0 {
			actions = append(actions, credit.Action{Tag: "recovery", Category: "recovery"})
		}
		_ = turns // unused for now

		outcome := credit.OutcomeSuccess
		if errs > 0 && recovs == 0 {
			outcome = credit.OutcomeFailure
		}
		analyzer := credit.NewAnalyzer()
		analyzer.Fold(credit.SessionRecord{
			SessionID: a.SessionID(),
			Outcome:   outcome,
			Actions:   actions,
			Tags:      []string{providerName, modelName},
		})
		// Save to disk.
		store := credit.NewStore(filepath.Join(homeDir, "credit"))
		_ = store.SaveSession(credit.SessionRecord{
			SessionID: a.SessionID(),
			Outcome:   outcome,
			Actions:   actions,
			Tags:      []string{providerName, modelName},
		})
	}()

	// P3: drift detection — compute session metrics and compare to baseline.
	go func() {
		toolCalls, errs, _, turns, _ := a.SessionMetrics()
		sample := make(map[drift.Metric]float64)
		if turns > 0 {
			sample[drift.MetricToolCallsPerTurn] = float64(toolCalls) / float64(turns)
		}
		sample[drift.MetricErrorRate] = float64(errs)
		sample[drift.MetricSessionLength] = float64(turns)

		store := drift.NewStore(filepath.Join(homeDir, "drift", "baseline.json"))
		baseline, _ := store.Load()
		if baseline != nil {
			findings := baseline.Compare(sample, drift.CompareOptions{Threshold: 2.0})
			if len(findings) > 0 {
				log.Printf("drift: %s", drift.FormatFindings(findings))
			}
			baseline.Fold(sample)
			_ = store.Save(baseline)
		}
	}()
}

// runShellCommand executes a shell command directly and prints output.
func runShellCommand(cmd string) {
	out, err := execShellCommand(cmd)
	if err != nil {
		fmt.Printf("%s✗ %s%s\n", colorRed, err, colorReset)
		return
	}
	fmt.Print(out)
}

// execShellCommand runs a shell command and returns its output.
func execShellCommand(cmd string) (string, error) {
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/sh"
	}
	c := exec.Command(sh, "-c", cmd)
	c.Env = os.Environ()
	c.Stdin = os.Stdin
	var stdout, stderr strings.Builder
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()
	out := stdout.String()
	if err != nil {
		return "", fmt.Errorf("%s: %w", stderr.String(), err)
	}
	return out, nil
}

// wrapExtensions adapts *extensions.Manager to agent.ExtensionsManager.
func wrapExtensions(m *extensions.Manager) agent.ExtensionsManager {
	return &extensionsAdapter{mgr: m}
}

type extensionsAdapter struct {
	mgr *extensions.Manager
}

func (e *extensionsAdapter) ListEnabled() []agent.ExtensionMeta {
	if e.mgr == nil {
		return nil
	}
	exts, _ := e.mgr.List()
	out := make([]agent.ExtensionMeta, 0, len(exts))
	for _, ext := range exts {
		if ext.Enabled {
			out = append(out, agent.ExtensionMeta{
				ID:          ext.ID,
				Name:        ext.Name,
				Kind:        string(ext.Kind),
				Description: ext.Description,
			})
		}
	}
	return out
}

// narrateCLISession runs the journal summarizer for the CLI (non-TUI)
// session path. Mirrors writeJournalNarrative but takes raw params
// instead of a TUI App struct.
//
// Uses the cheapest available model from the user's provider for the
// summarization to keep journal costs negligible (§4.19 sub-agent
// pattern — a tool-blocked observer that reads raw flight-recorder
// entries and produces prose).
func narrateCLISession(ctx context.Context, homeDir, sessionID, providerName, modelName string) {
	jdir := filepath.Join(homeDir, "journal")
	rec := journal.NewFlightRecorder(jdir, sessionID)

	pc, _ := resolveProvider()
	if pc == nil {
		return
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
	})
	if err != nil {
		return
	}

	// Pick the cheapest model from the provider's list for journal work.
	// Journal summarization is a fire-and-forget task — no tools needed,
	// no vision needed, just a single completion call. The cheapest model
	// that can produce coherent markdown prose wins.
	cheapestModel := pickCheapestModel(p)
	if cheapestModel == "" {
		cheapestModel = modelName // fallback to whatever was passed
	}
	if cheapestModel == "" {
		cheapestModel = "gpt-4o-mini" // absolute fallback
	}

	summ := journal.NewSummarizer(rec, p, cheapestModel)
	path, _, err := summ.NarrateSession(ctx, jdir, sessionID)
	if err != nil {
		log.Printf("journal narrate: %v", err)
		return
	}
	if path != "" {
		log.Printf("journal narrate: wrote %s (model=%s)", path, cheapestModel)
	}
}

// pickCheapestModel returns the ID of the cheapest model (by output cost)
// from the provider's model list. Returns empty string if no models.
func pickCheapestModel(p providers.Provider) string {
	models := p.Models()
	if len(models) == 0 {
		return ""
	}
	best := models[0]
	for i := 1; i < len(models); i++ {
		if models[i].CostOut < best.CostOut {
			best = models[i]
		}
	}
	return best.ID
}

// exportMemoryIfNeeded writes memory-export.md if it doesn't exist
// or is older than 24 hours. Contains a summary of recent sessions
// pulled from the journal entries directory.
func exportMemoryIfNeeded(homeDir string) {
	exportPath := filepath.Join(homeDir, "memory-export.md")
	// Check if recent enough
	if fi, err := os.Stat(exportPath); err == nil {
		if time.Since(fi.ModTime()) < 24*time.Hour {
			return // already fresh
		}
	}

	// Gather what we know from the journal
	entriesDir := filepath.Join(homeDir, "journal", "entries")
	entries, _ := os.ReadDir(entriesDir)

	var b strings.Builder
	b.WriteString("# Overkill Memory Export\n\n")
	b.WriteString("> Auto-generated on session exit.\n")
	b.WriteString("> Last updated: " + time.Now().Format("2006-01-02 15:04:05") + "\n\n")

	b.WriteString("## Recent Journal Entries\n\n")
	if len(entries) == 0 {
		b.WriteString("_(No journal entries yet. Start a session to populate the journal.)_\n\n")
	} else {
		// Show last 10 entries (newest first)
		start := 0
		if len(entries) > 10 {
			start = len(entries) - 10
		}
		for i := len(entries) - 1; i >= start; i-- {
			name := entries[i].Name()
			dateStr := strings.TrimSuffix(name, ".md")
			b.WriteString(fmt.Sprintf("- `%s` — journal entry\n", dateStr))
		}
	}

	// Check for soul.md
	soulPath := filepath.Join(homeDir, "memories", "soul.md")
	if _, err := os.Stat(soulPath); err == nil {
		b.WriteString("\n## Personality\n\n")
		b.WriteString("- Soul file: `memories/soul.md` ✓\n")
		b.WriteString("- Relationship arc: `memories/relationship-arc.json`\n")
	}

	// Check for skills
	skillsDir := filepath.Join(homeDir, "skills")
	if entries, err := os.ReadDir(skillsDir); err == nil && len(entries) > 0 {
		b.WriteString("\n## Active Skills\n\n")
		for _, e := range entries {
			if !e.IsDir() {
				b.WriteString(fmt.Sprintf("- `%s`\n", e.Name()))
			}
		}
	}

	b.WriteString("\n## Data Locations\n\n")
	b.WriteString(fmt.Sprintf("- Journal raw: `%s/journal/raw/`\n", homeDir))
	b.WriteString(fmt.Sprintf("- Journal entries: `%s/journal/entries/`\n", homeDir))
	b.WriteString(fmt.Sprintf("- Memories: `%s/memories/`\n", homeDir))
	b.WriteString(fmt.Sprintf("- Skills: `%s/skills/`\n", homeDir))

	_ = os.WriteFile(exportPath, []byte(b.String()), 0o644)
}

func buildSystemPrompt(cfg *config.Config) string {
	name := cfg.Agent.Name
	if name == "" {
		name = "Overkill"
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
	runCmd.Flags().BoolVar(&latsEnabled, "lats", false, "enable multi-branch LATS tree search (P3)")
	rootCmd.AddCommand(runCmd)
}
