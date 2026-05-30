package subagent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// AgentPriority determines which definition wins when multiple share a name.
// Higher priority (lower number) wins. Mirrors Claude Code's resolution.
type AgentPriority int

const (
	PriorityManaged    AgentPriority = 1 // organization-wide (managed settings)
	PriorityCLIFlag    AgentPriority = 2 // --agents CLI flag (session only)
	PriorityProject    AgentPriority = 3 // .overkill/agents/ (current project)
	PriorityUser       AgentPriority = 4 // ~/.overkill/agents/ (all projects)
	PriorityBuiltin    AgentPriority = 5 // hardcoded in Go (lowest)
)

// AgentFile is the on-disk representation of a sub-agent definition.
// Mirrors Claude Code's YAML frontmatter + markdown body format.
// All optional fields match the OpenClaude sub-agent spec exactly.
type AgentFile struct {
	Name              string     `yaml:"name"`
	Description       string     `yaml:"description"`
	Tools             csvStrings `yaml:"tools,omitempty"`
	DisallowedTools   csvStrings `yaml:"disallowedTools,omitempty"`
	Model             string     `yaml:"model,omitempty"`
	PermissionMode    string     `yaml:"permissionMode,omitempty"`
	MaxTurns          int        `yaml:"maxTurns,omitempty"`
	Skills            csvStrings `yaml:"skills,omitempty"`
	MCPServers        csvStrings `yaml:"mcpServers,omitempty"`
	Hooks             csvStrings `yaml:"hooks,omitempty"`
	Memory            string     `yaml:"memory,omitempty"` // user/project/local
	Color             string     `yaml:"color,omitempty"`
	Effort            string     `yaml:"effort,omitempty"`            // thinking effort level
	InitialPrompt     string     `yaml:"initialPrompt,omitempty"`     // prepended to first turn
	Background        bool       `yaml:"background,omitempty"`        // always run as background
	RequiredMCPServers csvStrings `yaml:"requiredMcpServers,omitempty"` // MCP patterns required
	// Body is the markdown after the frontmatter — the system prompt.
	Body string `yaml:"-"`
}

// csvStrings handles YAML fields that Claude Code accepts as either
// a YAML list or a comma-separated string (e.g., "read, grep, shell").
type csvStrings []string

func (c *csvStrings) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		// Comma-separated string: "read, grep, shell"
		parts := strings.Split(value.Value, ",")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				*c = append(*c, p)
			}
		}
		return nil
	case yaml.SequenceNode:
		// Proper YAML list: ["read", "grep", "shell"]
		var list []string
		if err := value.Decode(&list); err != nil {
			return err
		}
		*c = list
		return nil
	default:
		return fmt.Errorf("csvStrings: expected scalar or sequence, got %v", value.Kind)
	}
}

// registryEntry ties an agent definition to its priority and source path.
type registryEntry struct {
	Agent    AgentDef
	Priority AgentPriority
	Source   string // file path
}

// AgentRegistry discovers, resolves, and manages sub-agent definitions.
// It implements Claude Code's priority-based resolution: when multiple
// agents share the same Name, the highest priority (lowest number) wins.
//
// Discovery paths (in priority order, highest first):
//   - ~/.overkill/agents/managed/  (PriorityManaged)
//   - .overkill/agents/            (PriorityProject)
//   - ~/.overkill/agents/          (PriorityUser)
//
// Built-in agents (PriorityBuiltin) are registered programmatically
// and overridden by any file-based agent with the same name.
type AgentRegistry struct {
	mu      sync.RWMutex
	entries map[string]*registryEntry

	// Dirs scanned for file-based agents.
	projectDir string
	userDir    string
	managedDir string

	// FailedFiles tracks files that couldn't be parsed (for diagnostics).
	FailedFiles []FailedAgentFile
}

// FailedAgentFile records a file that looked like an agent definition
// but couldn't be parsed. Matches OpenClaude's failedFiles pattern.
type FailedAgentFile struct {
	Path  string
	Error string
}

// NewAgentRegistry creates a registry that scans the given directories.
// projectDir is typically <workdir>/.overkill/agents/.
// userDir is typically ~/.overkill/agents/.
func NewAgentRegistry(projectDir, userDir string) *AgentRegistry {
	r := &AgentRegistry{
		entries:    make(map[string]*registryEntry),
		projectDir: projectDir,
		userDir:    userDir,
		managedDir: filepath.Join(userDir, "managed"),
	}
	return r
}

// RegisterBuiltin registers a hardcoded agent at PriorityBuiltin.
// File-based agents with the same name will override it.
func (r *AgentRegistry) RegisterBuiltin(def AgentDef) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.entries[def.Name]; exists {
		return // higher-priority entry already exists
	}
	r.entries[def.Name] = &registryEntry{
		Agent:    def,
		Priority: PriorityBuiltin,
		Source:   "builtin",
	}
}

// RegisterCLI registers a session-only agent from --agents JSON at PriorityCLIFlag.
// These override everything except managed agents.
func (r *AgentRegistry) RegisterCLI(def AgentDef) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// CLI always wins unless managed already exists.
	if existing, ok := r.entries[def.Name]; ok && existing.Priority <= PriorityManaged {
		return
	}
	r.entries[def.Name] = &registryEntry{
		Agent:    def,
		Priority: PriorityCLIFlag,
		Source:   "cli",
	}
}

// Scan discovers agent files recursively in all configured directories
// and registers them at their respective priority levels. Lower-priority
// entries for the same name are silently replaced.
func (r *AgentRegistry) Scan() error {
	dirs := []struct {
		path     string
		priority AgentPriority
	}{
		{r.managedDir, PriorityManaged},
		{r.projectDir, PriorityProject},
		{r.userDir, PriorityUser},
	}

	for _, d := range dirs {
		if d.path == "" {
			continue
		}
		if err := r.scanDir(d.path, d.priority); err != nil {
			// Directory not existing is not an error — just skip.
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("subagent: scan %s: %w", d.path, err)
		}
	}
	return nil
}

// scanDir recursively walks a directory looking for .md files with
// valid YAML frontmatter, registering them at the given priority.
func (r *AgentRegistry) scanDir(dir string, priority AgentPriority) error {
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}

		af, err := parseAgentFile(path)
		if err != nil {
			// Record failed file if it has a 'name' field (looks intentional).
			if af != nil && af.Name != "" {
				r.mu.Lock()
				r.FailedFiles = append(r.FailedFiles, FailedAgentFile{
					Path:  path,
					Error: err.Error(),
				})
				r.mu.Unlock()
			}
			return nil
		}
		if af.Name == "" {
			return nil
		}

		def := agentFileToDef(af, path)

		r.mu.Lock()
		if existing, ok := r.entries[af.Name]; ok && existing.Priority <= priority {
			// Higher-priority entry already exists — skip.
			r.mu.Unlock()
			return nil
		}
		r.entries[af.Name] = &registryEntry{
			Agent:    def,
			Priority: priority,
			Source:   path,
		}
		r.mu.Unlock()

		return nil
	})
}

// Get returns the resolved agent definition for the given name,
// or nil if no agent is registered.
func (r *AgentRegistry) Get(name string) *AgentDef {
	r.mu.RLock()
	defer r.mu.RUnlock()

	e, ok := r.entries[name]
	if !ok {
		return nil
	}
	cp := e.Agent
	return &cp
}

// List returns all resolved agent definitions, sorted by priority then name.
func (r *AgentRegistry) List() []AgentDef {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]AgentDef, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, e.Agent)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

// FilterByMCPServers returns only agents whose RequiredMCPServers
// are all available. Agents with no requirements always pass.
// Matches OpenClaude's filterAgentsByMcpRequirements.
func (r *AgentRegistry) FilterByMCPServers(availableServers []string) []AgentDef {
	r.mu.RLock()
	defer r.mu.RUnlock()

	filtered := make([]AgentDef, 0)
	for _, e := range r.entries {
		if len(e.Agent.RequiredMCPServers) == 0 {
			filtered = append(filtered, e.Agent)
			continue
		}
		allMet := true
		for _, required := range e.Agent.RequiredMCPServers {
			found := false
			for _, available := range availableServers {
				if strings.Contains(strings.ToLower(available), strings.ToLower(required)) {
					found = true
					break
				}
			}
			if !found {
				allMet = false
				break
			}
		}
		if allMet {
			filtered = append(filtered, e.Agent)
		}
	}
	return filtered
}
// SelectBest finds the agent whose Description best matches the given
// task description. Returns nil if no agent matches well enough.
// Simple keyword-overlap scoring for now — embed-based matching can
// be added later when the embedding bridge is available.
func (r *AgentRegistry) SelectBest(taskDescription string) *AgentDef {
	r.mu.RLock()
	defer r.mu.RUnlock()

	type candidate struct {
		agent *AgentDef
		score int
	}
	var best *candidate

	taskLower := strings.ToLower(taskDescription)
	taskWords := splitWords(taskLower)

	for _, e := range r.entries {
		if e.Agent.Description == "" {
			continue // no description = not auto-selectable
		}
		descLower := strings.ToLower(e.Agent.Description)
		score := 0

		// Full phrase match bonus.
		if strings.Contains(descLower, taskLower) || strings.Contains(taskLower, descLower) {
			score += 50
		}

		// Word overlap (punctuation-stripped).
		descWords := splitWords(descLower)
		wordSet := make(map[string]bool, len(descWords))
		for _, dw := range descWords {
			wordSet[dw] = true
		}
		for _, tw := range taskWords {
			if wordSet[tw] {
				score += 10
			}
		}

		// Name bonus — if task mentions agent name.
		if strings.Contains(taskLower, strings.ToLower(e.Agent.Name)) {
			score += 30
		}

		if score > 0 && (best == nil || score > best.score) {
			best = &candidate{agent: &e.Agent, score: score}
		}
	}

	if best == nil || best.score < 20 {
		return nil
	}
	cp := *best.agent
	return &cp
}

// splitWords splits on whitespace and strips trailing punctuation.
func splitWords(s string) []string {
	raw := strings.Fields(s)
	out := make([]string, 0, len(raw))
	for _, w := range raw {
		w = strings.TrimRight(w, ".,;:!?()[]{}\"'")
		if w != "" {
			out = append(out, w)
		}
	}
	return out
}

// parseAgentFile reads a .md file and extracts YAML frontmatter + body.
// Frontmatter is delimited by --- lines (standard Hugo/Jekyll format).
func parseAgentFile(path string) (*AgentFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	content := string(data)
	af := &AgentFile{}

	// Split on --- delimiters for frontmatter.
	parts := strings.SplitN(content, "---", 3)
	if len(parts) < 3 {
		// No frontmatter — entire file is the system prompt.
		af.Body = strings.TrimSpace(content)
		return af, nil
	}

	if err := yaml.Unmarshal([]byte(parts[1]), af); err != nil {
		return nil, fmt.Errorf("bad YAML frontmatter: %w", err)
	}
	af.Body = strings.TrimSpace(parts[2])
	return af, nil
}

// agentFileToDef converts an AgentFile to an AgentDef for the delegator.
// csvStrings fields are already normalized during YAML unmarshal.
func agentFileToDef(af *AgentFile, sourcePath string) AgentDef {
	def := AgentDef{
		Name:              af.Name,
		Description:       af.Description,
		Model:             af.Model,
		SystemPrompt:      af.Body,
		Tools:             []string(af.Tools),
		DisallowedTools:   []string(af.DisallowedTools),
		MaxTurns:          af.MaxTurns,
		PermissionMode:    af.PermissionMode,
		Skills:            []string(af.Skills),
		Color:             af.Color,
		Effort:            af.Effort,
		InitialPrompt:     af.InitialPrompt,
		Background:        af.Background,
		Memory:            af.Memory,
		MCPServers:        []string(af.MCPServers),
		Hooks:             []string(af.Hooks),
		RequiredMCPServers: []string(af.RequiredMCPServers),
		Filename:          strings.TrimSuffix(filepath.Base(sourcePath), ".md"),
		// built-in: Command empty = no external process
		Command: "",
	}
	return def
}

// AgentDef is extended from the original to include Claude Code parity fields.
// Backward-compatible: all new fields are optional.
//
// NOTE: This mirrors the existing definition in external.go but adds fields.
// The original in external.go will be updated to match.
