package checks

import (
	"context"
	"fmt"

	"github.com/Sahaj-Tech-ltd/overkill/internal/doctor"
	"github.com/Sahaj-Tech-ltd/overkill/internal/plugin"
)

// noopBridge satisfies plugin.HostBridge for the doctor handshake — we don't
// need real session state to confirm a plugin starts and responds.
type noopBridge struct{}

func (noopBridge) SessionInfo() plugin.SessionInfo { return plugin.SessionInfo{ID: "doctor"} }
func (noopBridge) ConfigValue(string) (any, bool)  { return nil, false }
func (noopBridge) Toast(string, string)            {}

// RegisterPlugins runs the doctor handshake (mirrors `overkill plugin doctor`)
// against every plugin discovered under ~/.overkill/plugins. We deliberately
// reuse the public discovery + start path rather than re-implement it.
func RegisterPlugins(r *doctor.Runner, d Deps) {
	root := plugin.DefaultPluginsDir()
	if d.Cfg != nil && d.Cfg.Plugins.Dir != "" {
		root = d.Cfg.Plugins.Dir
	}
	discovered, err := plugin.Discover(root)
	if err != nil {
		r.Register(doctor.SubsystemCheck{
			ID:       "plugins.discover",
			Name:     "Plugin discovery",
			Category: doctor.CatPlugin,
			Fn: func(ctx context.Context) doctor.Result {
				return failf("inspect "+root+" — directory may be unreadable",
					"discover: %v", err)
			},
		})
		return
	}
	if len(discovered) == 0 {
		r.Register(doctor.SubsystemCheck{
			ID:       "plugins.none",
			Name:     "Plugins",
			Category: doctor.CatPlugin,
			Fn: func(ctx context.Context) doctor.Result {
				return info("%s", "no plugins installed under "+root)
			},
		})
		return
	}
	for _, p := range discovered {
		p := p
		r.Register(doctor.SubsystemCheck{
			ID:       "plugin." + p.Name,
			Name:     "Plugin: " + p.Name,
			Category: doctor.CatPlugin,
			Parallel: true,
			Fn: func(ctx context.Context) doctor.Result {
				c := plugin.NewClient(p.Name, p.EntryPath, p.EntryArgs, p.Env, noopBridge{})
				if p.StaticManifest != nil {
					c.SetStaticManifest(*p.StaticManifest)
				}
				if err := c.Start(ctx); err != nil {
					return failf("run `overkill plugin doctor "+p.Name+"` for details",
						"start: %v", err)
				}
				if ctx.Err() != nil {
					return failf("re-run doctor with a longer timeout",
						"context cancelled before shutdown: %v", ctx.Err())
				}
				_ = c.Shutdown(ctx)
				m := c.Manifest()
				return okf("handshake ok (%s)", fmt.Sprintf("v%s", m.Version))
			},
		})
	}
}
