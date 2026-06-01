// Package tools — Architecture diagram generator.
// Generates Mermaid diagrams from codebase structure descriptions.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// DiagramTool generates Mermaid architecture diagrams.
// The LLM calls this with a natural-language description of the architecture,
// and it returns a Mermaid diagram string + rendered HTML wrapper.
type DiagramTool struct {
	renderer DiagramRenderer
}

// DiagramRenderer converts Mermaid syntax to rendered output.
type DiagramRenderer interface {
	Render(mermaid string, title string) (svg string, err error)
}

// DiagramInput is the JSON input for the diagram tool.
type DiagramInput struct {
	Title       string             `json:"title"`       // Diagram title
	Type        string             `json:"type"`        // Mermaid diagram type: flowchart, sequenceDiagram, classDiagram, erDiagram, graph
	Description string             `json:"description"` // Natural language description of what to diagram
	Components  []DiagramComponent `json:"components,omitempty"`
}

// DiagramComponent describes one node in the architecture.
type DiagramComponent struct {
	Name        string   `json:"name"`
	Kind        string   `json:"kind"` // service, database, gateway, tool, external
	Description string   `json:"description"`
	DependsOn   []string `json:"depends_on"`
}

// NewDiagramTool creates a diagram generation tool.
func NewDiagramTool(renderer DiagramRenderer) *DiagramTool {
	if renderer == nil {
		renderer = &defaultDiagramRenderer{}
	}
	return &DiagramTool{renderer: renderer}
}

// Name returns the tool identifier.
func (t *DiagramTool) Name() string { return "diagram" }

// Description explains what the tool does.
func (t *DiagramTool) Description() string {
	return "Generate Mermaid architecture diagrams (flowchart, sequence, class, ERD). " +
		"Provide a title, diagram type, and description of the system. " +
		"Use this to visualize system architecture, data flow, component relationships, and deployment topology."
}

// Execute generates a Mermaid diagram from the input.
func (t *DiagramTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in DiagramInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("diagram: parse input: %w", err)
	}
	if in.Title == "" {
		in.Title = "Architecture Diagram"
	}
	if in.Type == "" {
		in.Type = "flowchart"
	}

	mermaid := generateMermaid(in)
	svg, err := t.renderer.Render(mermaid, in.Title)
	if err != nil {
		// Fallback: return the raw Mermaid syntax wrapped in an HTML block
		// so the caller can embed it directly.
		svg = wrapMermaidHTML(mermaid, in.Title)
	}

	result := map[string]string{
		"mermaid": mermaid,
		"svg":     svg,
		"title":   in.Title,
		"type":    in.Type,
	}
	out, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("diagram: marshal result: %w", err)
	}
	return out, nil
}

func (t *DiagramTool) Schema() json.RawMessage {
	schema := `{
		"type": "object",
		"properties": {
			"title": {"type": "string", "description": "Diagram title"},
			"type": {"type": "string", "enum": ["flowchart", "sequenceDiagram", "classDiagram", "erDiagram", "graph"], "description": "Mermaid diagram type"},
			"description": {"type": "string", "description": "Natural language description of the system architecture"},
			"components": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"name": {"type": "string"},
						"kind": {"type": "string", "enum": ["service", "database", "gateway", "tool", "external", "queue", "cache"]},
						"description": {"type": "string"},
						"depends_on": {"type": "array", "items": {"type": "string"}}
					}
				}
			}
		},
		"required": ["title", "type", "description"]
	}`
	return json.RawMessage(schema)
}

func generateMermaid(in DiagramInput) string {
	var b strings.Builder

	switch in.Type {
	case "sequenceDiagram":
		b.WriteString("sequenceDiagram\n")
		b.WriteString(fmt.Sprintf("    title %s\n", in.Title))
		for _, c := range in.Components {
			b.WriteString(fmt.Sprintf("    participant %s as %s\n", safeID(c.Name), c.Name))
		}
		for _, c := range in.Components {
			for _, dep := range c.DependsOn {
				b.WriteString(fmt.Sprintf("    %s->>%s: %s\n", safeID(dep), safeID(c.Name), c.Description))
			}
		}

	case "classDiagram":
		b.WriteString("classDiagram\n")
		for _, c := range in.Components {
			b.WriteString(fmt.Sprintf("    class %s {\n        +%s\n    }\n", safeID(c.Name), c.Kind))
		}
		for _, c := range in.Components {
			for _, dep := range c.DependsOn {
				b.WriteString(fmt.Sprintf("    %s --> %s\n", safeID(c.Name), safeID(dep)))
			}
		}

	case "erDiagram":
		b.WriteString("erDiagram\n")
		for _, c := range in.Components {
			for _, dep := range c.DependsOn {
				b.WriteString(fmt.Sprintf("    %s ||--o{ %s : \"%s\"\n", safeID(c.Name), safeID(dep), c.Description))
			}
		}

	default: // flowchart / graph
		b.WriteString("flowchart TB\n")
		// Group by kind
		groups := map[string][]DiagramComponent{}
		for _, c := range in.Components {
			groups[c.Kind] = append(groups[c.Kind], c)
		}
		for kind, comps := range groups {
			b.WriteString(fmt.Sprintf("    subgraph %s [%s]\n", safeID(kind), strings.Title(kind)+"s"))
			for _, c := range comps {
				icon := iconForKind(c.Kind)
				b.WriteString(fmt.Sprintf("        %s[\"%s %s\"]\n", safeID(c.Name), icon, c.Name))
			}
			b.WriteString("    end\n")
		}
		for _, c := range in.Components {
			for _, dep := range c.DependsOn {
				b.WriteString(fmt.Sprintf("    %s --> %s\n", safeID(dep), safeID(c.Name)))
			}
		}
	}

	return b.String()
}

func safeID(s string) string {
	// Replace characters that break Mermaid node IDs.
	r := strings.NewReplacer(
		" ", "_", "-", "_", ".", "_", "/", "_",
		"(", "_", ")", "_", "[", "_", "]", "_",
		":", "_", ",", "_", "'", "_", "\"", "_",
		"{", "_", "}", "_", "<", "_", ">", "_",
		"|", "_", "#", "_", "&", "_", ";", "_",
		"*", "_", "?", "_", "!", "_", "`", "_",
		"\\", "_", "%", "_", "=", "_",
	)
	result := r.Replace(strings.ToLower(s))
	// If everything was replaced, use a fallback.
	if len(strings.Trim(result, "_")) == 0 {
		return "node"
	}
	return result
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

// defaultDiagramRenderer produces an HTML wrapper that uses mermaid.js
// for client-side rendering. This works without any server-side deps.
type defaultDiagramRenderer struct{}

func (r *defaultDiagramRenderer) Render(mermaid string, title string) (string, error) {
	return wrapMermaidHTML(mermaid, title), nil
}

func wrapMermaidHTML(mermaid, title string) string {
	escaped := strings.ReplaceAll(mermaid, "`", "\\`")
	escaped = strings.ReplaceAll(escaped, "</", "<\\/")
	return fmt.Sprintf(`<div class="mermaid-diagram">
<div class="mermaid">
%s
</div>
<p class="diagram-caption">%s</p>
</div>
<script src="https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.min.js"></script>
<script>mermaid.initialize({startOnLoad:true, theme:'dark'});</script>`, escaped, title)
}
