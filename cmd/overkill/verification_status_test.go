package main

import (
	"context"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/plan"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/Sahaj-Tech-ltd/overkill/internal/verify"
	"github.com/stretchr/testify/require"
	"path/filepath"
)

type heldStatusChecker struct{ entered chan struct{} }

func (*heldStatusChecker) Name() string           { return "held real checker" }
func (*heldStatusChecker) Timeout() time.Duration { return 5 * time.Second }
func (v *heldStatusChecker) Verify(ctx context.Context, _ string, _ []byte) (bool, string, bool) {
	close(v.entered)
	<-ctx.Done()
	return false, ctx.Err().Error(), true
}
func TestProductionVerifierPublishesOnlyActualChecks(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, tc := range []struct {
			name, path, content string
			passed, failed      int
		}{{"pass", "file.json", "{}", 1, 0}, {"fail", "file.json", "{", 0, 2}, {"unmatched", "file.unknown", "text", 0, 0}} {
			t.Run(tc.name+map[bool]string{false: "/run", true: "/stream"}[stream], func(t *testing.T) {
				t.Setenv("OVERKILL_NO_VERIFY", "")
				services, err := newRuntimeServices(config.Default(), nil, t.TempDir())
				require.NoError(t, err)
				metadata := session.NewSession(t.TempDir())
				runtime, err := services.BuildSession(t.Context(), metadata)
				require.NoError(t, err)
				defer runtime.Cleanup()
				p := &verificationScript{path: tc.path, broken: tc.content}
				a := agent.New(agent.Config{Provider: p, Model: "fixture", Tools: runtime.Tools, SessionID: metadata.ID, MaxSteps: 5})
				defer a.Shutdown()
				a.SetPostWriteVerifier(runtime.PostWriteVerifier)
				if stream {
					events, err := a.Stream(t.Context(), "write fixture")
					require.NoError(t, err)
					for e := range events {
						require.NoError(t, e.Error)
					}
				} else {
					_, err := a.Run(t.Context(), "write fixture")
					require.NoError(t, err)
				}
				snap := a.VerificationSnapshot()
				require.False(t, snap.Running)
				require.Equal(t, tc.passed, snap.Passed)
				require.Equal(t, tc.failed, snap.Failed)
				require.Len(t, snap.Tests, tc.passed+tc.failed)
			})
		}
	}
}
func TestProductionVerifierCancellationClosesObservedCheck(t *testing.T) {
	t.Setenv("OVERKILL_NO_VERIFY", "")
	services, err := newRuntimeServices(config.Default(), nil, t.TempDir())
	require.NoError(t, err)
	m := session.NewSession(t.TempDir())
	runtime, err := services.BuildSession(t.Context(), m)
	require.NoError(t, err)
	defer runtime.Cleanup()
	entered := make(chan struct{})
	adapter := runtime.PostWriteVerifier.(*verifierAdapter)
	adapter.registry = verify.NewRegistry()
	adapter.registry.Register(".json", &heldStatusChecker{entered: entered})
	a := agent.New(agent.Config{Provider: &verificationScript{path: "held.json", broken: "{}"}, Model: "fixture", Tools: runtime.Tools, SessionID: m.ID, MaxSteps: 5})
	defer a.Shutdown()
	a.SetPostWriteVerifier(adapter)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := a.Run(ctx, "write fixture"); done <- err }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("checker not invoked")
	}
	require.True(t, a.VerificationSnapshot().Running)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("canceled checker did not finish")
	}
	snap := a.VerificationSnapshot()
	require.False(t, snap.Running)
	require.Equal(t, 1, snap.Canceled)
	require.Zero(t, snap.Passed)
}
func TestRuntimeDashboardReadersUsePersistedSessionBinding(t *testing.T) {
	db, _ := testpg.Open(t)
	home := t.TempDir()
	services, err := newRuntimeServices(config.Default(), db, home)
	require.NoError(t, err)
	store, err := session.NewPostgresStore(db)
	require.NoError(t, err)
	m := session.NewSession(t.TempDir())
	require.NoError(t, store.Create(t.Context(), m))
	require.NoError(t, services.Goals.SetWithBudgets(t.Context(), m.ID, "durable goal", 123, 4))
	_, err = plan.NewStore(filepath.Join(home, "plans"), m.ID).Set("durable plan", []string{"step"})
	require.NoError(t, err)
	cfg := services.APIConfig(store)
	g, err := cfg.DashboardGoalReader(t.Context(), m.ID)
	require.NoError(t, err)
	require.Equal(t, "durable goal", g.Text)
	require.Equal(t, 123, g.TokenBudget)
	p, err := cfg.DashboardPlanReader(t.Context(), m.ID)
	require.NoError(t, err)
	require.Equal(t, "durable plan", p.Title)
}
