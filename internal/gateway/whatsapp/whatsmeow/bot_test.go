package whatsmeow

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewBot_StripsPlusFromAllowList(t *testing.T) {
	bot := NewBot("/tmp/x.db",
		[]string{"+14155551234", "14155555678", "  +1415  "}, nil)
	if !bot.AllowedFrom["14155551234"] {
		t.Error("leading + should be stripped")
	}
	if !bot.AllowedFrom["14155555678"] {
		t.Error("plain number kept")
	}
}

func TestBot_Name(t *testing.T) {
	if got := (&Bot{}).Name(); got != "whatsapp-whatsmeow" {
		t.Errorf("name: %q", got)
	}
}

func TestRun_NoStorePathErrors(t *testing.T) {
	bot := NewBot("", nil, nil)
	err := bot.Run(context.Background())
	if err == nil {
		t.Error("missing store path should error")
	}
}

func TestRun_MissingStoreFileErrors(t *testing.T) {
	// store_path that doesn't exist on disk — we want a clear pair-
	// flow error, not a SQL panic.
	bot := NewBot(filepath.Join(t.TempDir(), "does-not-exist.db"), nil, nil)
	err := bot.Run(context.Background())
	if err == nil {
		t.Fatal("expected error for missing store file")
	}
	if !strings.Contains(err.Error(), "pair") {
		t.Errorf("error should point at the pair command, got %q", err.Error())
	}
}
