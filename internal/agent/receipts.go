// Package agent — cryptographic tool receipts (master plan §7.1
// Emergency Controls). Every tool call the agent makes produces one
// receipt; receipts form a hash chain so any single tampered entry
// invalidates the rest. The chain is what makes a post-incident audit
// trustworthy: if the receipt for "shell rm -rf /important" exists,
// the agent actually ran it; if someone edits the ledger to hide it,
// Verify() catches the break.
//
// Implementation notes:
//
//   - SHA-256 used for the chain hash. Not "speed at any cost" — we
//     only chain on completion of each tool call, so the cost is
//     negligible compared to the LLM call that produced it.
//   - PrevHash of the first receipt is the empty string by design,
//     not a random nonce. Replaying a chain from scratch should be
//     bit-identical to its original creation so VerifyChain can be
//     run by any auditor with the same receipts.
//   - Receipts capture INPUT + OUTPUT hashes, not the bodies. Bodies
//     can be huge (file contents); hashes give us tamper detection
//     without bloating the chain. If a user wants the bodies they
//     can also persist a separate journal — receipts are the
//     verifiable index.
package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Receipt is one row in the tool-call audit chain. Hash is over the
// canonicalized JSON of every other field, including PrevHash. The
// chain invariant: Receipt[N].PrevHash == Receipt[N-1].Hash.
type Receipt struct {
	Seq       int       `json:"seq"`
	SessionID string    `json:"session_id"`
	ToolName  string    `json:"tool_name"`
	InputHash string    `json:"input_hash"`
	OutputHash string   `json:"output_hash"`
	Err       string    `json:"error,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	PrevHash  string    `json:"prev_hash"`
	Hash      string    `json:"hash"`
}

// ReceiptChain is the append-only ledger. The first receipt has
// PrevHash = "" by convention; every subsequent receipt's PrevHash
// equals the previous receipt's Hash. Safe for concurrent Append
// calls — internal mutex serializes hash computation.
type ReceiptChain struct {
	mu       sync.Mutex
	receipts []Receipt
}

// NewReceiptChain returns an empty chain. The first Append starts the
// hash chain with PrevHash="".
func NewReceiptChain() *ReceiptChain {
	return &ReceiptChain{}
}

// Append records one tool call. Input/output payloads are hashed
// (SHA-256) so the chain captures their identity without their bulk.
// Returns the appended receipt so callers can persist it externally
// (e.g. to a journal) or display it.
//
// Thread-safe: chain mutations serialize under an internal mutex.
// Hashing is fast enough that the lock-hold time is microseconds
// even for large input/output blobs.
func (c *ReceiptChain) Append(sessionID, toolName string, input, output []byte, err error) Receipt {
	c.mu.Lock()
	defer c.mu.Unlock()

	prev := ""
	if len(c.receipts) > 0 {
		prev = c.receipts[len(c.receipts)-1].Hash
	}
	r := Receipt{
		Seq:        len(c.receipts),
		SessionID:  sessionID,
		ToolName:   toolName,
		InputHash:  hashBytes(input),
		OutputHash: hashBytes(output),
		Timestamp:  time.Now().UTC(),
		PrevHash:   prev,
	}
	if err != nil {
		r.Err = err.Error()
	}
	r.Hash = computeReceiptHash(r)
	c.receipts = append(c.receipts, r)
	return r
}

// Snapshot returns a copy of the chain. The receipts slice is copied
// so the caller can iterate without holding the lock.
func (c *ReceiptChain) Snapshot() []Receipt {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Receipt, len(c.receipts))
	copy(out, c.receipts)
	return out
}

// Len reports the current chain length.
func (c *ReceiptChain) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.receipts)
}

// VerifyChain walks receipts in order and reports the first index
// where the chain is broken — either a hash mismatch on the receipt
// itself OR a PrevHash that doesn't match the prior receipt's Hash.
// Returns (-1, nil) when the chain is intact, (idx, err) when broken.
// An empty chain is intact by definition.
func VerifyChain(receipts []Receipt) (int, error) {
	if len(receipts) == 0 {
		return -1, nil
	}
	for i, r := range receipts {
		// 1. Self-consistency: re-compute Hash from the receipt's
		// own fields. A mismatch means the receipt was edited.
		want := computeReceiptHash(r)
		if want != r.Hash {
			return i, fmt.Errorf("receipt %d: hash mismatch (recomputed %s != stored %s)", i, want, r.Hash)
		}
		// 2. Chain linkage: PrevHash must equal the prior Hash.
		expectedPrev := ""
		if i > 0 {
			expectedPrev = receipts[i-1].Hash
		}
		if r.PrevHash != expectedPrev {
			return i, fmt.Errorf("receipt %d: prev_hash %s does not chain from receipt %d (%s)", i, r.PrevHash, i-1, expectedPrev)
		}
		// 3. Sequence: receipts are 0-indexed and contiguous.
		if r.Seq != i {
			return i, fmt.Errorf("receipt %d: seq %d does not match position", i, r.Seq)
		}
	}
	return -1, nil
}

// computeReceiptHash hashes every receipt field EXCEPT Hash itself
// (chicken-and-egg). Canonicalize via JSON marshaling of a parallel
// shape so field order is deterministic.
func computeReceiptHash(r Receipt) string {
	body := struct {
		Seq        int       `json:"seq"`
		SessionID  string    `json:"session_id"`
		ToolName   string    `json:"tool_name"`
		InputHash  string    `json:"input_hash"`
		OutputHash string    `json:"output_hash"`
		Err        string    `json:"error,omitempty"`
		Timestamp  time.Time `json:"timestamp"`
		PrevHash   string    `json:"prev_hash"`
	}{
		Seq:        r.Seq,
		SessionID:  r.SessionID,
		ToolName:   r.ToolName,
		InputHash:  r.InputHash,
		OutputHash: r.OutputHash,
		Err:        r.Err,
		Timestamp:  r.Timestamp,
		PrevHash:   r.PrevHash,
	}
	// json.Marshal on a struct with explicit field order produces
	// stable bytes — the package guarantees field-declaration order.
	// We tag with omitempty on Err to match the receipt's own JSON
	// surface so an externally-replayed chain hashes the same way.
	b, _ := json.Marshal(body)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// hashBytes is a SHA-256 hex of b. nil and empty produce the same
// hash (sha256 of empty) so missing payloads chain identically.
func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
