// lsp_adapter.go — thin adapter mapping the concrete *lsp.Manager onto the
// LSPQuerier interface defined in internal/tools. Keeps the tools package
// from depending on the manager's public surface directly.
package main

import (
	"context"
	"path/filepath"

	"github.com/Sahaj-Tech-ltd/overkill/internal/lsp"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
)

// lspManagerAdapter implements tools.LSPQuerier on top of *lsp.Manager.
type lspManagerAdapter struct{ mgr *lsp.Manager }

func newLSPManagerAdapter(m *lsp.Manager) *lspManagerAdapter {
	return &lspManagerAdapter{mgr: m}
}

func (a *lspManagerAdapter) Available(path string) bool {
	if a == nil || a.mgr == nil {
		return false
	}
	return a.mgr.ClientForFile(path) != nil
}

func (a *lspManagerAdapter) Hover(ctx context.Context, path string, line, col int) (tools.HoverResult, error) {
	c := a.mgr.ClientForFile(path)
	if c == nil {
		return tools.HoverResult{}, nil
	}
	h, err := c.Hover(ctx, path, line, col)
	if err != nil {
		return tools.HoverResult{}, err
	}
	return tools.HoverResult{Contents: h.Contents}, nil
}

func (a *lspManagerAdapter) Definition(ctx context.Context, path string, line, col int) ([]tools.LocationResult, error) {
	c := a.mgr.ClientForFile(path)
	if c == nil {
		return nil, nil
	}
	locs, err := c.Definition(ctx, path, line, col)
	if err != nil {
		return nil, err
	}
	return convertLocations(locs), nil
}

func (a *lspManagerAdapter) References(ctx context.Context, path string, line, col int) ([]tools.LocationResult, error) {
	c := a.mgr.ClientForFile(path)
	if c == nil {
		return nil, nil
	}
	locs, err := c.References(ctx, path, line, col)
	if err != nil {
		return nil, err
	}
	return convertLocations(locs), nil
}

func convertLocations(in []lsp.Location) []tools.LocationResult {
	if len(in) == 0 {
		return nil
	}
	out := make([]tools.LocationResult, 0, len(in))
	for _, l := range in {
		out = append(out, tools.LocationResult{
			File:      cleanURI(l.URI),
			Line:      l.Range.Start.Line,
			Column:    l.Range.Start.Character,
			EndLine:   l.Range.End.Line,
			EndColumn: l.Range.End.Character,
		})
	}
	return out
}

func cleanURI(uri string) string {
	p := lsp.URIToPath(uri)
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}
