package agent

import (
	"fmt"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
)

func BuildSystemPrompt(base string, registry *tools.Registry) string {
	var b strings.Builder

	if base != "" {
		b.WriteString(base)
		b.WriteString("\n\n")
	}

	// §4.4 token-discipline directives. Lightweight enough to ship
	// in every system prompt — the savings on token-bloat tool calls
	// dwarf the few-hundred-character cost.
	b.WriteString(tokenDisciplineDirective)
	b.WriteString("\n\n")

	if registry == nil {
		return strings.TrimSpace(b.String())
	}

	toolNames := registry.List()
	if len(toolNames) == 0 {
		return strings.TrimSpace(b.String())
	}

	b.WriteString("Available tools:\n")
	for i, name := range toolNames {
		t, _ := registry.Get(name)
		if t != nil {
			b.WriteString(fmt.Sprintf("%d. %s\n", i+1, t.Name()))
		}
	}

	return strings.TrimSpace(b.String())
}

// tokenDisciplineDirective enforces §4.4 "Mine Context First" and
// "Grep-n navigation" as a system-prompt rule. The agent has full
// agency to deviate — this is a directive, not a hard gate. The gate
// lives in the fs.read tool (§4.4 large-file path).
const tokenDisciplineDirective = `Token discipline (§4.4):
- Mine context first. Before any tool call, check whether prior messages, the system prompt, or the memory recall already contain the answer. Tool calls cost tokens; re-reading recall is free.
- Grep before read. Prefer grep / search tools with line numbers over whole-file reads. When you must read a file, supply Offset + Limit unless you've already searched and know the file is small (<200 lines).
- Don't paste large files back to the user — summarise. The user can read the file themselves; your job is to extract the relevant slice.`
