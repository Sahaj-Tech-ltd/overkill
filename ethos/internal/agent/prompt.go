package agent

import (
	"fmt"
	"strings"

	"github.com/Sahaj-Tech-ltd/ethos/internal/tools"
)

func BuildSystemPrompt(base string, registry *tools.Registry) string {
	var b strings.Builder

	if base != "" {
		b.WriteString(base)
		b.WriteString("\n\n")
	}

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
