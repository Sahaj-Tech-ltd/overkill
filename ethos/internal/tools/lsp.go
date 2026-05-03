package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Sahaj-Tech-ltd/ethos/internal/lsp"
)

// LSPTools wraps an lsp.Manager so the agent can query language servers
// directly. Each method becomes a separate tool.

type lspBaseInput struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
}

type lspDefinitionTool struct{ mgr *lsp.Manager }

func NewLSPDefinitionTool(mgr *lsp.Manager) *lspDefinitionTool {
	return &lspDefinitionTool{mgr: mgr}
}
func (t *lspDefinitionTool) Name() string { return "lsp_definition" }
func (t *lspDefinitionTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in lspBaseInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	c := t.mgr.ClientForFile(in.Path)
	if c == nil {
		return json.Marshal(map[string]any{"error": "no language server for " + in.Path})
	}
	locs, err := c.Definition(ctx, in.Path, in.Line, in.Col)
	if err != nil {
		return nil, fmt.Errorf("lsp_definition: %w", err)
	}
	return json.Marshal(map[string]any{"locations": locs})
}

type lspReferencesTool struct{ mgr *lsp.Manager }

func NewLSPReferencesTool(mgr *lsp.Manager) *lspReferencesTool {
	return &lspReferencesTool{mgr: mgr}
}
func (t *lspReferencesTool) Name() string { return "lsp_references" }
func (t *lspReferencesTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in lspBaseInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	c := t.mgr.ClientForFile(in.Path)
	if c == nil {
		return json.Marshal(map[string]any{"error": "no language server for " + in.Path})
	}
	locs, err := c.References(ctx, in.Path, in.Line, in.Col)
	if err != nil {
		return nil, fmt.Errorf("lsp_references: %w", err)
	}
	return json.Marshal(map[string]any{"locations": locs})
}

type lspHoverTool struct{ mgr *lsp.Manager }

func NewLSPHoverTool(mgr *lsp.Manager) *lspHoverTool { return &lspHoverTool{mgr: mgr} }
func (t *lspHoverTool) Name() string                 { return "lsp_hover" }
func (t *lspHoverTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in lspBaseInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	c := t.mgr.ClientForFile(in.Path)
	if c == nil {
		return json.Marshal(map[string]any{"error": "no language server for " + in.Path})
	}
	h, err := c.Hover(ctx, in.Path, in.Line, in.Col)
	if err != nil {
		return nil, fmt.Errorf("lsp_hover: %w", err)
	}
	return json.Marshal(map[string]any{"contents": h.Contents})
}

type lspSymbolsInput struct {
	Path  string `json:"path"`
	Query string `json:"query"`
}

type lspSymbolsTool struct{ mgr *lsp.Manager }

func NewLSPSymbolsTool(mgr *lsp.Manager) *lspSymbolsTool { return &lspSymbolsTool{mgr: mgr} }
func (t *lspSymbolsTool) Name() string                   { return "lsp_symbols" }
func (t *lspSymbolsTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in lspSymbolsInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	if in.Path != "" {
		c := t.mgr.ClientForFile(in.Path)
		if c == nil {
			return json.Marshal(map[string]any{"error": "no language server for " + in.Path})
		}
		syms, err := c.DocumentSymbols(ctx, in.Path)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"symbols": syms})
	}
	// Workspace search — try every connected client and merge results.
	if t.mgr == nil {
		return json.Marshal(map[string]any{"symbols": []any{}})
	}
	var all []lsp.SymbolInformation
	for _, lang := range t.mgr.Languages() {
		_ = lang
		// We don't expose per-language clients publicly; fall back to picking
		// a connected client via ClientForFile with a synthetic extension.
		// Skip for now if no targeted client; the per-file path covers most
		// real use.
	}
	return json.Marshal(map[string]any{"symbols": all})
}
