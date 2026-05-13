package checks

import (
	"context"
	"os/exec"

	"github.com/Sahaj-Tech-ltd/overkill/internal/doctor"
	"github.com/Sahaj-Tech-ltd/overkill/internal/lsp"
	"github.com/Sahaj-Tech-ltd/overkill/internal/mcp"
)

// RegisterMCP attempts a handshake against each configured MCP server.
// Per-server check so a single broken server is visible.
func RegisterMCP(r *doctor.Runner, d Deps) {
	if d.Cfg == nil || len(d.Cfg.MCP.Servers) == 0 {
		r.Register(doctor.SubsystemCheck{
			ID:       "mcp.none",
			Name:     "MCP servers",
			Category: doctor.CatBackend,
			Fn:       func(ctx context.Context) doctor.Result { return info("no MCP servers configured") },
		})
		return
	}
	for _, s := range d.Cfg.MCP.Servers {
		s := s
		r.Register(doctor.SubsystemCheck{
			ID:       "mcp." + s.Name,
			Name:     "MCP server: " + s.Name,
			Category: doctor.CatBackend,
			Parallel: true,
			Fn: func(ctx context.Context) doctor.Result {
				if !s.Enabled {
					return skip("disabled in config")
				}
				c := mcp.NewClient(s.Name, s.Command, s.Args, s.Env)
				if err := c.Start(ctx); err != nil {
					return failf("verify the MCP server command can run: "+s.Command,
						"start failed: %v", err)
				}
				defer c.Close()
				if _, err := c.ListTools(ctx); err != nil {
					return warnf("MCP server started but ListTools failed; check server logs",
						"ListTools: %v", err)
				}
				return okf("handshake ok")
			},
		})
	}
}

// commonLSPProbes is a small, opinionated list of language servers we check
// for on PATH. Detection is best-effort; we only spawn what's available.
var commonLSPProbes = []struct {
	Lang, Cmd string
}{
	{"go", "gopls"},
	{"typescript", "typescript-language-server"},
	{"python", "pyright"},
	{"rust", "rust-analyzer"},
}

// RegisterLSP attempts to spawn + initialize each detected language server.
// Missing-on-PATH is info, not warn — most users only have one or two.
func RegisterLSP(r *doctor.Runner, d Deps) {
	for _, p := range commonLSPProbes {
		p := p
		r.Register(doctor.SubsystemCheck{
			ID:       "lsp." + p.Lang,
			Name:     "LSP: " + p.Lang + " (" + p.Cmd + ")",
			Category: doctor.CatBackend,
			Parallel: true,
			Fn: func(ctx context.Context) doctor.Result {
				if _, err := exec.LookPath(p.Cmd); err != nil {
					return skip(p.Cmd + " not on PATH")
				}
				c := lsp.NewClient(p.Lang, p.Cmd, nil)
				if err := c.Start(ctx, "."); err != nil {
					return warnf("ensure "+p.Cmd+" works in this directory",
						"start failed: %v", err)
				}
				defer c.Close()
				return okf("initialized %s", p.Cmd)
			},
		})
	}
}
