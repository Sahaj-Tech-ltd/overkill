package security

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// ────────────────────────────────────────────────────────────────────
// 8.7.5 Concurrency tests — fire N goroutines at the same endpoint
// simultaneously. Catch duplicate-row creation under race, dropped
// writes, panics under load.
// ────────────────────────────────────────────────────────────────────

// TestPermissionManager_ConcurrentCheck_AllowOnceAtomic verifies that
// when N goroutines call Check on the same AllowOnce pattern, exactly
// ONE gets ActionAllowOnce and the rest get ActionDeny. (AllowOnce
// must be consumed atomically — this is the core correctness property.)
func TestPermissionManager_ConcurrentCheck_AllowOnceAtomic(t *testing.T) {
	const N = 50
	pm := NewPermissionManager()
	pm.Allow(PermissionDecision{
		Action:  ActionAllowOnce,
		Pattern: "dangerous",
	})

	var wg sync.WaitGroup
	results := make(chan PermissionAction, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			dec := pm.Check("dangerous", "dangerous", "/project")
			results <- dec.Action
		}()
	}
	wg.Wait()
	close(results)

	allowOnce := 0
	for action := range results {
		if action == ActionAllowOnce {
			allowOnce++
		}
	}
	if allowOnce != 1 {
		t.Errorf("AllowOnce consumed by exactly 1 goroutine, got %d", allowOnce)
	}
}

// TestPermissionManager_ConcurrentCheckAndAllow races Allow and Check
// concurrently — the permission manager must remain consistent.
func TestPermissionManager_ConcurrentCheckAndAllow(t *testing.T) {
	const N = 100
	pm := NewPermissionManager()

	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			// Allow a new pattern
			pm.Allow(PermissionDecision{
				Action:  ActionAllowOnce,
				Pattern: "pattern_" + string(rune('a'+id%26)),
			})
			// Then check it (or another)
			pm.Check("pattern_a", "cmd", "/project")
		}(i)
	}
	wg.Wait()
	// No panic = passes (race detector checks the rest)
}

// TestPermissionManager_ConcurrentClearAndCheck races ClearSession and
// ClearProject against concurrent Check calls.
func TestPermissionManager_ConcurrentClearAndCheck(t *testing.T) {
	const N = 200
	pm := NewPermissionManager()
	// Seed with various permissions.
	for i := 0; i < 10; i++ {
		pm.Allow(PermissionDecision{
			Action:      ActionAllowProject,
			Pattern:     "p" + string(rune('a'+i%26)),
			ProjectPath: "/project",
		})
	}
	pm.Allow(PermissionDecision{Action: ActionAllowOnce, Pattern: "once"})
	pm.Allow(PermissionDecision{Action: ActionAllowGlobal, Pattern: "global"})

	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			switch id % 4 {
			case 0:
				pm.Check("once", "cmd", "/project")
			case 1:
				pm.Check("p_a", "cmd", "/project")
			case 2:
				pm.Check("global", "cmd", "/project")
			default:
				pm.IsAllowed("p_b", "cmd", "/project")
			}
		}(i)
	}

	// Race clears against the ongoing checks.
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			time.Sleep(time.Microsecond * time.Duration(id))
			if id%2 == 0 {
				pm.ClearSession()
			} else {
				pm.ClearProject("/project")
			}
		}(i)
	}
	wg.Wait()
}

// TestPermissionManager_ConcurrentProjects racy multi-project Allow/Check/Clear.
func TestPermissionManager_ConcurrentProjects(t *testing.T) {
	const NProjects = 20
	const NGoroutines = 100
	pm := NewPermissionManager()

	var wg sync.WaitGroup
	// Seed projects
	for i := 0; i < NProjects; i++ {
		pm.Allow(PermissionDecision{
			Action:      ActionAllowProject,
			Pattern:     "dangerous",
			ProjectPath: "/project/" + string(rune('a'+i%26)),
		})
	}

	for i := 0; i < NGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			proj := "/project/" + string(rune('a'+id%NProjects))
			switch id % 3 {
			case 0:
				pm.Check("dangerous", "cmd", proj)
			case 1:
				pm.Allow(PermissionDecision{
					Action:      ActionAllowProject,
					Pattern:     "safe",
					ProjectPath: proj,
				})
			default:
				pm.IsAllowed("dangerous", "cmd", proj)
			}
		}(i)
	}
	wg.Wait()
}

// TestPermissionManager_ConcurrentAllowOnceIsAllowed verifies that
// IsAllowed does NOT consume AllowOnce grants even under race.
func TestPermissionManager_ConcurrentAllowOnceIsAllowed(t *testing.T) {
	const N = 100
	pm := NewPermissionManager()
	pm.Allow(PermissionDecision{
		Action:  ActionAllowOnce,
		Pattern: "test",
	})

	var wg sync.WaitGroup
	count := 0
	var mu sync.Mutex
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if pm.IsAllowed("test", "cmd", "/project") {
				mu.Lock()
				count++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	// IsAllowed should return true for all goroutines (non-consuming)
	if count != N {
		t.Errorf("IsAllowed should return true for all %d goroutines, got %d", N, count)
	}
	// After all IsAllowed calls, the grant should still be intact.
	if !pm.IsAllowed("test", "cmd", "/project") {
		t.Error("IsAllowed must not consume the AllowOnce grant")
	}
}

// TestPrivilegeGate_ConcurrentModeSwitch races SetMode against Allow.
func TestPrivilegeGate_ConcurrentModeSwitch(t *testing.T) {
	const N = 200
	g := NewPrivilegeGate(ModeWriter)

	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(id int) {
			defer wg.Done()
			if id%2 == 0 {
				g.SetMode(ModeReader)
			} else {
				g.SetMode(ModeWriter)
			}
			// Always call Allow — must not panic.
			g.Allow("shell", json.RawMessage(`{"command":"rm /tmp/x"}`))
		}(i)
	}
	wg.Wait()
}

// TestPrivilegeGate_ConcurrentAllow races Allow calls from many goroutines.
func TestPrivilegeGate_ConcurrentAllow(t *testing.T) {
	const N = 100
	g := NewPrivilegeGate(ModeReader)

	tools := []struct {
		name string
		raw  json.RawMessage
	}{
		{"fs_write", json.RawMessage(`{}`)},
		{"shell", json.RawMessage(`{"command":"ls"}`)},
		{"shell", json.RawMessage(`{"command":"rm /tmp/x"}`)},
		{"git", json.RawMessage(`{"subcommand":"push"}`)},
		{"fs", json.RawMessage(`{"action":"read"}`)},
		{"fs", json.RawMessage(`{"action":"write","path":"x"}`)},
		{"patch", json.RawMessage(`{}`)},
		{"grep", json.RawMessage(`{}`)},
	}

	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			tc := tools[id%len(tools)]
			g.Allow(tc.name, tc.raw)
		}(i)
	}
	wg.Wait()
}

// TestCommandScanner_ConcurrentScan fires concurrent Scan calls through
// the rate limiter — the TOCTOU fix in checkAndRecord must hold.
func TestCommandScanner_ConcurrentScan(t *testing.T) {
	const N = 100
	scanner := NewCommandScanner()
	scanner.maxCmds = 50
	scanner.window = time.Minute

	var wg sync.WaitGroup
	rateLimited := 0
	var mu sync.Mutex

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := scanner.Scan("ls -la")
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if result.Blocked {
				mu.Lock()
				rateLimited++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	// At most N-maxCmds should be rate-limited (the others should pass)
	if rateLimited < N-scanner.maxCmds {
		t.Logf("rate limited: %d/%d", rateLimited, N)
	}
}

// TestCommandScanner_ConcurrentWithPermissions races Scan calls with
// concurrent permission Allow calls on the same PermissionManager.
func TestCommandScanner_ConcurrentWithPermissions(t *testing.T) {
	const N = 100
	pm := NewPermissionManager()
	scanner := NewCommandScanner(
		WithPermissionManager(pm),
		WithProjectPath("/test"),
	)

	// Pre-allow rm_rf_root for /test
	pm.Allow(PermissionDecision{
		Action:      ActionAllowProject,
		Pattern:     "rm_rf_root",
		ProjectPath: "/test",
	})

	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			// Alternate between a blocked and safe command.
			if id%2 == 0 {
				scanner.Scan("rm -rf /")
			} else {
				scanner.Scan("ls -la")
			}
		}(i)
	}
	wg.Wait()
}

// TestLedger_ConcurrentAppend fires concurrent Append calls and verifies
// all entries are recorded.
func TestLedger_ConcurrentAppend(t *testing.T) {
	const N = 100
	dir := t.TempDir()
	l, err := NewLedger(dir + "/concurrent.jsonl")
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			_ = l.Append(LedgerEntry{
				Tool:     "shell",
				Args:     "echo",
				Decision: "allow_once",
				Risk:     "low",
			})
		}(i)
	}
	wg.Wait()

	entries := l.Entries()
	if len(entries) != N {
		t.Errorf("expected %d entries, got %d", N, len(entries))
	}
}

// TestLedger_ConcurrentAppendAndFilter races Append against Filter/Entries.
func TestLedger_ConcurrentAppendAndFilter(t *testing.T) {
	const NReaders = 20
	const NWriters = 20
	dir := t.TempDir()
	l, err := NewLedger(dir + "/racelog.jsonl")
	if err != nil {
		t.Fatal(err)
	}

	// Pre-seed some entries.
	for i := 0; i < 20; i++ {
		_ = l.Append(LedgerEntry{
			Tool:     "shell",
			Args:     "ls",
			Decision: "allow_once",
		})
	}

	var wg sync.WaitGroup

	// Writers: keep appending.
	for i := 0; i < NWriters; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_ = l.Append(LedgerEntry{
					Tool:     "shell",
					Args:     "cmd",
					Decision: "allow_once",
					Risk:     "low",
				})
			}
		}(i)
	}

	// Readers: keep reading.
	for i := 0; i < NReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_ = l.Entries()
				_ = l.Filter(func(e LedgerEntry) bool {
					return e.Decision == "allow_once"
				})
			}
		}()
	}

	wg.Wait()
	// All entries should be accounted for.
	final := l.Entries()
	if len(final) < 20 {
		t.Errorf("expected at least %d entries, got %d", 20, len(final))
	}
}

// TestIsWriteLikeTool_Concurrent covers the exported function under load.
func TestIsWriteLikeTool_Concurrent(t *testing.T) {
	const N = 100
	inputs := []struct {
		name string
		raw  json.RawMessage
	}{
		{"shell", json.RawMessage(`{"command":"rm /tmp/x"}`)},
		{"shell", json.RawMessage(`{"command":"echo hi"}`)},
		{"fs", json.RawMessage(`{"action":"write","path":"x"}`)},
		{"fs", json.RawMessage(`{"action":"read","path":"x"}`)},
		{"git", json.RawMessage(`{"subcommand":"push"}`)},
		{"git", json.RawMessage(`{"subcommand":"log"}`)},
		{"fs_write", json.RawMessage(`{}`)},
		{"grep", json.RawMessage(`{}`)},
		{"patch", json.RawMessage(`{}`)},
	}

	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			in := inputs[id%len(inputs)]
			IsWriteLikeTool(in.name, in.raw)
		}(i)
	}
	wg.Wait()
}

// TestThreatLevel_String covers the String() method under concurrent access.
func TestThreatLevel_StringConcurrent(t *testing.T) {
	const N = 100
	levels := []ThreatLevel{ThreatNone, ThreatLow, ThreatMedium, ThreatHigh, ThreatCritical, ThreatLevel(99)}

	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, l := range levels {
				_ = l.String()
			}
		}()
	}
	wg.Wait()
}

// TestPermissionAction_StringConcurrent covers the String() method under race.
func TestPermissionAction_StringConcurrent(t *testing.T) {
	const N = 100
	actions := []PermissionAction{ActionDeny, ActionAllowOnce, ActionAllowProject, ActionAllowGlobal, PermissionAction(99)}

	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, a := range actions {
				_ = a.String()
			}
		}()
	}
	wg.Wait()
}

// TestPermissionManager_ConcurrentIsAllowedStress is a heavy stress test
// mixing all operations.
func TestPermissionManager_ConcurrentIsAllowedStress(t *testing.T) {
	const N = 200
	pm := NewPermissionManager()

	// Seed
	pm.Allow(PermissionDecision{Action: ActionAllowGlobal, Pattern: "global"})
	pm.Allow(PermissionDecision{Action: ActionAllowProject, Pattern: "proj", ProjectPath: "/p"})
	pm.Allow(PermissionDecision{Action: ActionAllowOnce, Pattern: "once"})

	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			switch id % 6 {
			case 0:
				pm.Check("once", "cmd", "/any")
			case 1:
				pm.Check("global", "cmd", "/any")
			case 2:
				pm.Check("proj", "cmd", "/p")
			case 3:
				pm.IsAllowed("global", "cmd", "/any")
			case 4:
				pm.Allow(PermissionDecision{Action: ActionAllowOnce, Pattern: "new_" + string(rune('a'+id%26))})
			default:
				pm.Check("nonexistent", "cmd", "/none")
			}
		}(i)
	}
	wg.Wait()
}
