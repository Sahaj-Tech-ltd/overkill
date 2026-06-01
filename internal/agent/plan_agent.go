// Package agent — DeepPlanner: autonomous codebase analysis + plan generation.
//
// DeepPlanner runs a silent subagent that reads the codebase for 5-15 minutes,
// builds a mental model, and produces three artifacts:
//   - plan.md        — phased implementation plan for the build agent
//   - architecture.mmd — Mermaid architecture diagram
//   - plan.html       — DeepWiki-style rendered plan for human review
//
// Inspired by Amp Code's Oracle + Deep mode pattern: the planner "goes dark"
// while reading/analysing, then surfaces everything at once.
package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/pipeline"
)

// DeepPlanner orchestrates the plan-generation workflow.
type DeepPlanner struct {
	// agent is reserved for future subagent-based planning (spawning
	// specialist subagents for code search, dependency analysis, etc.).
	// Currently unused — the planner runs inline.
	agent *Agent
	// ProjectRoot is the directory to analyse.
	ProjectRoot string
	// Timeout bounds the entire planning run (default 15 min).
	Timeout time.Duration
	// ModelOverride pins a specific model for planning (empty = SmartRouter).
	ModelOverride string
}

// PlanOutput holds the artifacts produced by the planner.
type PlanOutput struct {
	PlanPath    string // ~/.overkill/plans/<name>.md
	HTMLPath    string // ~/.overkill/plans/<name>.html
	DiagramPath string // ~/.overkill/plans/<name>.mmd
	DiagramSVG  string // inline SVG or HTML wrapper
	Phases      []PlanPhase
	Title       string
}

// NewDeepPlanner creates a planner bound to an agent.
func NewDeepPlanner(a *Agent, projectRoot string) *DeepPlanner {
	return &DeepPlanner{
		agent:       a,
		ProjectRoot: projectRoot,
		Timeout:     15 * time.Minute,
	}
}

// Run executes the full deep-planning workflow.
// It reads the codebase, generates artifacts, and returns the output paths.
func (dp *DeepPlanner) Run(ctx context.Context) (*PlanOutput, error) {
	if dp.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, dp.Timeout)
		defer cancel()
	}

	start := time.Now()
	log.Printf("deepplan: starting analysis of %s (timeout=%v)", dp.ProjectRoot, dp.Timeout)

	// Phase 1: Codebase reconnaissance — build a project map.
	projectMap, err := dp.buildProjectMap()
	if err != nil {
		return nil, fmt.Errorf("deepplan: project map: %w", err)
	}

	// Phase 2: Generate the architecture diagram.
	diagram, err := dp.generateDiagram(projectMap)
	if err != nil {
		log.Printf("deepplan: diagram generation failed (non-fatal): %v", err)
		diagram = fallbackDiagram(projectMap)
	}

	// Phase 3: Generate the implementation plan (markdown).
	planMD, phases, err := dp.generatePlan(projectMap, diagram)
	if err != nil {
		return nil, fmt.Errorf("deepplan: plan generation: %w", err)
	}

	// Phase 4: Render DeepWiki HTML from the plan.
	output, err := dp.writeArtifacts(planMD, diagram, projectMap.Name)
	if err != nil {
		return nil, fmt.Errorf("deepplan: writing artifacts: %w", err)
	}
	output.Phases = phases
	output.Title = projectMap.Name

	elapsed := time.Since(start)
	log.Printf("deepplan: complete in %v — plan=%s, html=%s, diagram=%s",
		elapsed.Round(time.Second), output.PlanPath, output.HTMLPath, output.DiagramPath)

	return output, nil
}

// ProjectMap captures the codebase structure for planning.
type ProjectMap struct {
	Name        string
	Language    string   // go, typescript, python, rust, mixed
	Entrypoints []string // relative paths
	Directories []string
	KeyFiles    []string // AGENTS.md, go.mod, etc.
	Interfaces  []string // key interfaces found
	DepGraph    string   // human-readable dependency description
	FileTree    string   // tree output
}

func (dp *DeepPlanner) buildProjectMap() (*ProjectMap, error) {
	root := dp.ProjectRoot
	pm := &ProjectMap{
		Name: filepath.Base(root),
	}

	// Detect language.
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
		pm.Language = "go"
		pm.KeyFiles = append(pm.KeyFiles, "go.mod")
	} else if _, err := os.Stat(filepath.Join(root, "package.json")); err == nil {
		pm.Language = "typescript"
		pm.KeyFiles = append(pm.KeyFiles, "package.json")
	} else if _, err := os.Stat(filepath.Join(root, "pyproject.toml")); err == nil {
		pm.Language = "python"
		pm.KeyFiles = append(pm.KeyFiles, "pyproject.toml")
	} else {
		pm.Language = "unknown"
	}

	// Find AGENTS.md / CLAUDE.md.
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", "AGENT.md"} {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			pm.KeyFiles = append(pm.KeyFiles, name)
		}
	}

	// Walk the directory (depth-limited, no symlink following).
	pm.Directories, pm.Entrypoints, pm.FileTree = walkProject(root, pm.Language, 4)

	// Build dep graph description (with size limit).
	pm.DepGraph = describeDependencies(root, pm.Language)

	// Find key interfaces (depth-limited, no symlink following).
	pm.Interfaces = findInterfaces(root, pm.Language, 3)

	return pm, nil
}

func walkProject(root string, lang string, maxDepth int) (dirs []string, entrypoints []string, tree string) {
	var treeBuilder strings.Builder
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		// Resolve symlinks to prevent traversal outside project root.
		resolved, evalErr := filepath.EvalSymlinks(path)
		if evalErr == nil && !strings.HasPrefix(resolved, root) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		rel, _ := filepath.Rel(root, path)
		if rel == "." {
			return nil
		}

		// Enforce max depth.
		depth := strings.Count(rel, string(filepath.Separator))
		if depth >= maxDepth {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip hidden dirs and vendor/node_modules.
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") || d.Name() == "vendor" ||
				d.Name() == "node_modules" || d.Name() == "__pycache__" ||
				d.Name() == ".git" {
				return filepath.SkipDir
			}
			dirs = append(dirs, rel)
			treeBuilder.WriteString(fmt.Sprintf("%s/\n", rel))
			return nil
		}

		// Detect entrypoints.
		switch lang {
		case "go":
			if strings.HasSuffix(d.Name(), ".go") && strings.Contains(path, "cmd/") && d.Name() == "main.go" {
				entrypoints = append(entrypoints, rel)
			}
		case "typescript":
			if strings.HasSuffix(d.Name(), ".tsx") && (strings.Contains(rel, "app.") || strings.Contains(rel, "index.")) {
				entrypoints = append(entrypoints, rel)
			}
		case "python":
			if d.Name() == "__main__.py" || d.Name() == "main.py" || d.Name() == "server.py" {
				entrypoints = append(entrypoints, rel)
			}
		}

		treeBuilder.WriteString(fmt.Sprintf("  %s\n", rel))
		return nil
	})
	return dirs, entrypoints, treeBuilder.String()
}

// maxDepFileSize limits how much of a dependency file we read.
const maxDepFileSize = 64 * 1024 // 64KB

func describeDependencies(root string, lang string) string {
	switch lang {
	case "go":
		modPath := filepath.Join(root, "go.mod")
		if data, err := os.ReadFile(modPath); err == nil {
			if len(data) > maxDepFileSize {
				data = data[:maxDepFileSize]
			}
			return fmt.Sprintf("Go module dependencies (from go.mod):\n%s", string(data))
		}
	case "typescript":
		pkgPath := filepath.Join(root, "package.json")
		if data, err := os.ReadFile(pkgPath); err == nil {
			if len(data) > maxDepFileSize {
				data = data[:maxDepFileSize]
			}
			return fmt.Sprintf("Node dependencies (from package.json):\n%s", string(data))
		}
	case "python":
		for _, name := range []string{"pyproject.toml", "requirements.txt", "setup.py"} {
			p := filepath.Join(root, name)
			if data, err := os.ReadFile(p); err == nil {
				if len(data) > maxDepFileSize {
					data = data[:maxDepFileSize]
				}
				return fmt.Sprintf("Python dependencies (from %s):\n%s", name, string(data))
			}
		}
	}
	return "No dependency file found."
}

func findInterfaces(root string, lang string, maxDepth int) []string {
	if lang != "go" {
		return nil
	}

	var ifaces []string
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}

		// Enforce depth limit.
		rel, _ := filepath.Rel(root, path)
		if depth := strings.Count(rel, string(filepath.Separator)); depth >= maxDepth {
			return nil
		}

		// Skip symlinks outside root.
		resolved, evalErr := filepath.EvalSymlinks(path)
		if evalErr == nil && !strings.HasPrefix(resolved, root) {
			return nil
		}

		// Skip hidden / vendor dirs.
		for _, part := range strings.Split(filepath.Dir(rel), string(filepath.Separator)) {
			if strings.HasPrefix(part, ".") || part == "vendor" {
				return nil
			}
		}

		data, err := os.ReadFile(path)
		if err != nil || len(data) > 100000 {
			return nil
		}
		content := string(data)
		if strings.Contains(content, "type ") && strings.Contains(content, "interface {") {
			ifaces = append(ifaces, fmt.Sprintf("file: %s", rel))
		}
		return nil
	})
	return ifaces
}

// Local diagram types — duplicated from tools to avoid circular import
// (tools package imports agent).
type plannerDiagramInput struct {
	Title       string
	Type        string
	Description string
	Components  []plannerDiagramComponent
}

type plannerDiagramComponent struct {
	Name        string
	Kind        string
	Description string
	DependsOn   []string
}

func (dp *DeepPlanner) generateDiagram(pm *ProjectMap) (string, error) {
	components := buildComponents(pm)
	if len(components) == 0 {
		return "", fmt.Errorf("no components found")
	}

	in := plannerDiagramInput{
		Title:      pm.Name + " Architecture",
		Type:       "flowchart",
		Components: components,
	}

	mermaid := generateMermaidStatic(in)
	return mermaid, nil
}

// maxPlanPhases caps the number of auto-generated implementation phases.
const maxPlanPhases = 20

func buildComponents(pm *ProjectMap) []plannerDiagramComponent {
	// Use a map for O(1) dedup instead of O(n²) linear scan.
	seen := map[string]bool{}
	var comps []plannerDiagramComponent

	// Pre-compute the directories string once (was O(n×m) double-join).
	dirsStr := strings.Join(pm.Directories, ", ")
	if len(dirsStr) > 100 {
		dirsStr = dirsStr[:100]
	}

	for _, dir := range pm.Directories {
		parts := strings.SplitN(dir, "/", 2)
		name := parts[0]
		if len(parts) <= 1 || strings.Contains(name, ".") {
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true

		kind := classifyComponent(name)
		comps = append(comps, plannerDiagramComponent{
			Name:        name,
			Kind:        kind,
			Description: dirsStr,
		})
	}
	return comps
}

func classifyComponent(name string) string {
	switch {
	case strings.Contains(name, "gateway") || strings.Contains(name, "api"):
		return "gateway"
	case strings.Contains(name, "db") || strings.Contains(name, "database") || strings.Contains(name, "store"):
		return "database"
	case strings.Contains(name, "tool"):
		return "tool"
	case strings.Contains(name, "cache") || strings.Contains(name, "redis"):
		return "cache"
	case strings.Contains(name, "queue") || strings.Contains(name, "daemon") || strings.Contains(name, "cron"):
		return "queue"
	case strings.Contains(name, "bridge") || strings.Contains(name, "sdk"):
		return "external"
	default:
		return "service"
	}
}

func generateMermaidStatic(in plannerDiagramInput) string {
	var b strings.Builder
	b.WriteString("flowchart TB\n")
	for i, c := range in.Components {
		icon := iconForKind(c.Kind)
		b.WriteString(fmt.Sprintf("    N%d[\"%s %s\"]\n", i, icon, mermaidSafeText(c.Name)))
	}
	// Draw edges from dependencies where available, otherwise linear.
	hasDeps := false
	for _, c := range in.Components {
		if len(c.DependsOn) > 0 {
			hasDeps = true
			break
		}
	}
	if hasDeps {
		for i, c := range in.Components {
			for _, dep := range c.DependsOn {
				for j, other := range in.Components {
					if other.Name == dep {
						b.WriteString(fmt.Sprintf("    N%d --> N%d\n", j, i))
					}
				}
			}
		}
	} else {
		for i := 1; i < len(in.Components); i++ {
			b.WriteString(fmt.Sprintf("    N%d --> N%d\n", i-1, i))
		}
	}
	return b.String()
}

// mermaidSafeText escapes text that appears inside Mermaid node labels.
func mermaidSafeText(s string) string {
	s = strings.ReplaceAll(s, "\"", "'")
	s = strings.ReplaceAll(s, "[", "(")
	s = strings.ReplaceAll(s, "]", ")")
	s = strings.ReplaceAll(s, "{", "(")
	s = strings.ReplaceAll(s, "}", ")")
	s = strings.ReplaceAll(s, "<", "")
	s = strings.ReplaceAll(s, ">", "")
	return s
}

func (dp *DeepPlanner) generatePlan(pm *ProjectMap, diagram string) (string, []PlanPhase, error) {
	var b strings.Builder
	title := pm.Name + " Implementation Plan"
	b.WriteString(fmt.Sprintf("# %s\n\n", title))
	b.WriteString(fmt.Sprintf("**Generated:** %s  \n", time.Now().UTC().Format("2006-01-02 15:04 UTC")))
	b.WriteString(fmt.Sprintf("**Language:** %s  \n", pm.Language))
	b.WriteString(fmt.Sprintf("**Root:** `%s`  \n\n", dp.ProjectRoot))

	b.WriteString("## Architecture Diagram\n\n")
	b.WriteString("```mermaid\n")
	b.WriteString(diagram)
	b.WriteString("\n```\n\n")

	b.WriteString("## Project Structure\n\n")
	b.WriteString("```\n")
	b.WriteString(pm.FileTree)
	b.WriteString("\n```\n\n")

	b.WriteString("## Entrypoints\n\n")
	for _, ep := range pm.Entrypoints {
		b.WriteString(fmt.Sprintf("- `%s`\n", ep))
	}
	b.WriteString("\n")

	b.WriteString("## Key Files\n\n")
	for _, kf := range pm.KeyFiles {
		b.WriteString(fmt.Sprintf("- `%s`\n", kf))
	}
	b.WriteString("\n")

	if len(pm.Interfaces) > 0 {
		b.WriteString("## Key Interfaces\n\n")
		for _, iface := range pm.Interfaces {
			b.WriteString(fmt.Sprintf("- %s\n", iface))
		}
		b.WriteString("\n")
	}

	b.WriteString("## Dependencies\n\n")
	b.WriteString("```\n")
	b.WriteString(pm.DepGraph)
	b.WriteString("\n```\n\n")

	// Auto-generate phases from the project structure (capped).
	var phases []PlanPhase
	phaseIdx := 1

	phases = append(phases, PlanPhase{
		Index:       phaseIdx,
		Title:       "Codebase Reconnaissance",
		Description: "Read key files (" + strings.Join(pm.KeyFiles, ", ") + ") and entrypoints (" + strings.Join(pm.Entrypoints, ", ") + ") to understand the existing architecture, interfaces, and conventions.",
		Status:      PhasePending,
	})
	phaseIdx++

	phases = append(phases, PlanPhase{
		Index:       phaseIdx,
		Title:       "Architecture Review",
		Description: "Review the generated architecture diagram with the user. Confirm component boundaries, data flow, and interfaces match expectations.",
		Status:      PhasePending,
	})
	phaseIdx++

	// Group directories by top-level component for fewer phases.
	seen := map[string]bool{}
	for _, dir := range pm.Directories {
		parts := strings.SplitN(dir, "/", 2)
		name := parts[0]
		if seen[name] || strings.Contains(name, ".") || len(parts) <= 1 {
			continue
		}
		seen[name] = true

		phases = append(phases, PlanPhase{
			Index:       phaseIdx,
			Title:       "Implement: " + name,
			Description: fmt.Sprintf("Build the %s package/directory. Define interfaces, write implementation, add tests.", name),
			Status:      PhasePending,
		})
		phaseIdx++
		if phaseIdx > maxPlanPhases {
			break
		}
	}

	b.WriteString("## Implementation Phases\n\n")
	for _, p := range phases {
		b.WriteString(fmt.Sprintf("### Phase %d: %s\n\n", p.Index, p.Title))
		b.WriteString(fmt.Sprintf("%s\n\n", p.Description))
	}

	return b.String(), phases, nil
}

func (dp *DeepPlanner) writeArtifacts(planMD string, diagram string, name string) (*PlanOutput, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("home dir: %w", err)
	}
	plansDir := filepath.Join(home, ".overkill", "plans")
	if err := os.MkdirAll(plansDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir plans: %w", err)
	}

	output := &PlanOutput{}

	planPath := filepath.Join(plansDir, name+".md")
	if err := os.WriteFile(planPath, []byte(planMD), 0o644); err != nil {
		return nil, fmt.Errorf("write plan: %w", err)
	}
	output.PlanPath = planPath

	diagramPath := filepath.Join(plansDir, name+".mmd")
	if err := os.WriteFile(diagramPath, []byte(diagram), 0o644); err != nil {
		return nil, fmt.Errorf("write diagram: %w", err)
	}
	output.DiagramPath = diagramPath
	output.DiagramSVG = diagram

	htmlPath, err := pipeline.RenderPlanToFile([]byte(planMD), pipeline.RenderConfig{
		Title:          name + " — Deep Plan",
		Name:           name,
		DiagramMermaid: diagram,
	})
	if err != nil {
		log.Printf("deepplan: html render failed (non-fatal): %v", err)
	} else {
		output.HTMLPath = htmlPath
	}

	return output, nil
}

func fallbackDiagram(pm *ProjectMap) string {
	var b strings.Builder
	b.WriteString("flowchart TB\n")
	b.WriteString(fmt.Sprintf("    title %s Architecture\n", mermaidSafeText(pm.Name)))
	limit := min(10, len(pm.Directories))
	for i := 0; i < limit; i++ {
		b.WriteString(fmt.Sprintf("    N%d[%s]\n", i, mermaidSafeText(pm.Directories[i])))
	}
	return b.String()
}

func iconForKind(kind string) string {
	switch kind {
	case "service":
		return "⚙️"
	case "database":
		return "🗄️"
	case "gateway":
		return "🌐"
	case "tool":
		return "🔧"
	case "external":
		return "☁️"
	case "queue":
		return "📮"
	case "cache":
		return "⚡"
	default:
		return "📦"
	}
}
