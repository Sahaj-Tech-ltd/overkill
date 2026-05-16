// Package walls — OVERKILL_ARCH.md + CONTEXT.md generation
// (master plan §6.5 Wall 2).
//
// Wall 2 already exists as ArchitectureWall (architecture.go) and gets
// surfaced as the wall_architecture agent tool. What was missing:
// the FILES the wall reads — OVERKILL_ARCH.md (canonical architecture
// reference) and CONTEXT.md (domain glossary). Without them the wall
// runs against an empty source of truth.
//
// This file generates seed versions of both on first boot of a project
// so the agent + Wall 2 have something to reason against. The user
// extends them as architecture evolves; agent tools (arch_*, glossary_*)
// let the agent append entries when it discovers new modules or terms.
//
// Generated files live in the PROJECT ROOT, not the agent's config dir.
// They're code-adjacent so they get committed alongside the source.
package walls

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/atomicfile"
	"github.com/Sahaj-Tech-ltd/overkill/internal/introspection"
)

const (
	// ArchFile is the canonical architecture reference (master plan §6.5).
	ArchFile = "OVERKILL_ARCH.md"
	// GlossaryFile is the domain vocabulary (CONTEXT.md per the plan).
	GlossaryFile = "CONTEXT.md"
)

// EnsureArch generates OVERKILL_ARCH.md at projectRoot if absent.
// Returns (created, error): created=true when we wrote a fresh file,
// false when one already existed. Non-fatal on any error — the wall
// keeps running without it.
func EnsureArch(projectRoot string) (bool, error) {
	if projectRoot == "" {
		return false, nil
	}
	path := filepath.Join(projectRoot, ArchFile)
	if _, err := os.Stat(path); err == nil {
		return false, nil // already exists
	}
	body, err := renderArchFromScan(projectRoot)
	if err != nil {
		return false, fmt.Errorf("walls: arch generate: %w", err)
	}
	if err := atomicfile.WriteFile(path, []byte(body), 0o644); err != nil {
		return false, fmt.Errorf("walls: arch write: %w", err)
	}
	return true, nil
}

// EnsureGlossary generates CONTEXT.md at projectRoot if absent.
// Returns (created, error) following the same convention as EnsureArch.
func EnsureGlossary(projectRoot string) (bool, error) {
	if projectRoot == "" {
		return false, nil
	}
	path := filepath.Join(projectRoot, GlossaryFile)
	if _, err := os.Stat(path); err == nil {
		return false, nil
	}
	body := renderGlossaryTemplate(filepath.Base(projectRoot))
	if err := atomicfile.WriteFile(path, []byte(body), 0o644); err != nil {
		return false, fmt.Errorf("walls: glossary write: %w", err)
	}
	return true, nil
}

// renderArchFromScan walks the project tree via the introspection
// scanner and produces a clean architecture-overview markdown:
//   - top-level packages with one-line purposes
//   - cross-module dependencies (high-level)
//   - the §6.5 deletion-test framing baked into the header
//   - placeholder sections (Layers, Invariants, ADRs) the user/agent
//     fills in as the project matures.
func renderArchFromScan(projectRoot string) (string, error) {
	res, err := introspection.WalkAndSummarize(projectRoot)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Architecture — %s\n", filepath.Base(projectRoot))
	fmt.Fprintf(&b, "_generated %s — extend as the project evolves_\n\n", time.Now().UTC().Format("2006-01-02"))

	b.WriteString("## Reading this file\n\n")
	b.WriteString("This is the **canonical architecture reference** for the project (master plan §6.5 Wall 2). ")
	b.WriteString("Wall 2 reads this file before non-trivial changes to flag drift. Skills that touch architecture write here. ")
	b.WriteString("Two principles inherited from the plan:\n\n")
	b.WriteString("1. **Deletion test** — when uncertain whether a module is earning its keep, imagine deleting it. ")
	b.WriteString("If the complexity vanishes, it was a pass-through. If it reappears across N callers, it was earning its keep.\n")
	b.WriteString("2. **One adapter = hypothetical seam, two adapters = real seam.** Don't engineer interfaces for single implementations.\n\n")

	// Top-level package manifests (go.mod, package.json, Cargo.toml).
	if len(res.Packages) > 0 {
		b.WriteString("## Package manifests\n\n")
		pkgs := make([]introspection.PackageSummary, len(res.Packages))
		copy(pkgs, res.Packages)
		sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Name < pkgs[j].Name })
		for _, p := range pkgs {
			label := p.NameField
			if label == "" {
				label = p.Name
			}
			fmt.Fprintf(&b, "- **`%s`** (%s) — %s\n", label, p.Stack, p.Manifest)
		}
		b.WriteString("\n")
	}

	// Top-level directory tree gives a quick orientation of where
	// code lives without expecting the reader to know the layout.
	if len(res.TopLevelTree) > 0 {
		b.WriteString("## Top-level layout\n\n```\n")
		for _, line := range res.TopLevelTree {
			fmt.Fprintf(&b, "%s\n", line)
		}
		b.WriteString("```\n\n")
	}

	b.WriteString("## Layers\n\n")
	b.WriteString("_Fill in once boundaries stabilise. Typical structure:_\n\n")
	b.WriteString("- **Edge** — CLI / TUI / gateways. User-facing surface.\n")
	b.WriteString("- **Orchestration** — agent loop, sub-agents, hooks.\n")
	b.WriteString("- **Domain** — providers, memory, skills, walls. The product logic.\n")
	b.WriteString("- **Infrastructure** — BadgerDB, bridge, journal. Persistence + IPC.\n\n")

	b.WriteString("## Invariants\n\n")
	b.WriteString("_Things that MUST hold across changes. Document each with a one-line test._\n\n")
	b.WriteString("- Example: \"The agent's history is owned by the Agent type — sub-agents take a snapshot, never a reference.\"\n\n")

	b.WriteString("## ADRs\n\n")
	b.WriteString("_Architectural Decision Records live in `docs/adr/`. ")
	b.WriteString("Per §6.5, only write an ADR when ALL THREE conditions hold:_\n\n")
	b.WriteString("1. The decision is **hard to reverse**.\n")
	b.WriteString("2. The decision is **surprising without context**.\n")
	b.WriteString("3. The decision is the result of a **real trade-off** (not the obvious choice).\n\n")

	b.WriteString("## Performance smells\n\n")
	b.WriteString("_Catalog of architectural anti-patterns observed in this codebase, with the canonical fix._\n\n")
	b.WriteString("- Example: \"Sync endpoint added to async layer — surface in next-turn warning.\"\n")

	return b.String(), nil
}

// renderGlossaryTemplate produces a starter CONTEXT.md the user/agent
// will extend. Format follows Matt Pocock's domain-glossary skill —
// term + one-line definition + (optional) example.
func renderGlossaryTemplate(projectName string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Glossary — %s\n", projectName)
	fmt.Fprintf(&b, "_generated %s — the canonical vocabulary for this project. ", time.Now().UTC().Format("2006-01-02"))
	b.WriteString("All skills read from it; all skills write to it when terms are established. ")
	b.WriteString("When you find yourself explaining a term mid-conversation, add it here._\n\n")

	b.WriteString("## How to use this file\n\n")
	b.WriteString("- **One term per entry.** Lowercase, hyphenated identifier.\n")
	b.WriteString("- **One-line definition.** Plain English. Avoid circular references.\n")
	b.WriteString("- **Example** (optional). A canonical use that pins the meaning.\n\n")

	b.WriteString("## Terms\n\n")
	b.WriteString("_The list grows as the project develops. Initial entries are placeholders — replace or extend._\n\n")

	b.WriteString("### `tracer-bullet-issue`\n\n")
	b.WriteString("A thin vertical slice that cuts through ALL architectural layers (schema → API → UI → tests) and is independently demoable. Contrasted with horizontal slices (one issue per layer).\n\n")
	b.WriteString("_Example: \"add `/auth/whoami` endpoint\" — touches handler, route registration, integration test, and (if surfaced) a UI element. One ticket._\n\n")

	b.WriteString("### `HITL` / `AFK`\n\n")
	b.WriteString("Issue classification. HITL (Human-In-The-Loop) requires human review before merge. AFK (Away-From-Keyboard) the agent can implement and merge independently.\n\n")

	b.WriteString("### `deletion-test`\n\n")
	b.WriteString("The check used to decide whether a module is earning its keep: imagine deleting it. If complexity vanishes, it was a pass-through. If complexity reappears across N callers, the module was real abstraction.\n\n")

	return b.String()
}

