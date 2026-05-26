package checks

import (
	"context"
	"sort"

	"github.com/Sahaj-Tech-ltd/overkill/internal/doctor"
	"github.com/Sahaj-Tech-ltd/overkill/internal/hooks"
	"github.com/Sahaj-Tech-ltd/overkill/internal/skills"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tokenizer"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
)

// (no extra helpers needed; constructors are called inline in buildToolRegistry.)

// expectedTools is the master plan §4.13 #11 list — the runtime guarantees
// these built-ins are present whenever the agent boots. Missing one would
// indicate a corrupt build or a deliberate stripping.
var expectedTools = []string{
	"shell", "fs", "git", "grep", "web",
	"patch", "pty_shell",
	"worktree_list", "worktree_add", "worktree_remove",
	"ask_user",
}

// RegisterTokenizer encodes a fixed string and asserts a non-zero token
// count. A zero count would mean the tokenizer is mis-wired.
func RegisterTokenizer(r *doctor.Runner, d Deps) {
	r.Register(doctor.SubsystemCheck{
		ID:       "tokenizer",
		Name:     "Tokenizer",
		Category: doctor.CatCore,
		Fn: func(ctx context.Context) doctor.Result {
			est := tokenizer.NewEstimator()
			n := est.Estimate("hello world")
			if n <= 0 {
				return failf("file a bug — tokenizer returns 0",
					"Estimate(\"hello world\") returned %d", n)
			}
			return okf("estimator returned %d tokens for 'hello world'", n)
		},
	})
}

// RegisterTools constructs the same registry the run/tui paths build, then
// asserts every expected tool is present.
func RegisterTools(r *doctor.Runner, d Deps) {
	r.Register(doctor.SubsystemCheck{
		ID:       "tools.registry",
		Name:     "Tools registry",
		Category: doctor.CatCore,
		Fn: func(ctx context.Context) doctor.Result {
			reg := buildToolRegistry()
			present := map[string]bool{}
			for _, n := range reg.List() {
				present[n] = true
			}
			var missing []string
			for _, want := range expectedTools {
				if !present[want] {
					missing = append(missing, want)
				}
			}
			if len(missing) > 0 {
				sort.Strings(missing)
				return failf("rebuild overkill from a clean checkout",
					"missing built-in tools: %v", missing)
			}
			return okf("%d tools registered", len(reg.List()))
		},
	})
}

// buildToolRegistry mirrors cmd/overkill/run.go so the doctor reflects what the
// agent actually loads. Kept narrow on purpose — anything optional belongs
// in its own check.
func buildToolRegistry() *tools.Registry {
	reg := tools.NewRegistry()
	_ = reg.Register(tools.NewShellTool())
	_ = reg.Register(tools.NewFSTool("."))
	_ = reg.Register(tools.NewGitTool("."))
	_ = reg.Register(tools.NewGrepTool("."))
	_ = reg.Register(tools.NewWebTool())
	_ = reg.Register(tools.NewPatchTool("."))
	_ = reg.Register(tools.NewPTYShellTool("."))
	_ = reg.Register(tools.NewWorktreeListTool("."))
	_ = reg.Register(tools.NewWorktreeAddTool("."))
	_ = reg.Register(tools.NewWorktreeRemoveTool("."))
	_ = reg.Register(tools.NewAskUserTool(nil))
	return reg
}

// RegisterHooks reports the count of hooks an empty registry knows about.
// Info-only — there is no required minimum.
func RegisterHooks(r *doctor.Runner, d Deps) {
	r.Register(doctor.SubsystemCheck{
		ID:       "hooks.registry",
		Name:     "Hooks registry",
		Category: doctor.CatCore,
		Fn: func(ctx context.Context) doctor.Result {
			reg := hooks.NewRegistry()
			total := 0
			for _, list := range reg.ListAll() {
				total += len(list)
			}
			return info("%d hook(s) registered", total)
		},
	})
}

// RegisterSkills loads the skill registry and reports the enabled subset.
// Info-only.
func RegisterSkills(r *doctor.Runner, d Deps) {
	r.Register(doctor.SubsystemCheck{
		ID:       "skills",
		Name:     "Skills",
		Category: doctor.CatCore,
		Fn: func(ctx context.Context) doctor.Result {
			reg := skills.NewRegistry()
			enabled := []string{}
			if d.Cfg != nil {
				enabled = append(enabled, d.Cfg.Skills.Enabled...)
			}
			return info("%d skill(s) loaded; %d enabled in config", reg.Count(), len(enabled))
		},
	})
}
