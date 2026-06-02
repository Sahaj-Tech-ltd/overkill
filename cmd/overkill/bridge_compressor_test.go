package main

import (
	"context"
	"testing"
)

// TestBridgeCompressorAdapter_NilSafe verifies the adapter's contract
// when the inner client is missing — the agent's SetPromptCompressor
// pipeline expects a non-error fall-through to the original prompt.
func TestBridgeCompressorAdapter_NilSafe(t *testing.T) {
	a := &bridgeCompressorAdapter{}
	got, saved, err := a.Compress(context.Background(), "hello")
	if err != nil {
		t.Errorf("nil client should not error: %v", err)
	}
	if got != "hello" {
		t.Errorf("nil client should pass prompt through, got %q", got)
	}
	if saved != 0 {
		t.Errorf("nil client should report 0 saved, got %d", saved)
	}
}

func TestBridgeCompressorAdapter_NilReceiver(t *testing.T) {
	var a *bridgeCompressorAdapter
	got, _, err := a.Compress(context.Background(), "x")
	if err != nil {
		t.Errorf("nil receiver should not error: %v", err)
	}
	if got != "x" {
		t.Errorf("nil receiver should pass prompt through, got %q", got)
	}
}
