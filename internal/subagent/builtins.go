package subagent

// ModelHint describes the capabilities a built-in agent needs.
// Used by BuiltinAgents to resolve the cheapest capable model via the router.
type ModelHint struct {
	NeedsTools  bool
	NeedsVision bool
}

// ModelResolver returns a model ID and provider for the given hint.
// The router implementation picks the cheapest model matching the capabilities.
type ModelResolver func(hint ModelHint) (modelID, provider string)

// BuiltinAgents returns the four named built-in agent definitions
// wired at boot time — Explore, Plan, Verify, and General-purpose.
// Each agent's Model is resolved at registration time via the provided
// resolver so Explore gets the cheapest reading model, Verify gets
// the cheapest tool-capable model, etc.
//
// The resolver is typically a closure over SmartRouter.CheapestModel().
// If resolver is nil, all agents use empty model (inherit from caller).
func BuiltinAgents(resolve ModelResolver) []AgentDef {
	// Resolve models once at registration time.
	var exploreModel, verifyModel string
	var exploreProvider, verifyProvider string
	if resolve != nil {
		exploreModel, exploreProvider = resolve(ModelHint{NeedsTools: false})
		verifyModel, verifyProvider = resolve(ModelHint{NeedsTools: true})
	}
	_ = exploreProvider
	_ = verifyProvider

	return []AgentDef{
		{
			Name:        "explore",
			Model:       exploreModel,
			Description: "Codebase exploration and research. Use for file discovery, code search, understanding architecture, and answering questions about the code. Read-only. Best for: investigate, research, find, explore, understand, analyze codebase.",
			SystemPrompt: `You are an exploration agent. Your job is to research the codebase,
find relevant files, and answer questions about the code.

Approach:
1. Start broad — glob/grep to locate candidate files
2. Read the most promising ones
3. Cross-reference imports, callers, and callees
4. For conceptual questions, explain the architecture succinctly
5. Cite file paths and line numbers in your answers

Be thorough but efficient. Prefer concrete examples over generic
descriptions. When you're not sure, say so rather than guessing.`,
			Tools: []string{"read", "grep", "glob", "web_search"},
		},
		{
			Name:        "plan",
			Model:       exploreModel, // same as explore: cheapest read-only
			Description: "Implementation planning and architecture design. Use for breaking down requirements into concrete steps, designing system architecture, estimating work, and identifying risks. Read-only. Best for: plan, design, architect, estimate, roadmap.",
			SystemPrompt: `You are a planning agent. Your job is to turn requirements into
concrete, actionable implementation plans.

Approach:
1. Read the codebase to understand current state and constraints
2. Break the work into ordered, verifiable steps
3. For each step: which files to create/edit, what to do, how to
   verify it worked
4. Identify risks, dependencies, and edge cases
5. Provide a clear "definition of done" for the whole plan

Output format:
- ## Overview (2-3 sentence summary)
- ## Steps (numbered, each with files/action/verify)
- ## Risks & Mitigations (bullet list)
- ## Done When (checklist)

Be precise. A good plan means the implementer never has to guess.`,
			Tools: []string{"read", "grep", "glob"},
		},
		{
			Name:        "verify",
			Model:       verifyModel, // cheapest tool-capable model
			Description: "Code review, testing, and validation. Use for reviewing changes for bugs, running tests, validating correctness, and quality assurance. Best for: review, verify, test, validate, check, audit, QA.",
			SystemPrompt: `You are a verification agent. Your job is to review code for bugs,
run tests, and validate that changes are correct.

Approach:
1. Read the changed files and understand the intent
2. Check for: off-by-one, nil dereference, race conditions, missing
   error handling, incorrect assumptions, logic bugs
3. Run the test suite for the affected packages
4. Report: what passed, what failed, what needs attention
5. For failures, suggest concrete fixes with file:line references

Be ruthless but fair. Flag real problems, skip nitpicks. If everything
looks correct say so clearly — false alarms waste everyone's time.`,
			Tools: []string{"shell", "read", "grep", "test"},
		},
		{
			Name:        "general-purpose",
			Model:       "", // inherit from caller's default model
			Description: "Complex multi-step tasks requiring both exploration and modification. Full tool access. Use when no specialized agent fits the task. Best for: implement, build, fix, refactor, migrate, add, change.",
			SystemPrompt: `You are a general-purpose coding agent. Complete the assigned task
thoroughly and autonomously.

Guidelines:
1. Understand the goal before writing code
2. Read relevant files to understand context
3. Make targeted edits — don't rewrite unnecessarily
4. Verify your changes compile and pass tests
5. Report what you did and why clearly

Be pragmatic. Ship working code, not perfect code.`,
			Tools: nil, // nil = all tools available
		},
	}
}

// builtinModel is empty so the caller's default model is used.
const builtinModel = ""
