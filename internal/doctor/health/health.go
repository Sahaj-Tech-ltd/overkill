// Package health provides the HealthCheck registry — the single best pattern
// stolen from OpenClaw's src/flows/. Every health check can optionally repair
// what it finds, and the framework re-runs detect() after repair() to verify
// the fix took. Plugin-owned checks live in plugins, not core. Registered
// globally, loaded lazily. Maps perfectly to Go interfaces.
//
// This package extends the existing doctor subsystem (runner.go, doctor.go,
// checks/) with the detect→repair→validate loop. It does NOT replace the
// existing Runner — it is a compatible superset.
package health

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"golang.org/x/sync/errgroup"
)

// HealthCheck is the universal doctor pattern. Every check can optionally
// repair what it finds, and the framework re-runs detect() after repair()
// to verify the fix took.
type HealthCheck interface {
	// ID returns a stable machine-readable name (e.g. "core:db:connect").
	ID() string
	// Kind describes ownership: "core" or "plugin".
	Kind() string
	// Description returns a human-readable summary of what this check does.
	Description() string
	// Detect examines the current state and returns findings. An empty
	// slice means the check passed.
	Detect(ctx context.Context, cfg *config.Config) ([]HealthFinding, error)
	// Repair attempts to fix the given findings. Returns nil if this check
	// has no repair logic (read-only checks). After repair, the framework
	// re-runs Detect() to validate.
	Repair(ctx context.Context, cfg *config.Config, findings []HealthFinding) (*HealthRepairResult, error)
}

// HealthFinding is one discovered issue. Severity maps to the existing
// doctor.Severity enum: "info", "warning", "error".
type HealthFinding struct {
	CheckID  string `json:"checkId"`
	Severity string `json:"severity"` // "info" | "warning" | "error"
	Message  string `json:"message"`
	Path     string `json:"path,omitempty"`
	FixHint  string `json:"fixHint,omitempty"`
}

// HealthRepairResult describes what repair did.
type HealthRepairResult struct {
	Status  string   `json:"status"` // "repaired" | "skipped" | "failed"
	Reason  string   `json:"reason,omitempty"`
	Changes []string `json:"changes,omitempty"`
	Diffs   []string `json:"diffs,omitempty"` // unified diff strings
}

// Registry is the global health check container. Plugins call Register()
// at init. The doctor command runs all checks.
type Registry struct {
	mu     sync.RWMutex
	checks map[string]HealthCheck
}

// DefaultRegistry is the package-level singleton. In tests, create a
// fresh *Registry to avoid pollution from other test packages.
var DefaultRegistry = &Registry{checks: make(map[string]HealthCheck)}

// Register adds a health check to the default registry. Safe for concurrent
// use — intended to be called from plugin init() functions.
func Register(h HealthCheck) {
	DefaultRegistry.Register(h)
}

// Register adds a health check to this registry instance.
func (r *Registry) Register(h HealthCheck) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checks[h.ID()] = h
}

// Unregister removes a check. Used when a plugin is unloaded.
func (r *Registry) Unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.checks, id)
}

// List returns a snapshot of all registered checks.
func (r *Registry) List() []HealthCheck {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]HealthCheck, 0, len(r.checks))
	for _, h := range r.checks {
		out = append(out, h)
	}
	return out
}

// RunResult packages everything returned by a full doctor run.
type RunResult struct {
	RunID     string            `json:"runId"`
	Timestamp time.Time         `json:"timestamp"`
	Findings  []HealthFinding   `json:"findings"`
	Repaired  []string          `json:"repaired"`  // check IDs that were successfully repaired
	Failed    []string          `json:"failed"`    // check IDs where repair didn't help
	Skipped   []string          `json:"skipped"`   // check IDs with no repair logic
	Errors    []string          `json:"errors"`    // check IDs that errored during detect/repair
}

// RunDoctor executes all registered health checks using the
// detect→repair→validate loop:
//
//  1. detect() on every check → collect all findings
//  2. For each check with findings that has a repair(), call repair()
//  3. Re-run detect() on the repaired check to validate
//  4. If findings remain → warn. If clean → report success.
//
// Checks with Kind="plugin" are skipped unless includePlugins is true.
func RunDoctor(ctx context.Context, cfg *config.Config, includePlugins bool) (*RunResult, error) {
	return DefaultRegistry.RunDoctor(ctx, cfg, includePlugins)
}

// RunDoctor executes all registered checks on this registry instance.
// Checks run in parallel (up to 8 concurrent) via errgroup — each check's
// detect→repair→validate loop is independent, so parallelising avoids
// N×timeout on plugin-heavy installs. Individual failures never cancel peers.
func (r *Registry) RunDoctor(ctx context.Context, cfg *config.Config, includePlugins bool) (*RunResult, error) {
	checks := r.List()

	result := &RunResult{
		RunID:     fmt.Sprintf("dr-%d", time.Now().Unix()),
		Timestamp: time.Now().UTC(),
	}

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(8)

	var mu sync.Mutex

	runOne := func(h HealthCheck) {
		// Phase 1: Detect
		findings, err := safeDetect(ctx, h, cfg)
		if err != nil {
			mu.Lock()
			result.Errors = append(result.Errors, h.ID())
			result.Findings = append(result.Findings, HealthFinding{
				CheckID:  h.ID(),
				Severity: "error",
				Message:  fmt.Sprintf("detect failed: %v", err),
			})
			mu.Unlock()
			return
		}

		// No findings — check passed.
		if len(findings) == 0 {
			return
		}

		mu.Lock()
		result.Findings = append(result.Findings, findings...)
		mu.Unlock()

		// Phase 2: Repair
		repairResult, err := safeRepair(ctx, h, cfg, findings)
		if err != nil {
			mu.Lock()
			result.Errors = append(result.Errors, h.ID())
			result.Findings = append(result.Findings, HealthFinding{
				CheckID:  h.ID(),
				Severity: "error",
				Message:  fmt.Sprintf("repair failed: %v", err),
			})
			mu.Unlock()
			return
		}
		if repairResult == nil {
			mu.Lock()
			result.Skipped = append(result.Skipped, h.ID())
			mu.Unlock()
			return
		}

		// Phase 3: Re-detect to validate the fix
		postFindings, err := safeDetect(ctx, h, cfg)
		if err != nil {
			mu.Lock()
			result.Failed = append(result.Failed, h.ID())
			mu.Unlock()
			return
		}
		if len(postFindings) == 0 {
			mu.Lock()
			result.Repaired = append(result.Repaired, h.ID())
			mu.Unlock()
		} else {
			mu.Lock()
			result.Failed = append(result.Failed, h.ID())
			// Add post-repair findings with a note
			for _, f := range postFindings {
				f.CheckID = h.ID() + ":post-repair"
				result.Findings = append(result.Findings, f)
			}
			mu.Unlock()
		}
	}

	for _, h := range checks {
		// Skip plugin-owned checks unless explicitly included.
		if h.Kind() == "plugin" && !includePlugins {
			continue
		}
		h := h
		g.Go(func() error {
			runOne(h)
			return nil // never cancel peers on individual check failure
		})
	}

	_ = g.Wait()
	return result, nil
}

// safeDetect wraps Detect() so a panic in one check doesn't crash the
// entire sweep. The panic surfaces as an error.
func safeDetect(ctx context.Context, h HealthCheck, cfg *config.Config) (findings []HealthFinding, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("health check %s panicked during detect: %v", h.ID(), rec)
		}
	}()
	return h.Detect(ctx, cfg)
}

// safeRepair wraps Repair() with panic recovery.
func safeRepair(ctx context.Context, h HealthCheck, cfg *config.Config, findings []HealthFinding) (result *HealthRepairResult, err error) {
	if len(findings) == 0 {
		return nil, nil
	}
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("health check %s panicked during repair: %v", h.ID(), rec)
		}
	}()
	return h.Repair(ctx, cfg, findings)
}
