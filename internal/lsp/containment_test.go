package lsp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWorkspacePathSymlinkContainment(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "absent"), filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "real"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "inside")); err != nil {
		t.Fatal(err)
	}
	client := &Client{rootURI: pathToURI(root)}
	for _, name := range []string{"escape/secret", "escape/new/absent.go", "dangling", "../outside"} {
		if err := client.validatePath(name); err == nil {
			t.Errorf("accepted %s", name)
		}
	}
	for _, name := range []string{"new/absent.go", "inside/new.go", root} {
		if err := client.validatePath(name); err != nil {
			t.Errorf("rejected %s: %v", name, err)
		}
	}
	if err := (&Client{}).validatePath("anything"); err == nil {
		t.Fatal("prestart must fail closed")
	}
}

func TestWorkspaceLocationResultsRejectEscapes(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	client := &Client{rootURI: pathToURI(root)}
	for _, uri := range []string{pathToURI(filepath.Join(root, "escape", "secret")), pathToURI(filepath.Join(outside, "secret")), "https://example.test/code", "file://remote" + root + "/file", "file:///etc/passwd?query=ignored"} {
		if err := client.validateLocations([]Location{{URI: uri}}); err == nil {
			t.Errorf("accepted %s", uri)
		}
	}
	if err := client.validateLocations([]Location{{URI: pathToURI(filepath.Join(root, "new.go"))}}); err != nil {
		t.Fatal(err)
	}
}

func TestLSPQueryRejectsSymlinkBeforeSending(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	client, cleanup := newConnectedFakeClient(t, ctx)
	defer cleanup()
	client.rootURI = pathToURI(root)
	for _, call := range []func() error{
		func() error { _, err := client.Definition(ctx, "escape/secret", 0, 0); return err },
		func() error { _, err := client.References(ctx, "escape/secret", 0, 0); return err },
		func() error { _, err := client.Hover(ctx, "escape/secret", 0, 0); return err },
		func() error { _, err := client.DocumentSymbols(ctx, "escape/secret"); return err },
	} {
		if err := call(); err == nil {
			t.Fatal("accepted escaped query")
		}
	}
	// The fake server returns /tmp definitions: enforce containment on actual
	// decoded wire responses too, even for an otherwise valid query.
	if _, err := client.Definition(ctx, "valid.go", 0, 0); err == nil {
		t.Fatal("accepted external definition")
	}
	if _, err := client.References(ctx, "valid.go", 0, 0); err == nil {
		t.Fatal("accepted external references")
	}
	if _, err := client.DocumentSymbols(ctx, "valid.go"); err == nil {
		t.Fatal("accepted external document symbol")
	}
	if _, err := client.WorkspaceSymbols(ctx, "symbol"); err == nil {
		t.Fatal("accepted external workspace symbol")
	}
}
