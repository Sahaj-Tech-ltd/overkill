package checks

import (
	"context"
	"strings"

	"github.com/Sahaj-Tech-ltd/ethos/internal/doctor"
)

// RegisterConfig adds the config load + validation checks. Validation errors
// are fail; warnings are warn; clean is ok.
func RegisterConfig(r *doctor.Runner, d Deps) {
	r.Register(doctor.SubsystemCheck{
		ID:       "config.load",
		Name:     "Config load + validation",
		Category: doctor.CatCore,
		Fn: func(ctx context.Context) doctor.Result {
			if d.Cfg == nil {
				return failf("run `ethos setup` to create ~/.ethos/config.toml", "no config loaded")
			}
			errs := d.Cfg.Validate()
			warns := d.Cfg.Warnings()
			if len(errs) > 0 {
				msgs := make([]string, 0, len(errs))
				for _, e := range errs {
					msgs = append(msgs, e.Error())
				}
				return failf("run /config to fix the listed validation errors",
					"%d validation error(s): %s", len(errs), strings.Join(msgs, "; "))
			}
			if len(warns) > 0 {
				msgs := make([]string, 0, len(warns))
				for _, w := range warns {
					msgs = append(msgs, w.String())
				}
				return warnf("run /config to address: "+strings.Join(msgs, "; "),
					"%d warning(s)", len(warns))
			}
			return okf("config loads cleanly with %d providers", len(d.Cfg.Providers))
		},
	})
}
