// Package personality — baseline identity (master plan §4.16).
//
// The agent's own self-model. Loaded on EVERY boot regardless of
// personality level OR cold-start state. The cold-start protocol
// fills in the user-side relationship; the baseline identity fills
// in the agent-side voice. Without both, the agent on session 1
// is a tone-less form-filler.
//
// File resolution:
//
//   1. Power-user override at ~/.overkill/identity.toml (if exists)
//   2. Embedded default (always available, ships with the binary)
//
// Power-user override is NOT advertised. The default is the identity.
// We expose the override path so people who want to fork the voice
// can — but every new install gets the same Overkill personality.
package personality

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// defaultIdentityTOML is the baseline voice that ships with the
// binary. Loaded via go:embed so the agent always has a self-model
// to fall back on even when the user's home directory is read-only
// or the override file is missing.
//
//go:embed default_identity.toml
var defaultIdentityTOML []byte

// Identity is the agent's self-model. Four prose fields the model
// reads directly — not a structured schema. The file should feel
// like "who the agent is", not configuration.
type Identity struct {
	WhoIAm        string `toml:"who_i_am"`
	HowITalk      string `toml:"how_i_talk"`
	WhatIBelieve  string `toml:"what_i_believe"`
	SelfAwareness string `toml:"self_awareness"`
	Roastability  string `toml:"roastability"`
}

// identityFile mirrors the on-disk TOML shape with the [identity]
// table wrapper so the file structure matches what humans expect.
type identityFile struct {
	Identity Identity `toml:"identity"`
}

// LoadIdentity returns the active identity. Resolution order:
//
//   1. ~/.overkill/identity.toml (power-user override) if it exists
//      and parses cleanly
//   2. Embedded default
//
// A malformed override file is treated as if absent — we don't want
// a typo in the user's toml to leave the agent voiceless. The error
// (if any) is returned alongside the embedded default so callers can
// log it but keep operating.
func LoadIdentity() (*Identity, error) {
	// Try override first.
	if home, err := os.UserHomeDir(); err == nil {
		overridePath := filepath.Join(home, ".overkill", "identity.toml")
		if data, err := os.ReadFile(overridePath); err == nil {
			var doc identityFile
			if perr := toml.Unmarshal(data, &doc); perr == nil {
				return &doc.Identity, nil
			} else {
				// Fall through to embedded default but surface the
				// parse error so the caller can warn.
				def, _ := parseEmbeddedIdentity()
				return def, fmt.Errorf("identity: parse %s: %w (using embedded default)", overridePath, perr)
			}
		}
	}
	return parseEmbeddedIdentity()
}

// parseEmbeddedIdentity decodes the go:embed default. Returns an
// error only if the embedded file itself is malformed — which would
// be a build-time bug, never a runtime user error.
func parseEmbeddedIdentity() (*Identity, error) {
	var doc identityFile
	if err := toml.Unmarshal(defaultIdentityTOML, &doc); err != nil {
		return nil, fmt.Errorf("identity: embedded default unparseable: %w", err)
	}
	return &doc.Identity, nil
}

// SystemPromptBlock returns the identity content formatted for
// injection into the agent's system prompt. Single block, leading
// header so the model can find it when other directives are layered
// on. Trims each field so the source TOML's pretty multi-line
// quoting doesn't bleed extra blank lines into the prompt.
func (id *Identity) SystemPromptBlock() string {
	if id == nil {
		return ""
	}
	parts := []struct {
		header, body string
	}{
		{"Who I am", id.WhoIAm},
		{"How I talk", id.HowITalk},
		{"What I believe", id.WhatIBelieve},
		{"Self-awareness", id.SelfAwareness},
		{"Roastability", id.Roastability},
	}
	var b strings.Builder
	b.WriteString("Baseline identity (§4.16):\n")
	for _, p := range parts {
		body := strings.TrimSpace(p.body)
		if body == "" {
			continue
		}
		// Single-line per field for compactness — a sysprompt that's
		// 30% personality and 70% directives is wrong.
		body = strings.Join(strings.Fields(body), " ")
		b.WriteString("- ")
		b.WriteString(p.header)
		b.WriteString(": ")
		b.WriteString(body)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// Display returns the identity as a human-readable block for the
// /identity slash command. Differs from SystemPromptBlock in that
// it preserves the prose form (paragraph per field) instead of
// flattening to one-liners. The user sees the file the way the
// author wrote it.
func (id *Identity) Display() string {
	if id == nil {
		return "(no identity loaded)"
	}
	parts := []struct {
		header, body string
	}{
		{"Who I am", id.WhoIAm},
		{"How I talk", id.HowITalk},
		{"What I believe", id.WhatIBelieve},
		{"Self-awareness", id.SelfAwareness},
		{"Roastability", id.Roastability},
	}
	var b strings.Builder
	b.WriteString("Overkill — baseline identity\n")
	b.WriteString("─────────────────────────────\n\n")
	for _, p := range parts {
		body := strings.TrimSpace(p.body)
		if body == "" {
			continue
		}
		b.WriteString(p.header)
		b.WriteString("\n")
		b.WriteString(body)
		b.WriteString("\n\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
