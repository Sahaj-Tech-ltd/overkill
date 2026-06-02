// Package personality — learning memos and hypothesis tracking.
// Collects confidence-weighted facts from user interactions and
// surfaces them as context in future sessions.
package personality

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

// MemoPhraseRule associates regex patterns with contextual status phrases.
// The frontend matches user input against these patterns and cycles through
// matching phrases while the agent is thinking.
type MemoPhraseRule struct {
	Patterns []string `json:"patterns"`
	Phrases  []string `json:"phrases"`
	Category string   `json:"category"`
}

// MemoPhraseResult is returned from Match() for a given input.
type MemoPhraseResult struct {
	Phrase   string `json:"phrase"`
	Category string `json:"category"`
}

// MemoEngine powers the Memo the Elephant thinking indicator. It matches
// user input against phrase rules, supports action-specific phrases for
// tool calls, and learns new phrases via the self-improvement loop.
//
// The engine can be used entirely in-memory (for the TUI path) or with
// a Postgres-backed learning store for persistence.
type MemoEngine struct {
	mu    sync.RWMutex
	rules []compiledRule
	db    *sql.DB

	// Default phrases when no rules match.
	defaults []string

	// Tool-action phrases keyed by action name.
	actions map[string][]string

	// Custom phrases learned from self-improvement.
	custom []compiledRule
}

type compiledRule struct {
	patterns []*regexp.Regexp
	phrases  []string
	category string
}

// DefaultMemoRules returns the baseline phrase rules.
func DefaultMemoRules() []MemoPhraseRule {
	return []MemoPhraseRule{
		{
			Category: "research",
			Patterns: []string{`research`, `paper`, `arxiv`, `study`, `synthesize`, `literature`, `academic`},
			Phrases: []string{
				"Synthesizing papers in the trunk...",
				"Cross-referencing research...",
				"Never forgetting a citation...",
				"Two elephants, one literature review...",
				"Trunk-deep in the archives...",
			},
		},
		{
			Category: "debugging",
			Patterns: []string{`bug`, `fix`, `error`, `broke`, `debug`, `crash`, `fail`, `trace`},
			Phrases: []string{
				"Sniffing out the bug like a truffle pig...",
				"Tusks deployed for debugging...",
				"Charging at the error with full force...",
				"Two elephants can squash any bug...",
				"Tracking the stack trace with elephant precision...",
			},
		},
		{
			Category: "coding",
			Patterns: []string{`code`, `implement`, `build`, `create`, `write`, `refactor`, `migrate`, `feature`},
			Phrases: []string{
				"Trunk-deep in the codebase...",
				"Stampeding through files...",
				"Writing code that elephants would approve...",
				"Building something mighty...",
				"Crafting postgres-worthy code...",
			},
		},
		{
			Category: "deploy",
			Patterns: []string{`deploy`, `push`, `ship`, `release`, `publish`, `launch`},
			Phrases: []string{
				"Charging toward deployment...",
				"Summoning the build gods...",
				"Shipping with elephant-sized confidence...",
				"Two elephants pushing to prod...",
				"The frontier is scared of this deploy...",
			},
		},
		{
			Category: "memory",
			Patterns: []string{`memory`, `remember`, `recall`, `context`, `history`, `session`, `past`},
			Phrases: []string{
				"Never forgetting anything...",
				"Consolidating memories...",
				"Two elephants, zero forgotten context...",
				"Recalling everything you've ever said...",
				"The memory trunk never empties...",
			},
		},
		{
			Category: "review",
			Patterns: []string{`review`, `audit`, `check`, `inspect`, `analyze`, `scan`},
			Phrases: []string{
				"Inspecting with elephant eyes...",
				"Auditing the savanna...",
				"Two elephants reviewing your work...",
				"Nothing escapes an elephant audit...",
				"Scanning every file like a trunk sweeps the ground...",
			},
		},
		{
			Category: "planning",
			Patterns: []string{`plan`, `think`, `design`, `architect`, `spec`, `proposal`},
			Phrases: []string{
				"Planning with elephant foresight...",
				"Designing something elephants will remember...",
				"Architecting a savanna of ideas...",
				"Two elephants strategizing...",
			},
		},
		{
			Category: "testing",
			Patterns: []string{`test`, `verify`, `validate`, `assert`, `prove`},
			Phrases: []string{
				"Testing with elephant thoroughness...",
				"Two elephants verifying every edge case...",
				"Validating like an elephant never forgets a bug...",
				"Proving correctness, trunk-first...",
			},
		},
	}
}

// DefaultMemoDefaults returns the fallback phrases.
func DefaultMemoDefaults() []string {
	return []string{
		"Remembering everything...",
		"Processing with postgres-grade memory...",
		"Two elephants, zero forgetfulness...",
		"The trunk is thinking...",
		"Mighty thoughts in progress...",
	}
}

// DefaultMemoActions returns tool-action-specific phrases.
func DefaultMemoActions() map[string][]string {
	return map[string][]string{
		"web_search": {
			"Trunk-extending into the web...",
			"Searching the savanna of the internet...",
			"Two elephants Googling...",
		},
		"read_file": {
			"Reading files with elephant attention...",
			"Scanning the code like an elephant scans the horizon...",
			"Never skimming — reading every line...",
		},
		"write_file": {
			"Writing with the precision of an elephant's trunk...",
			"Creating files that will be remembered forever...",
		},
		"terminal": {
			"Running commands with elephant power...",
			"Shell access granted. Tusks activated...",
		},
		"patch": {
			"Patching with surgical trunk precision...",
			"Editing like only an elephant can...",
		},
		"delegate_task": {
			"Sending out the elephant herd...",
			"Delegating across the savanna...",
			"Multiple elephants working in parallel...",
		},
		"memory": {
			"Committing to the eternal memory trunk...",
			"Two elephants storing this forever...",
		},
	}
}

// NewMemoEngine creates a MemoEngine with default rules.
// If db is non-nil, learned phrases persist to Postgres.
func NewMemoEngine(db *sql.DB) *MemoEngine {
	e := &MemoEngine{
		defaults: DefaultMemoDefaults(),
		actions:  DefaultMemoActions(),
		db:       db,
	}
	e.initRules()
	if db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		e.loadCustom(ctx)
	}
	return e
}

func (e *MemoEngine) initRules() {
	rules := DefaultMemoRules()
	e.rules = make([]compiledRule, 0, len(rules))
	for _, r := range rules {
		cr := compiledRule{category: r.Category, phrases: r.Phrases}
		cr.patterns = make([]*regexp.Regexp, 0, len(r.Patterns))
		for _, p := range r.Patterns {
			if re, err := regexp.Compile("(?i)" + p); err == nil {
				cr.patterns = append(cr.patterns, re)
			}
		}
		e.rules = append(e.rules, cr)
	}
}

// Match returns a context-aware phrase for the given user input.
func (e *MemoEngine) Match(input string) MemoPhraseResult {
	if e == nil {
		return MemoPhraseResult{Category: "default"}
	}
	e.mu.RLock()
	defer e.mu.RUnlock()

	// Check custom rules first (user/agent-learned).
	for _, cr := range e.custom {
		for _, re := range cr.patterns {
			if re.MatchString(input) {
				return MemoPhraseResult{
					Phrase:   pickMemo(cr.phrases),
					Category: cr.category,
				}
			}
		}
	}

	// Check built-in rules.
	for _, cr := range e.rules {
		for _, re := range cr.patterns {
			if re.MatchString(input) {
				return MemoPhraseResult{
					Phrase:   pickMemo(cr.phrases),
					Category: cr.category,
				}
			}
		}
	}

	// Default.
	return MemoPhraseResult{
		Phrase:   pickMemo(e.defaults),
		Category: "default",
	}
}

// ActionMatch returns a phrase for a tool-call action, or a generic one.
func (e *MemoEngine) ActionMatch(action string) MemoPhraseResult {
	if e == nil {
		return MemoPhraseResult{Category: "default"}
	}
	e.mu.RLock()
	defer e.mu.RUnlock()

	if phrases, ok := e.actions[action]; ok {
		return MemoPhraseResult{
			Phrase:   pickMemo(phrases),
			Category: "tool",
		}
	}
	return MemoPhraseResult{
		Phrase:   pickMemo(e.defaults),
		Category: "default",
	}
}

// Learn adds a new phrase rule. Called by the self-improvement loop
// when the agent discovers useful new patterns during its work.
func (e *MemoEngine) Learn(ctx context.Context, patterns []string, phrases []string, category string) error {
	if e == nil {
		return nil
	}
	if len(patterns) == 0 || len(phrases) == 0 {
		return nil
	}

	// Reject empty patterns/phrases — an empty pattern compiles to "(?i)"
	// which matches everything, and an empty phrase pollutes the rotation.
	for _, p := range patterns {
		if strings.TrimSpace(p) == "" {
			return fmt.Errorf("memo: empty pattern in Learn")
		}
	}
	for _, ph := range phrases {
		if strings.TrimSpace(ph) == "" {
			return fmt.Errorf("memo: empty phrase in Learn")
		}
	}

	cr := compiledRule{category: category, phrases: phrases}
	cr.patterns = make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile("(?i)" + p)
		if err != nil {
			continue
		}
		cr.patterns = append(cr.patterns, re)
	}
	if len(cr.patterns) == 0 {
		return nil
	}

	e.mu.Lock()
	e.custom = append(e.custom, cr)
	e.mu.Unlock()

	if e.db != nil {
		return e.persistCustom(ctx, cr)
	}
	return nil
}

// Rules returns all currently active rules (built-in + custom).
func (e *MemoEngine) Rules() []MemoPhraseRule {
	if e == nil {
		return nil
	}
	e.mu.RLock()
	defer e.mu.RUnlock()

	out := make([]MemoPhraseRule, 0, len(e.rules)+len(e.custom))
	for _, cr := range e.rules {
		out = append(out, ruleFromCompiled(cr))
	}
	for _, cr := range e.custom {
		out = append(out, ruleFromCompiled(cr))
	}
	return out
}

// AllPhrases returns every phrase (defaults + actions) for the TUI
// to use when no backend RPC is available.
func (e *MemoEngine) AllPhrases() map[string]interface{} {
	if e == nil {
		return nil
	}
	e.mu.RLock()
	defer e.mu.RUnlock()

	// Inline rule collection instead of calling e.Rules() to avoid
	// a recursive RLock deadlock (Go's RWMutex is not reentrant;
	// a pending writer between the two RLocks blocks forever).
	rules := make([]MemoPhraseRule, 0, len(e.rules)+len(e.custom))
	for _, cr := range e.rules {
		rules = append(rules, ruleFromCompiled(cr))
	}
	for _, cr := range e.custom {
		rules = append(rules, ruleFromCompiled(cr))
	}

	return map[string]interface{}{
		"defaults": e.defaults,
		"actions":  e.actions,
		"rules":    rules,
	}
}

// ── persistence ──

func (e *MemoEngine) persistCustom(ctx context.Context, cr compiledRule) error {
	patterns, _ := json.Marshal(uncompilePatterns(cr.patterns))
	phrases, _ := json.Marshal(cr.phrases)
	_, err := e.db.ExecContext(ctx,
		`INSERT INTO memo_phrases (category, patterns, phrases)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (category) DO UPDATE SET patterns=$2, phrases=$3, updated_at=NOW()`,
		cr.category, string(patterns), string(phrases),
	)
	return err
}

func (e *MemoEngine) loadCustom(ctx context.Context) {
	rows, err := e.db.QueryContext(ctx,
		`SELECT category, patterns, phrases FROM memo_phrases ORDER BY created_at`)
	if err != nil {
		return
	}
	defer rows.Close()

	e.mu.Lock()
	defer e.mu.Unlock()

	for rows.Next() {
		var category, patternsJSON, phrasesJSON string
		if err := rows.Scan(&category, &patternsJSON, &phrasesJSON); err != nil {
			continue
		}
		var patterns []string
		var phrases []string
		json.Unmarshal([]byte(patternsJSON), &patterns)
		json.Unmarshal([]byte(phrasesJSON), &phrases)

		cr := compiledRule{category: category, phrases: phrases}
		cr.patterns = make([]*regexp.Regexp, 0, len(patterns))
		for _, p := range patterns {
			if re, err := regexp.Compile("(?i)" + p); err == nil {
				cr.patterns = append(cr.patterns, re)
			}
		}
		e.custom = append(e.custom, cr)
	}
}

// ── helpers ──

func pickMemo(items []string) string {
	if len(items) == 0 {
		return "Processing..."
	}
	// Use crypto/rand for rotation instead of a deterministic hash of
	// the first item, which always returned the same index for fixed lists.
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Fall back to deterministic hash on crypto/rand failure.
		return items[hashString(items[0])%len(items)]
	}
	return items[binary.LittleEndian.Uint64(b[:])%uint64(len(items))]
}

func hashString(s string) int {
	h := 0
	for i := 0; i < len(s); i++ {
		h = h*31 + int(s[i])
	}
	if h < 0 {
		h = -h
	}
	return h
}

func ruleFromCompiled(cr compiledRule) MemoPhraseRule {
	return MemoPhraseRule{
		Patterns: uncompilePatterns(cr.patterns),
		Phrases:  cr.phrases,
		Category: cr.category,
	}
}

func uncompilePatterns(res []*regexp.Regexp) []string {
	out := make([]string, 0, len(res))
	for _, re := range res {
		s := re.String()
		out = append(out, strings.TrimPrefix(s, "(?i)"))
	}
	return out
}

// EnsureMemoTable creates the memo_phrases table if it doesn't exist.
func EnsureMemoTable(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS memo_phrases (
			id         SERIAL PRIMARY KEY,
			category   TEXT NOT NULL UNIQUE,
			patterns   TEXT NOT NULL DEFAULT '[]',
			phrases    TEXT NOT NULL DEFAULT '[]',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	return err
}
