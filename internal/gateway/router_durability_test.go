package gateway

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRouterConcurrentBindingsSurviveReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router.json")
	router, err := NewSessionRouter(path)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errors := make(chan error, 80)
	for index := range 80 {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			id := fmt.Sprint(index)
			errors <- router.Bind("bridge:fixture", id, "", "session-"+id)
		}(index)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	restored, err := NewSessionRouter(path)
	if err != nil {
		t.Fatal(err)
	}
	for index := range 80 {
		id := fmt.Sprint(index)
		got, _ := restored.Resolve("bridge:fixture", id, "", "")
		if got != "session-"+id {
			t.Fatalf("binding %s lost: %s", id, got)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("temporary files leaked: %v", entries)
	}
}
func TestRouterPersistenceFailureDoesNotCommitMemoryOrDispatch(t *testing.T) {
	base := t.TempDir()
	block := filepath.Join(base, "block")
	path := filepath.Join(block, "router.json")
	router, err := NewSessionRouter(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(block, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := router.Bind("bridge:fixture", "chat", "", "not-saved"); err == nil {
		t.Fatal("expected bind persistence error")
	}
	if got, _ := router.Resolve("bridge:fixture", "chat", "", ""); got != "" {
		t.Fatalf("failed bind became visible: %s", got)
	}
	if err := router.Follow("bridge:fixture", "chat", "not-saved"); err == nil {
		t.Fatal("expected follow persistence error")
	}
	if router.FollowTarget("bridge:fixture", "chat") != "" {
		t.Fatal("failed follow became visible")
	}
	ag := &fakeAgent{}
	dispatcher := NewDispatcher(ag, router)
	reply := &fakeReply{}
	dispatcher.Handle(context.Background(), Inbound{Channel: "bridge:fixture", ChatKey: "chat", From: "owner", Text: "hello"}, reply)
	ag.mu.Lock()
	input := ag.gotInput
	ag.mu.Unlock()
	if input != "" {
		t.Fatal("agent ran despite failed router persistence")
	}
	reply.mu.Lock()
	var out strings.Builder
	for _, frame := range reply.frames {
		out.WriteString(frame.text)
	}
	reply.mu.Unlock()
	if !strings.Contains(out.String(), "routing failed") {
		t.Fatalf("persistence failure not surfaced: %s", out.String())
	}
	if err := os.Remove(block); err != nil {
		t.Fatal(err)
	}
	if err := router.Bind("bridge:fixture", "chat", "", "saved"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewSessionRouter(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := reloaded.Resolve("bridge:fixture", "chat", "", ""); got != "saved" {
		t.Fatal(got)
	}
}
func TestRouterCorruptFileIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router.json")
	if err := os.WriteFile(path, []byte("invalid json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewSessionRouter(path); err == nil {
		t.Fatal("corrupt routing must not silently create a different session")
	}
}

func TestRouterEqualTimestampRecencyPersistsBindAndTouchOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router.json")
	router, err := NewSessionRouter(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"first", "second", "third"} {
		if err := router.Bind("fixture", id, "", id); err != nil {
			t.Fatal(err)
		}
	}
	if err := router.Touch("fixture", "first", ""); err != nil {
		t.Fatal(err)
	}
	router.mu.Lock()
	for key, entry := range router.data.Bindings {
		entry.Updated = time.Unix(1, 0)
		router.data.Bindings[key] = entry
	}
	err = router.persistLocked()
	router.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := NewSessionRouter(path)
	if err != nil {
		t.Fatal(err)
	}
	for repeat := range 20 {
		rows := restored.Recent(3)
		for index, want := range []string{"first", "third", "second"} {
			if rows[index].SessionID != want {
				t.Fatalf("iteration %d order: %v", repeat, rows)
			}
		}
	}
}
func TestRouterLegacyEqualTimestampsHaveDeterministicTieOrder(t *testing.T) {
	router, err := NewSessionRouter("")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"z", "a", "m"} {
		router.data.Bindings[bindKey("fixture", id, "")] = binding{SessionID: id, Updated: time.Unix(1, 0)}
	}
	for repeat := range 20 {
		rows := router.Recent(3)
		for index, want := range []string{"a", "m", "z"} {
			if rows[index].SessionID != want {
				t.Fatalf("iteration %d order: %v", repeat, rows)
			}
		}
	}
}
