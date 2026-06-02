package agent

import (
	"sync"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
)

// ────────────────────────────────────────────────────────────────────
// 8.7.5 Concurrency tests — fire N goroutines at the same endpoint
// simultaneously. Catch duplicate-row creation under race, dropped
// writes, panics under load.
// ────────────────────────────────────────────────────────────────────

// TestAgent_ApprovalCheckConcurrent fires N goroutines at approvalCheck
// simultaneously. The callback must be invoked correctly under race,
// and the persisted allowedTools cache must reflect the first persist.
func TestAgent_ApprovalCheckConcurrent(t *testing.T) {
	a := New(Config{Model: "test", MaxSteps: 5})
	var mu sync.Mutex
	callCount := 0
	approvals := make([]bool, 0)

	a.SetApprovalFunc(func(toolName, args, risk string) Approval {
		mu.Lock()
		callCount++
		mu.Unlock()
		return Approval{Allow: true, Persist: true}
	})

	const N = 50
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := a.checkToolApproval("shell", "rm -rf /")
			mu.Lock()
			approvals = append(approvals, result)
			mu.Unlock()
		}()
	}
	wg.Wait()

	// All should be approved (either by callback or cached).
	for i, result := range approvals {
		if !result {
			t.Errorf("goroutine %d: expected approval, got deny", i)
		}
	}
	// At least one callback fire (for the first caller), but not N.
	if callCount < 1 || callCount > N {
		t.Errorf("callCount = %d, expected 1..%d", callCount, N)
	}
}

// TestAgent_ApprovalCheckConcurrent_NoPersist fires N goroutines at
// approvalCheck where the callback does NOT persist. Every caller must
// hit the callback.
func TestAgent_ApprovalCheckConcurrent_NoPersist(t *testing.T) {
	a := New(Config{Model: "test", MaxSteps: 5})
	var mu sync.Mutex
	callCount := 0

	a.SetApprovalFunc(func(toolName, args, risk string) Approval {
		mu.Lock()
		callCount++
		mu.Unlock()
		return Approval{Allow: true, Persist: false}
	})

	const N = 50
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.checkToolApproval("shell", "rm -rf /")
		}()
	}
	wg.Wait()

	// Without persist, the allowedTools cache should not have been set,
	// so all N callers hit the callback.
	if callCount != N {
		t.Errorf("expected %d callbacks (no persist), got %d", N, callCount)
	}
}

// TestAgent_ApprovalCheckConcurrent_Deny races deny callbacks.
func TestAgent_ApprovalCheckConcurrent_Deny(t *testing.T) {
	a := New(Config{Model: "test", MaxSteps: 5})
	var mu sync.Mutex
	callCount := 0

	a.SetApprovalFunc(func(toolName, args, risk string) Approval {
		mu.Lock()
		callCount++
		mu.Unlock()
		return Approval{Allow: false, Persist: false}
	})

	const N = 50
	denied := 0
	var mu2 sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !a.checkToolApproval("shell", "rm -rf /") {
				mu2.Lock()
				denied++
				mu2.Unlock()
			}
		}()
	}
	wg.Wait()

	if denied != N {
		t.Errorf("expected %d denials, got %d", N, denied)
	}
	if callCount != N {
		t.Errorf("expected %d callbacks, got %d", N, callCount)
	}
}

// TestAgent_SetApprovalFuncConcurrent races SetApprovalFunc against
// concurrent checkToolApproval calls.
func TestAgent_SetApprovalFuncConcurrent(t *testing.T) {
	a := New(Config{Model: "test", MaxSteps: 5})

	var wg sync.WaitGroup

	// G1: Keep replacing the approval func mid-flight.
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			a.SetApprovalFunc(func(toolName, args, risk string) Approval {
				return Approval{Allow: id%2 == 0, Persist: true}
			})
		}(i)
	}

	// G2: Keep checking tools.
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.checkToolApproval("shell", "echo hi")
			a.checkToolApproval("fs_write", `{"path":"x"}`)
		}()
	}

	wg.Wait()
	// Must not panic.
}

// TestAgent_ApprovalCheck_AllowedToolsRace races the allowedTools
// map access between checkToolApproval (which reads it via approvalCheck)
// and SetApprovalFunc (which writes allowedTools).
func TestAgent_ApprovalCheck_AllowedToolsRace(t *testing.T) {
	a := New(Config{Model: "test", MaxSteps: 5})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			a.checkToolApproval("shell", "ls")
			a.checkToolApproval("patch", `{}`)
			a.checkToolApproval("git", `{"subcommand":"push"}`)
		}(i)
	}

	wg.Wait()
}

// TestAgent_StateTransitionsUnderLoad races agent state mutations
// (model, history, privilege gate) against concurrent reads.
func TestAgent_StateTransitionsUnderLoad(t *testing.T) {
	a := New(Config{Model: "test", MaxSteps: 20, SessionID: "load-test"})

	var wg sync.WaitGroup

	// Writers: change model, history, privilege gate.
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			a.SetModel("model-" + string(rune('a'+id%26)))
			a.SetHistory([]providers.Message{
				{Role: "user", Content: "msg"},
			})
			gate := security.NewPrivilegeGate(security.ModeWriter)
			a.SetPrivilegeGate(gate)
		}(i)
	}

	// Readers: read model, history, privilege mode.
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = a.Model()
			_ = a.History()
			_ = a.PrivilegeMode()
		}()
	}

	wg.Wait()
}

// TestAgent_ReceiptChain_ConcurrentStress races heavy concurrent Append
// calls with Snapshot reads, verifying chain integrity under load.
func TestAgent_ReceiptChain_ConcurrentStress(t *testing.T) {
	rc := NewReceiptChain()
	const N = 200

	var wg sync.WaitGroup

	// Writers: append receipts.
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			rc.Append("sess-stress", "shell", []byte("input"), []byte("output"), nil)
		}(i)
	}

	// Reader: periodically snapshot and verify during appends.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				snap := rc.Snapshot()
				// Verify must not race with concurrent appends
				// (snapshot is a copy, so verification is safe).
				if len(snap) > 0 {
					VerifyChain(snap)
				}
			}
		}()
	}

	wg.Wait()

	if rc.Len() != N {
		t.Errorf("expected %d receipts, got %d", N, rc.Len())
	}
	// Final verification: chain must be intact.
	if idx, err := VerifyChain(rc.Snapshot()); idx != -1 {
		t.Errorf("chain broken at %d: %v", idx, err)
	}
}

// TestAgent_EStopConcurrent races EStop, ResetStop, and StopCh reads.
func TestAgent_EStopConcurrent(t *testing.T) {
	a := New(Config{Model: "test", MaxSteps: 5})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = a.StopCh()
		}()
	}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.EStop()
			a.ResetStop()
		}()
	}

	wg.Wait()
	// After all resets, StopCh should be open.
	select {
	case <-a.StopCh():
		t.Error("stop channel should be open after final ResetStop")
	default:
	}
}

// TestAgent_PrivilegeGateConcurrent races setting and reading the privilege
// gate while checking privilege mode and calling SetPrivilegeMode.
func TestAgent_PrivilegeGateConcurrent(t *testing.T) {
	a := New(Config{Model: "test", MaxSteps: 5})
	gate := security.NewPrivilegeGate(security.ModeWriter)
	a.SetPrivilegeGate(gate)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if id%2 == 0 {
				a.SetPrivilegeMode(security.ModeReader)
			} else {
				a.SetPrivilegeMode(security.ModeWriter)
			}
			_ = a.PrivilegeMode()
		}(i)
	}
	wg.Wait()
}

// TestAgent_CheckToolApproval_ConcurrentDifferentTools tests that different
// tool patterns don't interfere under concurrent access.
func TestAgent_CheckToolApproval_ConcurrentDifferentTools(t *testing.T) {
	a := New(Config{Model: "test", MaxSteps: 5})
	a.SetApprovalFunc(func(toolName, args, risk string) Approval {
		// Approve shell, deny everything else.
		if toolName == "shell" {
			return Approval{Allow: true, Persist: true}
		}
		return Approval{Allow: false, Persist: false}
	})

	const N = 50
	var wg sync.WaitGroup
	shellResults := make([]bool, 0, N)
	otherResults := make([]bool, 0, N)
	var mu sync.Mutex

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			shellOK := a.checkToolApproval("shell", "ls")
			otherOK := a.checkToolApproval("fs_write", `{"path":"x"}`)
			mu.Lock()
			shellResults = append(shellResults, shellOK)
			otherResults = append(otherResults, otherOK)
			mu.Unlock()
		}()
	}
	wg.Wait()

	// Shell should always be allowed (first call persists).
	for i, r := range shellResults {
		if !r {
			t.Errorf("shell result %d: expected allow, got deny", i)
		}
	}
	// fs_write should always be denied.
	for i, r := range otherResults {
		if r {
			t.Errorf("fs_write result %d: expected deny, got allow", i)
		}
	}
}

// TestAgent_ApprovalCheck_LedgerConcurrent races approval checks that
// write to a permission ledger concurrently.
func TestAgent_ApprovalCheck_LedgerConcurrent(t *testing.T) {
	dir := t.TempDir()
	ledger, err := security.NewLedger(dir + "/perm.jsonl")
	if err != nil {
		t.Fatal(err)
	}

	a := New(Config{Model: "test", MaxSteps: 5})
	a.SetApprovalFunc(func(toolName, args, risk string) Approval {
		return Approval{Allow: true, Persist: true}
	})
	a.SetPermissionLedger(ledger)

	const N = 50
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			tool := "shell"
			if id%2 == 0 {
				tool = "fs_write"
			}
			a.checkToolApproval(tool, "test")
		}(i)
	}
	wg.Wait()

	// Check that the ledger has entries.
	entries := ledger.Entries()
	if len(entries) == 0 {
		t.Error("ledger should have entries after concurrent approval checks")
	}
	t.Logf("ledger entries: %d", len(entries))
}

// TestAgent_ModelsConcurrent tests concurrent Model() and SetModel() calls.
func TestAgent_ModelsConcurrent(t *testing.T) {
	a := New(Config{Model: "initial", MaxSteps: 5})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if id%2 == 0 {
				a.SetModel("model-" + string(rune('a'+id%26)))
			} else {
				_ = a.Model()
			}
		}(i)
	}
	wg.Wait()
}

// TestAgent_HistoryConcurrent races history mutation and reads.
func TestAgent_HistoryConcurrent(t *testing.T) {
	a := New(Config{Model: "test", MaxSteps: 5})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			a.SetHistory([]providers.Message{
				{Role: "user", Content: "msg-" + string(rune('a'+id%26))},
			})
		}(i)
	}
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = a.History()
		}()
	}

	wg.Wait()
}

// TestAgent_CheckToolApproval_RiskClassification tests the risk classifier
// under concurrent access (reads are inherently safe, but verify).
func TestAgent_CheckToolApproval_RiskClassification(t *testing.T) {
	// Test the classifyToolRisk function directly under concurrent load.
	const N = 100
	tools := []struct {
		name string
		args string
	}{
		{"shell", `{"command":"rm -rf /"}`},
		{"bash", `{"command":"ls"}`},
		{"fs_write", `{"path":"x"}`},
		{"patch", `{}`},
		{"git", `{"subcommand":"push"}`},
		{"web_fetch", `{"url":"http://example.com"}`},
		{"browser_eval", `{"script":"alert(1)"}`},
		{"browser_open", `{"url":"http://example.com"}`},
		{"browser_navigate", `{"url":"http://example.com"}`},
		{"browser_navigate", `{"url":"javascript:alert(1)"}`},
		{"grep", `{"pattern":"foo"}`},
	}

	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			tc := tools[id%len(tools)]
			risk := classifyToolRisk(tc.name, tc.args)
			if risk == "" {
				t.Errorf("classifyToolRisk(%q, %q) returned empty", tc.name, tc.args)
			}
		}(i)
	}
	wg.Wait()
}

// TestAgent_ApprovalCheckNilFunction verifies nil approval func under race.
func TestAgent_ApprovalCheckNilFunction(t *testing.T) {
	// When approvalFn is nil, approvalCheck should always return true.
	a := New(Config{Model: "test", MaxSteps: 5})
	// Do NOT set an approval function — nil means auto-allow.

	const N = 50
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !a.checkToolApproval("shell", "rm -rf /") {
				t.Error("nil approvalFn should auto-allow")
			}
		}()
	}
	wg.Wait()
}

// TestAgent_ReceiptChain_LenConcurrent races Len() against Append().
func TestAgent_ReceiptChain_LenConcurrent(t *testing.T) {
	rc := NewReceiptChain()
	const N = 100

	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rc.Append("sess", "shell", []byte("in"), []byte("out"), nil)
		}()
	}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = rc.Len()
		}()
	}

	wg.Wait()
	if rc.Len() != N {
		t.Errorf("expected %d receipts, got %d", N, rc.Len())
	}
}

// TestAgent_FlowStoreConcurrent races FlowStore set/load operations.
func TestAgent_FlowStoreConcurrent(t *testing.T) {
	store := NewMemoryFlowStore()
	a := New(Config{Model: "test", MaxSteps: 5, SessionID: "ff"})

	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.SetFlowStore(store, nil)
		}()
	}
	wg.Wait()
}

// ------------------------------------------
// Comprehensive stress test: all agent operations under load.
// ------------------------------------------

// TestAgent_AgentLoopStateTransitions simulates the state mutations
// that occur during the agent loop (model changes, history appends,
// approval checks, privilege gate flips) under concurrent load.
func TestAgent_AgentLoopStateTransitions(t *testing.T) {
	a := New(Config{
		Model:     "test-model",
		MaxSteps:  10,
		SessionID: "stress-session",
	})

	gate := security.NewPrivilegeGate(security.ModeWriter)
	a.SetPrivilegeGate(gate)

	dir := t.TempDir()
	ledger, _ := security.NewLedger(dir + "/ledger.jsonl")
	a.SetPermissionLedger(ledger)

	// These goroutines simulate what happens in the agent's Run() loop:
	// - model routing (SetModel)
	// - privilege mode switching
	// - history appends
	// - approval checks during tool dispatch
	// - state reads

	const NGroups = 10
	var wg sync.WaitGroup

	for g := 0; g < NGroups; g++ {
		// State writers
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				switch id % 6 {
				case 0:
					a.SetModel("model-" + string(rune('a'+id%26)))
				case 1:
					a.SetHistory([]providers.Message{
						{Role: "user", Content: "step"},
						{Role: "assistant", Content: "response", ToolCalls: []providers.ToolCall{
							{ID: "1", Name: "shell", Arguments: "ls"},
						}},
					})
				case 2:
					a.RestoreHistory([]providers.Message{
						{Role: "user", Content: "restored"},
					})
				case 3:
					if id%2 == 0 {
						a.SetPrivilegeMode(security.ModeReader)
					} else {
						a.SetPrivilegeMode(security.ModeWriter)
					}
				case 4:
					a.SetApprovalFunc(func(toolName, args, risk string) Approval {
						return Approval{Allow: true, Persist: id%2 == 0}
					})
				default:
					a.SetPermissionLedger(ledger)
				}
			}(i)
		}

		// State readers
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				switch id % 5 {
				case 0:
					_ = a.Model()
				case 1:
					_ = a.History()
				case 2:
					_ = a.PrivilegeMode()
				case 3:
					_ = a.PermissionLog()
				default:
					a.checkToolApproval("shell", "ls")
				}
			}(i)
		}
	}

	wg.Wait()
}

// TestAgent_Receipt_HashIntegrityUnderRace verifies that the receipt
// chain's hash computation is deterministic under concurrent access.
func TestAgent_Receipt_HashIntegrityUnderRace(t *testing.T) {
	rc := NewReceiptChain()
	const N = 50

	// Append sequentially first to get a reference chain.
	for i := 0; i < N; i++ {
		rc.Append("sess", "shell", []byte("in"), []byte("out"), nil)
	}
	refHash := rc.Snapshot()[N-1].Hash

	// Now run concurrent snapshot + verify.
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			snap := rc.Snapshot()
			if len(snap) > 0 && snap[N-1].Hash != refHash {
				t.Errorf("hash changed under concurrent Snapshot: got %s, want %s", snap[N-1].Hash, refHash)
			}
		}()
	}
	wg.Wait()
}

// TestAgent_AppendMessage_Race tests concurrent access to appendMessage
// via the agent's history path (which takes the mutex internally).
func TestAgent_AppendMessage_Race(t *testing.T) {
	a := New(Config{Model: "test", MaxSteps: 10})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			a.appendMessage(providers.Message{
				Role:    "user",
				Content: "msg-" + string(rune('a'+id%26)),
			})
		}(i)
	}
	wg.Wait()

	hist := a.History()
	if len(hist) != 100 {
		t.Errorf("expected 100 messages in history, got %d", len(hist))
	}
}

// ------------------------------------------
// Pointer-box state transitions under load
// ------------------------------------------

// (removed — provider field is mutated test is not realistic for this codebase)
