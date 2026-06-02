package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// Batch F additions only. update_test.go covers the version-comparison
// + asset-picker surfaces. This file tests the NEW pieces shipped in
// Batch F:
//   - downloadToWithHash returns a verifiable SHA-256
//   - 4xx responses don't leave partial files on disk
//   - lookupExpectedSHA parses checksums.txt in both modes
//   - rollback handles the no-backup case gracefully

func TestDownloadToWithHash_HashesContent(t *testing.T) {
	payload := []byte("hello overkill")
	wantSum := sha256.Sum256(payload)
	wantHex := hex.EncodeToString(wantSum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	dst := filepath.Join(t.TempDir(), "binary")
	gotHash, err := downloadToWithHash(context.Background(), srv.URL, dst)
	if err != nil {
		t.Fatal(err)
	}
	if gotHash != wantHex {
		t.Errorf("hash mismatch: got %s, want %s", gotHash, wantHex)
	}
	data, _ := os.ReadFile(dst)
	if string(data) != string(payload) {
		t.Errorf("file content mismatch")
	}
}

func TestDownloadToWithHash_4xxLeavesNoPartialFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	dst := filepath.Join(t.TempDir(), "binary")
	if _, err := downloadToWithHash(context.Background(), srv.URL, dst); err == nil {
		t.Error("404 should surface as error")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Errorf("4xx must not leave a partial file at %s", dst)
	}
}

func TestLookupExpectedSHA_FindsAssetInTwoSpaceFormat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(
			"deadbeefdeadbeef  overkill-linux-amd64\n" +
				"cafe1234cafe1234  overkill-darwin-arm64\n",
		))
	}))
	defer srv.Close()

	rel := &release{Assets: []releaseAsset{
		{Name: "overkill-darwin-arm64", BrowserDownloadURL: "https://example.com/bin"},
		{Name: "checksums.txt", BrowserDownloadURL: srv.URL},
	}}
	got := lookupExpectedSHA(rel, "overkill-darwin-arm64")
	if got != "cafe1234cafe1234" {
		t.Errorf("got %q, want cafe1234cafe1234", got)
	}
}

func TestLookupExpectedSHA_StripsBinaryModeMarker(t *testing.T) {
	// sha256sum binary mode prefixes filenames with '*'.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("deadbeef *overkill-linux-amd64\n"))
	}))
	defer srv.Close()

	rel := &release{Assets: []releaseAsset{
		{Name: "overkill-linux-amd64"},
		{Name: "checksums.txt", BrowserDownloadURL: srv.URL},
	}}
	got := lookupExpectedSHA(rel, "overkill-linux-amd64")
	if got != "deadbeef" {
		t.Errorf("binary-mode marker stripping failed: got %q", got)
	}
}

func TestLookupExpectedSHA_NoChecksumsFileReturnsEmpty(t *testing.T) {
	// No checksums.txt in the release → empty result means "skip
	// verification". The install path treats this as an unverified
	// install — the correct fallback for releases without checksums.
	rel := &release{Assets: []releaseAsset{
		{Name: "overkill-linux-amd64"},
	}}
	if got := lookupExpectedSHA(rel, "overkill-linux-amd64"); got != "" {
		t.Errorf("missing checksums.txt should return empty, got %q", got)
	}
}

func TestLookupExpectedSHA_UnknownAssetReturnsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("deadbeef  other-binary\n"))
	}))
	defer srv.Close()

	rel := &release{Assets: []releaseAsset{
		{Name: "checksums.txt", BrowserDownloadURL: srv.URL},
	}}
	if got := lookupExpectedSHA(rel, "missing-asset"); got != "" {
		t.Errorf("unknown asset should return empty, got %q", got)
	}
}

func TestLookupExpectedSHA_SkipsCommentsAndBlankLines(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(
			"# Overkill release checksums\n" +
				"\n" +
				"abc123  overkill-linux-amd64\n",
		))
	}))
	defer srv.Close()

	rel := &release{Assets: []releaseAsset{
		{Name: "overkill-linux-amd64"},
		{Name: "checksums.txt", BrowserDownloadURL: srv.URL},
	}}
	got := lookupExpectedSHA(rel, "overkill-linux-amd64")
	if got != "abc123" {
		t.Errorf("comment-stripping failed: got %q", got)
	}
}

func TestRollback_NoBackupIsNoOp(t *testing.T) {
	// We can't easily mock os.Executable; pin that runUpdateRollback
	// exits cleanly when no .bak exists. Most users hit this when
	// they run --rollback without ever having installed.
	if err := runUpdateRollback(); err != nil {
		t.Errorf("rollback with no backup should not error: %v", err)
	}
}
