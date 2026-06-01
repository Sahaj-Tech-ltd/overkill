package doctor

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// wiringAudit verifies that every exported constructor (NewXxx) defined under
// internal/ is either explicitly listed as wired or exempted. This prevents the
// "built but never wired" anti-pattern (ColdStartManager, speculation, etc.).
//
// When you add a new NewXxx() function, you MUST add it to either:
//   - knownWiredConstructors (with wired=true) — it's called from production code
//   - knownWiredConstructors (with wired=false) — deliberately NOT wired yet, with a reason comment
//   - wiringExemptPackages — the whole package is library-only
//
// The test fails if any constructor is unaccounted for.

var wiringExemptPackages = map[string]bool{
	"internal/tokenizer":       true, // pure library via NewEstimator
	"internal/providers":       true, // factory-driven via NewProvider
	"internal/security":        true, // config-driven scanner constructors
	"internal/compaction":      true, // wired via NewAgentCompactor
	"internal/credit":          true, // pure math library
	"internal/db":              true, // pure library via Open()
	"internal/multimodal":      true, // utility library
	"internal/hooks":           true, // registry-based, no direct NewXxx wiring needed
	"internal/skills":          true, // registry-based
	"internal/events":          true, // registry-based
	"internal/plugin":          true, // manager-driven via NewManager
	"internal/gateway":         true, // bot constructors wired from daemon_gateways.go
	"internal/diagnostic":      true, // on-demand, not wired at boot
	"internal/introspection":   true, // on-demand, not wired at boot
	"internal/playbooks":       true, // on-demand
	"internal/plan":            true, // on-demand
	"internal/tags":            true, // utility
	"internal/tasks":           true, // utility
	"internal/verify":          true, // on-demand
	"internal/prompt":          true, // utility
	"internal/checkpoint":      true, // wired via agent.Config
	"internal/extensions":      true, // manager-driven
	"internal/tools":           true, // factory-driven via tools.NewFactory
	"internal/pipeline":        true, // on-demand
	"internal/worktree":        true, // on-demand
	"internal/learning":        true, // on-demand
	"internal/share":           true, // on-demand
	"internal/sync":            true, // on-demand (Autopush wired inline)
	"internal/lats":            true, // on-demand branching
	"internal/lsp":             true, // manager-driven
	"internal/mcp":             true, // manager-driven
	"internal/acp":             true, // server-driven
	"internal/web":             true, // server-driven
	"internal/api":             true, // server constructors (NewServer, NewSecureStore)
	"internal/automation":      true, // daemon-driven constructors
	"internal/cron":            true, // daemon-driven scheduler
	"internal/cost":            true, // wired via tracker in agent.Config
	"internal/daemon":          true, // daemon constructors
	"internal/journal":         true, // wired via agent.Config
	"internal/walls":           true, // wired via FactoryDeps
	"internal/personality":     true, // constructors wired via personality subsystem
	"internal/memory":          true, // wired via memory orchestrator
	"internal/session":         true, // wired via session store
	"internal/subagent":        true, // wired via delegation manager
	"internal/routing":         true, // wired via SmartRouter
	"internal/rewriter":        true, // wired via prompt rewriter middleware
	"internal/speculative":     true, // on-demand read cache
	"internal/browser":         true, // factory-driven
	"internal/doctor":          true, // test-only, this package itself
	"internal/automemory":      true, // wired via agent.Config
	"internal/drift":           true, // wired via agent.Config
	"internal/features":        true, // feature flag manager
	"internal/flows":           true, // task-flow storage (unwired, dead package)
	"internal/halluscan":       true, // hallucination scanner
	"internal/modules":         true, // dead package, modules not active
	"internal/prompt/chips":    true, // prompt chip utilities
	"internal/slack":           true, // internal utility for slack gateway
	"internal/tools/bash":      true, // bash security utilities
	"internal/vision":          true, // vision describer utilities
	"internal/skills/safety":   true, // sub-package of skills
	"internal/skills/registry": true, // sub-package of skills
}

// knownWiredConstructors maps package.FuncName to whether it's wired.
// wired=true  → confirmed called from production code
// wired=false → intentionally not wired (must include reason as comment)
var knownWiredConstructors = map[string]bool{
	// ── agent/ ──────────────────────────────────────────
	"agent.New":                              true,
	"agent.NewRedTeamTestGen":                true,
	"agent.NewSteeringQueue":                 true,
	"agent.NewSpecDriver":                    true,
	"agent.NewPromptCompressor":              true,  // WIRED: via SetPromptCompressor in agent.New() with AgentPromptCompressor adapter
	"agent.NewAutoMode":                      true,  // WIRED: SetAutoMode called from /auto, /yolo, /safe + hotreload
	"agent.NewBudgetEstimatorWithThresholds": true,  // WIRED: created in agent.New() when tokenizer present with config thresholds
	"agent.NewBudgetEstimator":               true,  // WIRED: created in agent.New() when tokenizer present, used by checkBudget
	"agent.NewCheckpointManager":             true,  // WIRED: git snapshots, SetCheckpointManager called at boot
	"agent.NewContentClassifier":             true,  // WIRED: shell vs NL routing, SetContentClassifier called at boot
	"agent.NewContractDriver":                false, // DEAD: subagent contract enforcement, no callers outside tests
	"agent.NewEventBus":                      true,  // WIRED: created in agent.New(), used by agent.emit() for event fanout
	"agent.NewMemoryFlowStore":               true,  // WIRED: flow checkpointing, SetFlowStore called at boot
	"agent.NewPostgresFlowStore":             false, // DEAD: NewMemoryFlowStore is used instead
	"agent.NewForethinker":                   true,  // WIRED: auto-created in agent.New() with fallback
	"agent.NewGoalStore":                     true,  // WIRED: Postgres-backed, conditional on DATABASE_URL
	"agent.NewGoalAccountingState":           false, // DEAD: depends on goal store, no production caller
	"agent.NewReceiptChain":                  true,  // WIRED: created in agent.New(), used by tool dispatch for crypto audit chain
	"agent.NewErrorRecovery":                 true,  // WIRED: auto-created in agent.New()
	"agent.NewToolInventory":                 true,  // WIRED: situational awareness, SetToolInventory called at boot
	"agent.NewSelfEvaluateLoop":              true,  // WIRED: called in slash_commands.go /auto, /build handlers
	"agent.NewDecomposer":                    true,  // WIRED: created by NewSequentialProcessor + used in subagent manager
	"agent.NewSequentialProcessor":           true,  // WIRED: lazy-created in runSequential() via /think toggle
	"agent.NewItemContext":                   false, // DEAD: never created in production, only in tests
	"agent.NewSuspendedApprover":             false, // DEAD: suspend/resume approval gate, no production caller
	"agent.NewDeepPlanner":                   true,  // WIRED: handleDeepPlan in slash_commands.go, /plan command
	"agent.NewTestAgent":                     false, // test-only constructor, intentionally test-only

	// ── compaction/ ────────────────────────────────────
	"compaction.NewAgentCompactor": true,
	"compaction.NewLCMCompactor":   true,

	// ── config/ ────────────────────────────────────────
	"config.NewDefaultConfig": true,
	"config.Load":             true,
	"config.NewSetupWizard":   true,

	// ── personality/ ───────────────────────────────────
	"personality.NewColdStartManager":    true, // wired in run.go REPL loop
	"personality.NewColdStartProtocol":   true, // called by ColdStartManager
	"personality.NewBlindSpotDetector":   true,
	"personality.NewRelationshipTracker": true,
	"personality.NewFrustrationDetector": true,
	"personality.NewTransparencyEngine":  true,
	"personality.NewStyleInferencer":     true,
	"personality.NewBeatRecorder":        true,

	// ── audit/ ─────────────────────────────────────────
	// audit.Auditor is created as &audit.Auditor{} directly (no NewXxx constructor).
	// Wired in run.go and handlers.go agent boot paths.

	// ── speculation/ ──────────────────────────────────
	"speculation.NewEngine": true, // wired in run.go and handlers.go agent boot

	// ── tokenizer/ (exempt package but listing for clarity)
	"tokenizer.NewEstimator": true,
}

// TestWiringAudit scans all exported constructors in internal/ and verifies
// each is accounted for in knownWiredConstructors or wiringExemptPackages.
func TestWiringAudit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping wiring audit in short mode")
	}

	projectRoot := findProjectRoot(t)
	constructors := findConstructors(t, projectRoot)

	var unaccounted []string
	var unwired []string
	for _, c := range constructors {
		full := c.pkg + "." + c.name
		wired, known := knownWiredConstructors[full]
		if known {
			if !wired {
				unwired = append(unwired, fmt.Sprintf("  %s — %s", full, c.file))
			}
			continue
		}
		// Also try without the leading "internal/" prefix (legacy map keys).
		short := strings.TrimPrefix(full, "internal/")
		wired, known = knownWiredConstructors[short]
		if known {
			if !wired {
				unwired = append(unwired, fmt.Sprintf("  %s — %s", full, c.file))
			}
			continue
		}
		// Check if any parent package path matches an exempt entry.
		exempt := false
		for exemptPkg := range wiringExemptPackages {
			if c.pkg == exemptPkg || strings.HasPrefix(c.pkg, exemptPkg+"/") {
				exempt = true
				break
			}
		}
		if exempt {
			continue
		}
		unaccounted = append(unaccounted, fmt.Sprintf("  %s — %s (add to knownWiredConstructors or wiringExemptPackages)", full, c.file))
	}

	if len(unaccounted) > 0 {
		t.Errorf("UNACCOUNTED constructors — must be listed in knownWiredConstructors or wiringExemptPackages:\n\n%s",
			strings.Join(unaccounted, "\n"))
	}

	if len(unwired) > 0 {
		// Don't fail — these are intentionally unwired. Just print them.
		t.Logf("📋 Known-unwired constructors (intentional):\n%s", strings.Join(unwired, "\n"))
	}
}

type constructor struct {
	pkg  string
	name string
	file string
}

func findProjectRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("cannot find project root (go.mod)")
		}
		dir = parent
	}
}

var newFuncPattern = regexp.MustCompile(`^New[A-Z]`)

func findConstructors(t *testing.T, projectRoot string) []constructor {
	t.Helper()
	var constructors []constructor

	internalDir := filepath.Join(projectRoot, "internal")
	err := filepath.Walk(internalDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == "testdata" || info.Name() == ".git" || strings.HasPrefix(info.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil
		}

		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Recv != nil {
				continue
			}
			if !fd.Name.IsExported() {
				continue
			}
			if !newFuncPattern.MatchString(fd.Name.Name) {
				continue
			}

			relPath, _ := filepath.Rel(projectRoot, path)
			pkgPath := filepath.Dir(relPath)
			pkgCanonical := strings.ReplaceAll(pkgPath, string(filepath.Separator), "/")

			constructors = append(constructors, constructor{
				pkg:  pkgCanonical,
				name: fd.Name.Name,
				file: relPath,
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/: %v", err)
	}
	return constructors
}
