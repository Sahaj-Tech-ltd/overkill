// Package tools — lsp_hover / lsp_definition / lsp_references / lsp_symbols
// expose the running LSP manager to the agent so it can query the language
// server for semantic info on the current codebase.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/lsp"
)

// LSPQuerier is the minimal surface the LSP tools need. Keeping it local to
// the tools package avoids a hard dep on *lsp.Manager and lets tests fake it.
//
// HoverClient / LocClient are tiny shims so we can return strongly-typed
// responses without leaking the full Client through the interface.
type LSPQuerier interface {
	// Available reports whether any language server is running and can handle
	// the given path. Tools short-circuit with a clear error otherwise.
	Available(path string) bool
	Hover(ctx context.Context, path string, line, col int) (HoverResult, error)
	Definition(ctx context.Context, path string, line, col int) ([]LocationResult, error)
	References(ctx context.Context, path string, line, col int) ([]LocationResult, error)
}

// HoverResult is the trimmed hover payload returned to the agent.
type HoverResult struct {
	Contents string `json:"contents"`
}

// LocationResult is a file/line/column triple returned for definition/references.
type LocationResult struct {
	File      string `json:"file"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	EndLine   int    `json:"end_line,omitempty"`
	EndColumn int    `json:"end_column,omitempty"`
}

// ---------- shared input + validation ----------

type lspPositionInput struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

// validatePath rejects obviously hostile or malformed file inputs. The LSP
// client is responsible for opening the file; we only ensure the path is sane.
func validatePath(p string) error {
	if p == "" {
		return fmt.Errorf("file is required")
	}
	if strings.ContainsAny(p, "\n\r") {
		return fmt.Errorf("file contains newline")
	}
	// reject inputs that start with an ASCII control character (excluding tab)
	if r := p[0]; r != '\t' && r < 0x20 {
		return fmt.Errorf("file starts with control character")
	}
	return nil
}

// ---------- lsp_hover ----------

// LSPHoverTool returns hover documentation at file:line:column.
type LSPHoverTool struct{ q LSPQuerier }

// NewLSPHoverTool wires an LSPQuerier into the tool.
func NewLSPHoverTool(q LSPQuerier) *LSPHoverTool { return &LSPHoverTool{q: q} }

// Name implements Tool.
func (t *LSPHoverTool) Name() string { return "lsp_hover" }

// Execute implements Tool.
func (t *LSPHoverTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.q == nil {
		return errorJSON("lsp not available"), nil
	}
	var req lspPositionInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("lsp_hover: %w", err)
	}
	if err := validatePath(req.File); err != nil {
		return errorJSON(err.Error()), nil
	}
	if !t.q.Available(req.File) {
		return errorJSON("no language server for " + req.File), nil
	}
	h, err := t.q.Hover(ctx, req.File, req.Line, req.Column)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	out, _ := json.Marshal(map[string]any{"contents": h.Contents})
	return out, nil
}

// ---------- lsp_definition ----------

// LSPDefinitionTool returns the definition site(s) of the symbol at a position.
type LSPDefinitionTool struct{ q LSPQuerier }

// NewLSPDefinitionTool wires an LSPQuerier into the tool.
func NewLSPDefinitionTool(q LSPQuerier) *LSPDefinitionTool { return &LSPDefinitionTool{q: q} }

// Name implements Tool.
func (t *LSPDefinitionTool) Name() string { return "lsp_definition" }

// Execute implements Tool.
func (t *LSPDefinitionTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.q == nil {
		return errorJSON("lsp not available"), nil
	}
	var req lspPositionInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("lsp_definition: %w", err)
	}
	if err := validatePath(req.File); err != nil {
		return errorJSON(err.Error()), nil
	}
	if !t.q.Available(req.File) {
		return errorJSON("no language server for " + req.File), nil
	}
	locs, err := t.q.Definition(ctx, req.File, req.Line, req.Column)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	out, _ := json.Marshal(map[string]any{"locations": locs, "count": len(locs)})
	return out, nil
}

// ---------- lsp_references ----------

// LSPReferencesTool returns usage sites of the symbol at a position.
type LSPReferencesTool struct{ q LSPQuerier }

// NewLSPReferencesTool wires an LSPQuerier into the tool.
func NewLSPReferencesTool(q LSPQuerier) *LSPReferencesTool { return &LSPReferencesTool{q: q} }

// Name implements Tool.
func (t *LSPReferencesTool) Name() string { return "lsp_references" }

// Execute implements Tool.
func (t *LSPReferencesTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.q == nil {
		return errorJSON("lsp not available"), nil
	}
	var req lspPositionInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("lsp_references: %w", err)
	}
	if err := validatePath(req.File); err != nil {
		return errorJSON(err.Error()), nil
	}
	if !t.q.Available(req.File) {
		return errorJSON("no language server for " + req.File), nil
	}
	locs, err := t.q.References(ctx, req.File, req.Line, req.Column)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	out, _ := json.Marshal(map[string]any{"locations": locs, "count": len(locs)})
	return out, nil
}

// ---------- lsp_symbols (kept for compatibility with existing registration) ----------

// LSPSymbolsTool returns the document outline for a file via the LSP manager.
// It still takes the concrete manager because document symbols isn't part of
// the minimal LSPQuerier surface — callers that don't have an *lsp.Manager
// can skip registering it.
type LSPSymbolsTool struct{ mgr *lsp.Manager }

// NewLSPSymbolsTool wires an *lsp.Manager into the tool.
func NewLSPSymbolsTool(mgr *lsp.Manager) *LSPSymbolsTool { return &LSPSymbolsTool{mgr: mgr} }

// Name implements Tool.
func (t *LSPSymbolsTool) Name() string { return "lsp_symbols" }

type lspSymbolsInput struct {
	File string `json:"file"`
}

// Execute implements Tool.
func (t *LSPSymbolsTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.mgr == nil {
		return errorJSON("lsp not available"), nil
	}
	var req lspSymbolsInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("lsp_symbols: %w", err)
	}
	if err := validatePath(req.File); err != nil {
		return errorJSON(err.Error()), nil
	}
	c := t.mgr.ClientForFile(req.File)
	if c == nil {
		return errorJSON("no language server for " + req.File), nil
	}
	syms, err := c.DocumentSymbols(ctx, req.File)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	out, _ := json.Marshal(map[string]any{"symbols": syms, "count": len(syms)})
	return out, nil
}
