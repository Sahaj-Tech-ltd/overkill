// Package agent — Reflexion wiring (paper #51 AlphaGRPO recipe).
//
// The agent keeps a tiny Reflector interface so internal/reflect can
// own the actual reflection logic without internal/agent picking up
// a hard import. After each tool batch, stream.go classifies failed
// results, asks the reflector for a structured note, and injects
// that note as a system message before the next model turn.
//
// Budgeted: max N reflections per turn (default 2). Multiple
// concurrent failures don't drown the next prompt; the agent sees
// the most salient ones and the rest still surface via raw error.
package agent

// Failure mirrors internal/reflect.Failure so the agent doesn't have
// to import that package. The cmd/overkill wiring layer adapts
// between the two — same shape, separated by an interface.
type Failure struct {
	ToolName string
	Input    string
	Output   string
	Error    string
}

// Reflection mirrors internal/reflect.Reflection at the agent
// boundary. Only the fields the agent needs to inject the note.
type Reflection struct {
	Mode       string  // string-typed at this boundary; internal/reflect owns the enum
	RootCause  string
	Hypothesis string
	Confidence float64
}

// Reflector is the minimal hook the agent calls. The wiring layer
// implements this in terms of internal/reflect.
type Reflector interface {
	// IsFailure decides whether this tool result warrants a
	// reflection. Cheaper than calling Reflect on every result —
	// most tool calls succeed and don't need this path.
	IsFailure(toolName, output, err string) bool

	// Reflect produces the structured reflection for one failure.
	// Must be fast (the agent calls this on the hot path) — heavy
	// LLM reflectors should cap their own latency.
	Reflect(f Failure) Reflection

	// FormatNote renders the prose injected into history as a
	// system message. Separate from Reflect so the agent can
	// dedupe or rate-limit before paying the rendering cost.
	FormatNote(toolName string, r Reflection) string
}

const defaultReflectionBudget = 2

// SetReflector wires the reflector. Pass nil to disable. Setting a
// non-nil reflector implicitly enables a 2-note-per-turn budget;
// override with SetReflectionBudget if you need a different cap.
func (a *Agent) SetReflector(r Reflector) {
	if a == nil {
		return
	}
	a.verifierMu.Lock()
	a.reflector = r
	if r != nil && a.reflectionBudget == 0 {
		a.reflectionBudget = defaultReflectionBudget
	}
	a.verifierMu.Unlock()
}

// SetReflectionBudget overrides the per-turn reflection note cap.
// 0 disables reflection even if a reflector is wired.
func (a *Agent) SetReflectionBudget(n int) {
	if a == nil {
		return
	}
	a.verifierMu.Lock()
	a.reflectionBudget = n
	a.verifierMu.Unlock()
}

// getReflector reads the current reflector + budget under read lock.
// Returns (nil, 0) when reflection is disabled.
func (a *Agent) getReflector() (Reflector, int) {
	if a == nil {
		return nil, 0
	}
	a.verifierMu.RLock()
	defer a.verifierMu.RUnlock()
	return a.reflector, a.reflectionBudget
}
