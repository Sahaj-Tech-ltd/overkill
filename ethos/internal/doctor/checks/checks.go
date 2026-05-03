// Package checks bundles the individual subsystem checks that the Runner
// composes for `ethos doctor`. Each file in this package owns one related
// group of checks (config, providers, storage, etc.) and exposes a Register
// helper that the CLI wires together.
package checks

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Sahaj-Tech-ltd/ethos/internal/config"
	"github.com/Sahaj-Tech-ltd/ethos/internal/doctor"
)

// Deps is the bundle of objects checks need. Keeping them on a single struct
// lets the CLI build it once and pass it down rather than threading half a
// dozen positional arguments through every Register helper.
type Deps struct {
	Cfg       *config.Config
	ConfigDir string // typically ~/.ethos
	HTTP      *http.Client
	Now       func() time.Time
}

// DefaultDeps fills the optional fields with sensible defaults.
func DefaultDeps(cfg *config.Config, configDir string) Deps {
	return Deps{
		Cfg:       cfg,
		ConfigDir: configDir,
		HTTP:      &http.Client{Timeout: 4 * time.Second},
		Now:       time.Now,
	}
}

// RegisterAll wires every built-in subsystem check onto the runner. Order
// here mirrors the master plan §4.13 list so the rendered report reads in a
// predictable order.
func RegisterAll(r *doctor.Runner, d Deps) {
	RegisterConfig(r, d)
	RegisterProviders(r, d)
	RegisterStorage(r, d)
	RegisterCatalog(r, d)
	RegisterMCP(r, d)
	RegisterLSP(r, d)
	RegisterPlugins(r, d)
	RegisterSync(r, d)
	RegisterACP(r, d)
	RegisterTokenizer(r, d)
	RegisterTools(r, d)
	RegisterHooks(r, d)
	RegisterSkills(r, d)
	RegisterFilesystem(r, d)
	RegisterDisk(r, d)
	RegisterBridge(r, d)
	RegisterMemory(r, d)
	RegisterCellRenderer(r, d)
	RegisterAnimations(r, d)
	RegisterVersion(r, d)
}

// okf is a tiny constructor used across the checks package.
func okf(detail string, args ...any) doctor.Result {
	return doctor.Result{Status: doctor.SevOK, Detail: fmt.Sprintf(detail, args...)}
}

func warnf(fix string, detail string, args ...any) doctor.Result {
	return doctor.Result{Status: doctor.SevWarn, Detail: fmt.Sprintf(detail, args...), Fix: fix}
}

func failf(fix string, detail string, args ...any) doctor.Result {
	return doctor.Result{Status: doctor.SevFail, Detail: fmt.Sprintf(detail, args...), Fix: fix}
}

func info(detail string, args ...any) doctor.Result {
	return doctor.Result{Status: doctor.SevInfo, Detail: fmt.Sprintf(detail, args...)}
}

func skip(reason string) doctor.Result {
	return doctor.Result{Status: doctor.SevSkip, Detail: reason}
}

// withTimeout shrinks ctx to at most d. Useful inside checks that issue a
// network call — the runner already enforces a 5s budget but checks may want
// tighter inner timeouts.
func withTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, d)
}
