// Package mcpshield — MCP tool-call policy layer (§8.4 MCPSHIELD,
// Acharya 2026).
//
// Threat: a malicious or compromised MCP server exposes tools that
// the agent then invokes with sensitive arguments. Even a benign
// MCP server might expose more capabilities than the user intends
// to grant.
//
// MCPSHIELD adds a layer between the agent's tool dispatcher and
// the MCP transport: each MCP server gets a Capability declaration,
// and tool calls are checked against it before forwarding. Unknown
// capabilities are denied. Per-call policies (path allow-lists,
// command allow-lists) gate specific tool invocations.
//
// What we DO build:
//
//   - Capability: a declaration of what an MCP server is allowed to
//     do. Tools, paths, max bytes per call.
//   - Policy: combine declarations with per-tool-call rules.
//   - Check(): returns Allow/Deny + reason. Agent dispatcher calls
//     Check before invoking the MCP tool.
//
// What we DO NOT build: the original paper's TEE-style attestation.
// We're a client process, not a hypervisor — capability declarations
// are operator-managed config, not cryptographically attested.
package mcpshield

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// Capability declares what one MCP server is allowed to do.
type Capability struct {
	// ServerName is the identifier the agent uses to address this
	// MCP server (e.g. "github", "filesystem", "browser").
	ServerName string `json:"server_name"`
	// AllowedTools is the explicit list of tool names this server
	// is permitted to expose. Empty list means "any tool" (use
	// only for fully-trusted servers).
	AllowedTools []string `json:"allowed_tools,omitempty"`
	// AllowedPathPrefixes restricts filesystem-shaped tool calls
	// to paths under one of these prefixes. Empty disables the
	// path check entirely.
	AllowedPathPrefixes []string `json:"allowed_path_prefixes,omitempty"`
	// MaxBytesPerCall caps the byte size of arguments passed to a
	// tool call. 0 means no cap.
	MaxBytesPerCall int `json:"max_bytes_per_call,omitempty"`
	// Trusted, when true, bypasses the unknown-tool check. Path
	// and byte limits still apply.
	Trusted bool `json:"trusted,omitempty"`
}

// Decision is the outcome of a Check.
type Decision struct {
	Allow  bool
	Reason string
}

// Policy holds capability declarations keyed by server name. Safe
// for concurrent use; tests + production share the same shape.
type Policy struct {
	mu    sync.RWMutex
	caps  map[string]Capability
}

// NewPolicy returns an empty policy. By default every server is
// untrusted: a tool call against a server with no capability
// declaration is denied.
func NewPolicy() *Policy {
	return &Policy{caps: map[string]Capability{}}
}

// Set installs or replaces the capability for serverName.
func (p *Policy) Set(c Capability) error {
	if strings.TrimSpace(c.ServerName) == "" {
		return errors.New("mcpshield: server name required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.caps[c.ServerName] = c
	return nil
}

// Get returns the capability for serverName, or (Capability{}, false)
// when none is declared.
func (p *Policy) Get(serverName string) (Capability, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	c, ok := p.caps[serverName]
	return c, ok
}

// Remove drops a capability declaration. Idempotent.
func (p *Policy) Remove(serverName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.caps, serverName)
}

// All returns every declared capability. Order is not guaranteed.
func (p *Policy) All() []Capability {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]Capability, 0, len(p.caps))
	for _, c := range p.caps {
		out = append(out, c)
	}
	return out
}

// CheckCall evaluates a tool invocation against the policy. The
// caller (the MCP dispatch layer) provides:
//
//   - serverName: which MCP server is being called.
//   - toolName: the specific tool on that server.
//   - paths: any filesystem paths referenced by the call (caller
//     extracts these from the tool's argument shape — different
//     tools have different schemas).
//   - argSize: total byte size of the serialized arguments.
//
// Returns Decision.Allow + Reason. Unknown server → deny. Unknown
// tool on a non-trusted server → deny. Path outside allow-list →
// deny. Args over the byte cap → deny.
func (p *Policy) CheckCall(serverName, toolName string, paths []string, argSize int) Decision {
	c, ok := p.Get(serverName)
	if !ok {
		return Decision{Reason: fmt.Sprintf("mcpshield: no capability declared for server %q", serverName)}
	}
	if !c.Trusted && len(c.AllowedTools) > 0 {
		found := false
		for _, t := range c.AllowedTools {
			if t == toolName {
				found = true
				break
			}
		}
		if !found {
			return Decision{Reason: fmt.Sprintf("mcpshield: tool %q not in allow-list for %q", toolName, serverName)}
		}
	}
	if c.MaxBytesPerCall > 0 && argSize > c.MaxBytesPerCall {
		return Decision{Reason: fmt.Sprintf("mcpshield: argument size %d exceeds cap %d for %q", argSize, c.MaxBytesPerCall, serverName)}
	}
	if len(c.AllowedPathPrefixes) > 0 {
		for _, raw := range paths {
			path := filepath.Clean(raw)
			if !anyPrefixMatch(path, c.AllowedPathPrefixes) {
				return Decision{Reason: fmt.Sprintf("mcpshield: path %q not under allow-listed prefixes for %q", path, serverName)}
			}
		}
	}
	return Decision{Allow: true, Reason: "allowed"}
}

// anyPrefixMatch returns true when path starts with any prefix.
// Prefix matching is filepath-aware so "/foo" matches "/foo/bar"
// but not "/foobar".
func anyPrefixMatch(path string, prefixes []string) bool {
	for _, p := range prefixes {
		clean := filepath.Clean(p)
		if path == clean || strings.HasPrefix(path, clean+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
