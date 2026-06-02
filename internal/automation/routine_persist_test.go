package automation

import (
	"testing"
	"time"
)

func TestRoutineEngine_RegisterPersists(t *testing.T) {
	store := NewMemoryRoutineStore()
	e, err := NewRoutineEngineWithStore(noopFire, store)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Register(&Routine{ID: "r1", Name: "test", Trigger: "build_success", Action: "echo hi", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	loaded, _ := store.Load()
	if len(loaded) != 1 || loaded[0].ID != "r1" {
		t.Errorf("Register did not persist: %+v", loaded)
	}
}

func TestRoutineEngine_UnregisterRemovesFromStore(t *testing.T) {
	store := NewMemoryRoutineStore()
	e, _ := NewRoutineEngineWithStore(noopFire, store)
	_ = e.Register(&Routine{ID: "r1", Trigger: "x", Enabled: true})
	if !e.Unregister("r1") {
		t.Fatal("unregister returned false")
	}
	loaded, _ := store.Load()
	if len(loaded) != 0 {
		t.Errorf("Unregister did not clean up store: %+v", loaded)
	}
}

func TestRoutineEngine_LoadRestoresFromStore(t *testing.T) {
	store := NewMemoryRoutineStore()
	_ = store.Save(&Routine{ID: "r1", Trigger: "boot", Action: "echo", Enabled: true, FireCount: 3})

	e, err := NewRoutineEngineWithStore(noopFire, store)
	if err != nil {
		t.Fatal(err)
	}
	got := e.List()
	if len(got) != 1 {
		t.Fatalf("expected 1 routine restored, got %d", len(got))
	}
	if got[0].FireCount != 3 {
		t.Errorf("FireCount not preserved: %d", got[0].FireCount)
	}
}

func TestRoutineEngine_HandleEventPersistsCooldown(t *testing.T) {
	store := NewMemoryRoutineStore()
	e, _ := NewRoutineEngineWithStore(noopFire, store)
	_ = e.Register(&Routine{
		ID:       "r1",
		Trigger:  "tool_call",
		Action:   "echo hi",
		Enabled:  true,
		Cooldown: time.Hour,
	})
	fired, err := e.HandleEvent("tool_call")
	if err != nil {
		t.Fatal(err)
	}
	if !fired {
		t.Fatal("expected routine to fire")
	}
	loaded, _ := store.Load()
	if len(loaded) != 1 {
		t.Fatalf("expected persisted entry, got %d", len(loaded))
	}
	if loaded[0].FireCount != 1 {
		t.Errorf("FireCount not persisted: %d", loaded[0].FireCount)
	}
	if loaded[0].LastFired.IsZero() {
		t.Error("LastFired should be persisted")
	}
}

func TestRoutineEngine_EnableDisablePersist(t *testing.T) {
	store := NewMemoryRoutineStore()
	e, _ := NewRoutineEngineWithStore(noopFire, store)
	_ = e.Register(&Routine{ID: "r1", Trigger: "x", Enabled: false})
	if err := e.Enable("r1"); err != nil {
		t.Fatal(err)
	}
	loaded, _ := store.Load()
	if !loaded[0].Enabled {
		t.Error("Enable did not persist")
	}
	if err := e.Disable("r1"); err != nil {
		t.Fatal(err)
	}
	loaded, _ = store.Load()
	if loaded[0].Enabled {
		t.Error("Disable did not persist")
	}
}

func TestRoutineEngine_NilStoreIsAllowed(t *testing.T) {
	e, err := NewRoutineEngineWithStore(noopFire, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Register(&Routine{ID: "r1", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if len(e.List()) != 1 {
		t.Errorf("nil store should still work in-memory")
	}
}

func noopFire(action string) (string, error) {
	return "ran: " + action, nil
}
