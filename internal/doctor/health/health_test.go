package health

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
)

// --- mockHealthCheck ---

type mockHealthCheck struct {
	id          string
	kind        string
	desc        string
	detect      func(ctx context.Context, cfg *config.Config) ([]HealthFinding, error)
	repair      func(ctx context.Context, cfg *config.Config, findings []HealthFinding) (*HealthRepairResult, error)
	detectCalls int
	repairCalls int
	mu          sync.Mutex
}

func (m *mockHealthCheck) ID() string          { return m.id }
func (m *mockHealthCheck) Kind() string        { return m.kind }
func (m *mockHealthCheck) Description() string { return m.desc }

func (m *mockHealthCheck) Detect(ctx context.Context, cfg *config.Config) ([]HealthFinding, error) {
	m.mu.Lock()
	m.detectCalls++
	m.mu.Unlock()
	return m.detect(ctx, cfg)
}

func (m *mockHealthCheck) Repair(ctx context.Context, cfg *config.Config, findings []HealthFinding) (*HealthRepairResult, error) {
	m.mu.Lock()
	m.repairCalls++
	m.mu.Unlock()
	return m.repair(ctx, cfg, findings)
}

// --- helpers ---

func passDetect() func(context.Context, *config.Config) ([]HealthFinding, error) {
	return func(_ context.Context, _ *config.Config) ([]HealthFinding, error) {
		return nil, nil // no findings = pass
	}
}

func failDetect(msg string) func(context.Context, *config.Config) ([]HealthFinding, error) {
	return func(_ context.Context, _ *config.Config) ([]HealthFinding, error) {
		return []HealthFinding{
			{CheckID: "test", Severity: "error", Message: msg},
		}, nil
	}
}

func errDetect(msg string) func(context.Context, *config.Config) ([]HealthFinding, error) {
	return func(_ context.Context, _ *config.Config) ([]HealthFinding, error) {
		return nil, errors.New(msg)
	}
}

func panicDetect() func(context.Context, *config.Config) ([]HealthFinding, error) {
	return func(_ context.Context, _ *config.Config) ([]HealthFinding, error) {
		panic("boom during detect")
	}
}

func successfulRepair() func(context.Context, *config.Config, []HealthFinding) (*HealthRepairResult, error) {
	return func(_ context.Context, _ *config.Config, _ []HealthFinding) (*HealthRepairResult, error) {
		return &HealthRepairResult{
			Status:  "repaired",
			Reason:  "fixed automatically",
			Changes: []string{"applied config fix"},
		}, nil
	}
}

func nilRepair() func(context.Context, *config.Config, []HealthFinding) (*HealthRepairResult, error) {
	return func(_ context.Context, _ *config.Config, _ []HealthFinding) (*HealthRepairResult, error) {
		return nil, nil // read-only check, intentionally skipped
	}
}

func failRepair() func(context.Context, *config.Config, []HealthFinding) (*HealthRepairResult, error) {
	return func(_ context.Context, _ *config.Config, _ []HealthFinding) (*HealthRepairResult, error) {
		return &HealthRepairResult{
			Status: "failed",
			Reason: "could not fix",
		}, nil
	}
}

func errRepair(msg string) func(context.Context, *config.Config, []HealthFinding) (*HealthRepairResult, error) {
	return func(_ context.Context, _ *config.Config, _ []HealthFinding) (*HealthRepairResult, error) {
		return nil, errors.New(msg)
	}
}

func panicRepair() func(context.Context, *config.Config, []HealthFinding) (*HealthRepairResult, error) {
	return func(_ context.Context, _ *config.Config, _ []HealthFinding) (*HealthRepairResult, error) {
		panic("boom during repair")
	}
}

// --- Tests ---

func TestRegister(t *testing.T) {
	r := &Registry{checks: make(map[string]HealthCheck)}

	h := &mockHealthCheck{id: "test:check", kind: "core", desc: "a test check"}
	r.Register(h)

	list := r.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 registered check, got %d", len(list))
	}
	if list[0].ID() != "test:check" {
		t.Errorf("expected ID 'test:check', got %q", list[0].ID())
	}
}

func TestRegisterOverwrite(t *testing.T) {
	r := &Registry{checks: make(map[string]HealthCheck)}

	h1 := &mockHealthCheck{id: "test:check", kind: "core", desc: "first"}
	h2 := &mockHealthCheck{id: "test:check", kind: "plugin", desc: "second"}
	r.Register(h1)
	r.Register(h2)

	list := r.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 check after overwrite, got %d", len(list))
	}
	if list[0].Description() != "second" {
		t.Errorf("expected overwritten description 'second', got %q", list[0].Description())
	}
}

func TestUnregister(t *testing.T) {
	r := &Registry{checks: make(map[string]HealthCheck)}

	h := &mockHealthCheck{id: "a:1", kind: "core", desc: "check one"}
	r.Register(h)
	r.Unregister("a:1")

	list := r.List()
	if len(list) != 0 {
		t.Errorf("expected empty registry after unregister, got %d checks", len(list))
	}
}

func TestUnregisterMissing(t *testing.T) {
	r := &Registry{checks: make(map[string]HealthCheck)}
	// Should not panic
	r.Unregister("nonexistent")
	if len(r.List()) != 0 {
		t.Error("registry should still be empty")
	}
}

func TestListEmpty(t *testing.T) {
	r := &Registry{checks: make(map[string]HealthCheck)}
	list := r.List()
	if len(list) != 0 {
		t.Errorf("expected empty list, got %d", len(list))
	}
	// Should return empty slice, not nil
	if list == nil {
		t.Error("List() should return non-nil empty slice (from make)")
	}
}

func TestRegisterConcurrent(t *testing.T) {
	r := &Registry{checks: make(map[string]HealthCheck)}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			r.Register(&mockHealthCheck{
				id:   fmt.Sprintf("check:%d", n),
				kind: "core",
				desc: "concurrent",
			})
		}(i)
	}
	wg.Wait()

	list := r.List()
	if len(list) != 100 {
		t.Errorf("expected 100 checks, got %d", len(list))
	}
}

// --- RunDoctor tests ---

func TestRunDoctorAllPass(t *testing.T) {
	r := &Registry{checks: make(map[string]HealthCheck)}

	for i := 0; i < 3; i++ {
		r.Register(&mockHealthCheck{
			id:     fmt.Sprintf("pass:%d", i),
			kind:   "core",
			desc:   "always passes",
			detect: passDetect(),
		})
	}

	cfg := &config.Config{}
	result, err := r.RunDoctor(context.Background(), cfg, false)
	if err != nil {
		t.Fatalf("RunDoctor() unexpected error: %v", err)
	}
	if len(result.Findings) != 0 {
		t.Errorf("expected 0 findings, got %d", len(result.Findings))
	}
	if result.RunID == "" {
		t.Error("RunID should not be empty")
	}
}

func TestRunDoctorDetectFindsIssueAndRepairs(t *testing.T) {
	r := &Registry{checks: make(map[string]HealthCheck)}

	// detect fails, repair succeeds, re-detect passes
	detectCalled := 0
	r.Register(&mockHealthCheck{
		id:   "fixable",
		kind: "core",
		desc: "finds issue then fixes it",
		detect: func(_ context.Context, _ *config.Config) ([]HealthFinding, error) {
			detectCalled++
			if detectCalled == 1 {
				return []HealthFinding{{CheckID: "fixable", Severity: "error", Message: "broken"}}, nil
			}
			return nil, nil // fixed on re-detect
		},
		repair: successfulRepair(),
	})

	cfg := &config.Config{}
	result, err := r.RunDoctor(context.Background(), cfg, false)
	if err != nil {
		t.Fatalf("RunDoctor() unexpected error: %v", err)
	}

	// Should have one finding from initial detect
	if len(result.Findings) != 1 {
		t.Errorf("expected 1 finding, got %d", len(result.Findings))
	}
	if len(result.Repaired) != 1 {
		t.Errorf("expected 1 repaired, got %d", len(result.Repaired))
	}
	if result.Repaired[0] != "fixable" {
		t.Errorf("expected repaired ID 'fixable', got %q", result.Repaired[0])
	}
}

func TestRunDoctorRepairReturnsNil(t *testing.T) {
	r := &Registry{checks: make(map[string]HealthCheck)}

	r.Register(&mockHealthCheck{
		id:     "readonly",
		kind:   "core",
		desc:   "read-only check, no repair",
		detect: failDetect("issue found"),
		repair: nilRepair(),
	})

	cfg := &config.Config{}
	result, err := r.RunDoctor(context.Background(), cfg, false)
	if err != nil {
		t.Fatalf("RunDoctor() unexpected error: %v", err)
	}

	if len(result.Skipped) != 1 {
		t.Errorf("expected 1 skipped, got %d", len(result.Skipped))
	}
	if result.Skipped[0] != "readonly" {
		t.Errorf("expected skipped ID 'readonly', got %q", result.Skipped[0])
	}
}

func TestRunDoctorRepairFailsDetection(t *testing.T) {
	r := &Registry{checks: make(map[string]HealthCheck)}

	// detect always fails — repair can't fix it
	r.Register(&mockHealthCheck{
		id:     "unfixable",
		kind:   "core",
		desc:   "unfixable issue",
		detect: failDetect("persistent issue"),
		repair: failRepair(),
	})

	cfg := &config.Config{}
	result, err := r.RunDoctor(context.Background(), cfg, false)
	if err != nil {
		t.Fatalf("RunDoctor() unexpected error: %v", err)
	}

	if len(result.Failed) != 1 {
		t.Errorf("expected 1 failed, got %d", len(result.Failed))
	}
	if result.Failed[0] != "unfixable" {
		t.Errorf("expected failed ID 'unfixable', got %q", result.Failed[0])
	}
	// Post-repair findings should include a "post-repair" check entry
	hasPostRepair := false
	for _, f := range result.Findings {
		if f.CheckID == "unfixable:post-repair" {
			hasPostRepair = true
		}
	}
	if !hasPostRepair {
		t.Error("expected post-repair finding with check ID 'unfixable:post-repair'")
	}
}

func TestRunDoctorDetectError(t *testing.T) {
	r := &Registry{checks: make(map[string]HealthCheck)}

	r.Register(&mockHealthCheck{
		id:     "detect-err",
		kind:   "core",
		desc:   "detect errors out",
		detect: errDetect("connection refused"),
	})

	cfg := &config.Config{}
	result, err := r.RunDoctor(context.Background(), cfg, false)
	if err != nil {
		t.Fatalf("RunDoctor() unexpected error: %v", err)
	}

	if len(result.Errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(result.Errors))
	}
	if result.Errors[0] != "detect-err" {
		t.Errorf("expected error ID 'detect-err', got %q", result.Errors[0])
	}
}

func TestRunDoctorRepairError(t *testing.T) {
	r := &Registry{checks: make(map[string]HealthCheck)}

	r.Register(&mockHealthCheck{
		id:     "repair-err",
		kind:   "core",
		desc:   "repair errors out",
		detect: failDetect("needs fixing"),
		repair: errRepair("repair tool crashed"),
	})

	cfg := &config.Config{}
	result, err := r.RunDoctor(context.Background(), cfg, false)
	if err != nil {
		t.Fatalf("RunDoctor() unexpected error: %v", err)
	}

	if len(result.Errors) != 1 {
		t.Errorf("expected 1 error entry, got %d", len(result.Errors))
	}
}

func TestRunDoctorDetectPanicRecovery(t *testing.T) {
	r := &Registry{checks: make(map[string]HealthCheck)}

	r.Register(&mockHealthCheck{
		id:     "panic-detect",
		kind:   "core",
		desc:   "panics during detect",
		detect: panicDetect(),
	})

	cfg := &config.Config{}
	result, err := r.RunDoctor(context.Background(), cfg, false)
	if err != nil {
		t.Fatalf("RunDoctor() should not error on panic: %v", err)
	}

	if len(result.Errors) != 1 {
		t.Errorf("expected 1 error from panic, got %d", len(result.Errors))
	}
}

func TestRunDoctorRepairPanicRecovery(t *testing.T) {
	r := &Registry{checks: make(map[string]HealthCheck)}

	r.Register(&mockHealthCheck{
		id:     "panic-repair",
		kind:   "core",
		desc:   "panics during repair",
		detect: failDetect("needs fixing"),
		repair: panicRepair(),
	})

	cfg := &config.Config{}
	result, err := r.RunDoctor(context.Background(), cfg, false)
	if err != nil {
		t.Fatalf("RunDoctor() should not error on panic: %v", err)
	}

	if len(result.Errors) != 1 {
		t.Errorf("expected 1 error from panic, got %d", len(result.Errors))
	}
}

func TestRunDoctorSkipsPlugins(t *testing.T) {
	r := &Registry{checks: make(map[string]HealthCheck)}

	pluginCheck := &mockHealthCheck{
		id:     "plugin:example",
		kind:   "plugin",
		desc:   "a plugin check",
		detect: failDetect("plugin issue"),
		repair: nilRepair(), // read-only, no repair logic
	}
	r.Register(&mockHealthCheck{
		id:     "core:one",
		kind:   "core",
		desc:   "core check",
		detect: passDetect(),
	})
	r.Register(pluginCheck)

	cfg := &config.Config{}

	// with includePlugins=false, plugin check is skipped
	result, err := r.RunDoctor(context.Background(), cfg, false)
	if err != nil {
		t.Fatalf("RunDoctor() unexpected error: %v", err)
	}
	if len(result.Findings) != 0 {
		t.Errorf("expected 0 findings (plugin skipped), got %d", len(result.Findings))
	}

	// with includePlugins=true, plugin check runs
	result2, err2 := r.RunDoctor(context.Background(), cfg, true)
	if err2 != nil {
		t.Fatalf("RunDoctor() with plugins unexpected error: %v", err2)
	}
	if len(result2.Findings) != 1 {
		t.Errorf("expected 1 finding (plugin included), got %d", len(result2.Findings))
	}
}

func TestDefaultRegistryRegister(t *testing.T) {
	// Save and restore
	old := DefaultRegistry
	defer func() { DefaultRegistry = old }()

	DefaultRegistry = &Registry{checks: make(map[string]HealthCheck)}

	h := &mockHealthCheck{id: "default:check", kind: "core", desc: "default registry test"}
	Register(h)

	list := DefaultRegistry.List()
	if len(list) != 1 {
		t.Errorf("expected 1 check in default registry, got %d", len(list))
	}
}

func TestRunDoctorRunIDFormat(t *testing.T) {
	r := &Registry{checks: make(map[string]HealthCheck)}

	r.Register(&mockHealthCheck{
		id:     "format-test",
		kind:   "core",
		desc:   "format check",
		detect: passDetect(),
	})

	cfg := &config.Config{}
	result, err := r.RunDoctor(context.Background(), cfg, false)
	if err != nil {
		t.Fatalf("RunDoctor() error: %v", err)
	}

	if result.RunID == "" {
		t.Error("RunID should not be empty")
	}
	if len(result.RunID) < 4 {
		t.Errorf("RunID too short: %q", result.RunID)
	}
	if result.Timestamp.IsZero() {
		t.Error("Timestamp should not be zero")
	}
}

// --- Struct tests ---

func TestHealthFinding(t *testing.T) {
	hf := HealthFinding{
		CheckID:  "test:1",
		Severity: "error",
		Message:  "something is wrong",
		Path:     "/etc/config.toml",
		FixHint:  "run --fix",
	}
	if hf.CheckID != "test:1" {
		t.Errorf("CheckID mismatch: %q", hf.CheckID)
	}
	if hf.Severity != "error" {
		t.Errorf("Severity mismatch: %q", hf.Severity)
	}
	if hf.Path != "/etc/config.toml" {
		t.Errorf("Path mismatch: %q", hf.Path)
	}
	if hf.FixHint != "run --fix" {
		t.Errorf("FixHint mismatch: %q", hf.FixHint)
	}
}

func TestHealthRepairResult(t *testing.T) {
	hr := HealthRepairResult{
		Status:  "repaired",
		Reason:  "config fixed",
		Changes: []string{"line 42 changed"},
		Diffs:   []string{"@@ -42,3 +42,3 @@"},
	}
	if hr.Status != "repaired" {
		t.Errorf("Status mismatch: %q", hr.Status)
	}
	if len(hr.Changes) != 1 {
		t.Errorf("expected 1 change, got %d", len(hr.Changes))
	}
}
