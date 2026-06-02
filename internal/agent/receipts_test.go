package agent

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestReceiptChain_FirstReceiptHasEmptyPrev(t *testing.T) {
	c := NewReceiptChain()
	r := c.Append("sess-1", "shell", []byte(`{"cmd":"ls"}`), []byte("output"), nil)
	if r.PrevHash != "" {
		t.Errorf("first receipt PrevHash should be empty, got %q", r.PrevHash)
	}
	if r.Hash == "" {
		t.Error("first receipt Hash empty")
	}
	if r.Seq != 0 {
		t.Errorf("Seq: %d", r.Seq)
	}
}

func TestReceiptChain_ChainsAcrossAppends(t *testing.T) {
	c := NewReceiptChain()
	a := c.Append("s", "shell", []byte("in1"), []byte("out1"), nil)
	b := c.Append("s", "fs_read", []byte("in2"), []byte("out2"), nil)
	if b.PrevHash != a.Hash {
		t.Errorf("chain broken: b.PrevHash=%s a.Hash=%s", b.PrevHash, a.Hash)
	}
	if b.Seq != 1 {
		t.Errorf("Seq: %d", b.Seq)
	}
}

func TestReceiptChain_HashesInputAndOutput(t *testing.T) {
	c := NewReceiptChain()
	r := c.Append("s", "shell", []byte("foo"), []byte("bar"), nil)
	// Hashes are 64 hex chars (SHA-256).
	if len(r.InputHash) != 64 || len(r.OutputHash) != 64 {
		t.Errorf("hashes wrong length: in=%d out=%d", len(r.InputHash), len(r.OutputHash))
	}
	// Different payloads produce different hashes.
	if r.InputHash == r.OutputHash {
		t.Errorf("foo and bar should hash differently")
	}
}

func TestReceiptChain_RecordsToolError(t *testing.T) {
	c := NewReceiptChain()
	r := c.Append("s", "shell", []byte("in"), nil, errors.New("boom"))
	if r.Err != "boom" {
		t.Errorf("Err: %q", r.Err)
	}
}

func TestVerifyChain_IntactChain(t *testing.T) {
	c := NewReceiptChain()
	for i := 0; i < 5; i++ {
		c.Append("s", "shell", []byte("in"), []byte("out"), nil)
	}
	idx, err := VerifyChain(c.Snapshot())
	if idx != -1 || err != nil {
		t.Errorf("intact chain should verify: idx=%d err=%v", idx, err)
	}
}

func TestVerifyChain_EmptyIsIntact(t *testing.T) {
	idx, err := VerifyChain(nil)
	if idx != -1 || err != nil {
		t.Errorf("empty chain: idx=%d err=%v", idx, err)
	}
}

func TestVerifyChain_TamperedHashDetected(t *testing.T) {
	c := NewReceiptChain()
	c.Append("s", "shell", []byte("a"), []byte("b"), nil)
	c.Append("s", "shell", []byte("c"), []byte("d"), nil)
	snap := c.Snapshot()
	// Edit the second receipt's tool name.
	snap[1].ToolName = "evil"
	idx, err := VerifyChain(snap)
	if idx != 1 {
		t.Errorf("expected break at index 1, got %d", idx)
	}
	if err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Errorf("err: %v", err)
	}
}

func TestVerifyChain_BrokenLinkageDetected(t *testing.T) {
	c := NewReceiptChain()
	c.Append("s", "shell", []byte("a"), []byte("b"), nil)
	c.Append("s", "shell", []byte("c"), []byte("d"), nil)
	c.Append("s", "shell", []byte("e"), []byte("f"), nil)
	snap := c.Snapshot()
	// Replace the entire middle receipt with a freshly-computed but
	// disconnected one — its self-hash will be valid, but PrevHash
	// won't link from receipt 0.
	snap[1].PrevHash = "deadbeef"
	snap[1].Hash = computeReceiptHash(snap[1])
	idx, err := VerifyChain(snap)
	if idx != 1 {
		t.Errorf("expected break at index 1, got %d", idx)
	}
	if err == nil || !strings.Contains(err.Error(), "prev_hash") {
		t.Errorf("err: %v", err)
	}
}

func TestVerifyChain_DeletedReceiptDetected(t *testing.T) {
	c := NewReceiptChain()
	c.Append("s", "shell", []byte("a"), []byte("b"), nil)
	c.Append("s", "shell", []byte("c"), []byte("d"), nil)
	c.Append("s", "shell", []byte("e"), []byte("f"), nil)
	snap := c.Snapshot()
	// Remove the middle row. Receipt 2 still has the original
	// PrevHash pointing at receipt 1's Hash — but in the new
	// shortened slice, receipts[1] is the old receipts[2], so the
	// chain is broken at index 1.
	tampered := []Receipt{snap[0], snap[2]}
	idx, err := VerifyChain(tampered)
	if idx == -1 {
		t.Errorf("deletion should break the chain, got intact (err=%v)", err)
	}
}

func TestReceiptChain_ConcurrentAppendIsSafe(t *testing.T) {
	c := NewReceiptChain()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Append("s", "shell", []byte("in"), []byte("out"), nil)
		}()
	}
	wg.Wait()
	if got := c.Len(); got != 50 {
		t.Errorf("len: %d", got)
	}
	if idx, err := VerifyChain(c.Snapshot()); idx != -1 {
		t.Errorf("concurrent append produced broken chain at %d: %v", idx, err)
	}
}

func TestAgent_EStopChannel(t *testing.T) {
	a := New(Config{Model: "test", MaxSteps: 5})
	ch := a.StopCh()
	select {
	case <-ch:
		t.Error("stop channel should be open before EStop")
	default:
	}
	a.EStop()
	select {
	case <-ch:
		// good
	default:
		t.Error("stop channel should be closed after EStop")
	}
	// Double EStop must not panic.
	a.EStop()
}

func TestAgent_ResetStopAfterEStop(t *testing.T) {
	a := New(Config{Model: "test", MaxSteps: 5})
	a.EStop()
	a.ResetStop()
	ch := a.StopCh()
	select {
	case <-ch:
		t.Error("stop channel should be reopened after Reset")
	default:
	}
}

func TestAgent_ResetStopWhileOpenIsNoOp(t *testing.T) {
	a := New(Config{Model: "test", MaxSteps: 5})
	// Capture the current channel; ResetStop on an open channel
	// should be a no-op (NOT replace the channel).
	ch1 := a.StopCh()
	a.ResetStop()
	ch2 := a.StopCh()
	if ch1 != ch2 {
		t.Error("ResetStop on open channel should not replace it")
	}
}

func TestAgent_NilEStopSafe(t *testing.T) {
	var a *Agent
	a.EStop() // should not panic
	if ch := a.StopCh(); ch == nil {
		t.Error("nil agent should return a non-nil never-closing channel")
	}
}
