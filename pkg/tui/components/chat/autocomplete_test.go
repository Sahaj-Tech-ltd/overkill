package chat

import "testing"

func entries() []AutocompleteEntry {
	return []AutocompleteEntry{
		{ID: "help"},
		{ID: "model"},
		{ID: "compact"},
		{ID: "config"},
		{ID: "stash"},
	}
}

func TestAutocomplete_HiddenWithoutSlash(t *testing.T) {
	a := NewAutocomplete(entries())
	a.Update("hello")
	if a.Visible() {
		t.Fatal("autocomplete should be hidden when text isn't slash-prefixed")
	}
}

func TestAutocomplete_FilterSubstring(t *testing.T) {
	a := NewAutocomplete(entries())
	a.Update("/co")
	got := a.Entries()
	want := []string{"compact", "config"}
	if len(got) != len(want) {
		t.Fatalf("got %d matches, want %d (%v)", len(got), len(want), got)
	}
	for i := range got {
		if got[i].ID != want[i] {
			t.Fatalf("entry %d: got %q want %q", i, got[i].ID, want[i])
		}
	}
}

func TestAutocomplete_EmptySlashShowsAll(t *testing.T) {
	a := NewAutocomplete(entries())
	a.Update("/")
	if len(a.Entries()) != len(entries()) {
		t.Fatalf("expected all entries on bare /, got %d", len(a.Entries()))
	}
}

func TestAutocomplete_Completion(t *testing.T) {
	a := NewAutocomplete(entries())
	a.Update("/co")
	a.Move(1)
	c, ok := a.Completion()
	if !ok {
		t.Fatal("expected completion to be available")
	}
	if c != "/config" {
		t.Fatalf("got completion %q want /config", c)
	}
}

func TestAutocomplete_NoMatches(t *testing.T) {
	a := NewAutocomplete(entries())
	a.Update("/xyz")
	if a.Visible() {
		t.Fatal("expected hidden when no matches")
	}
	if _, ok := a.Completion(); ok {
		t.Fatal("expected no completion when no matches")
	}
}

func TestAutocomplete_MoveClamps(t *testing.T) {
	a := NewAutocomplete(entries())
	a.Update("/")
	a.Move(-5)
	if a.Cursor() != 0 {
		t.Fatalf("cursor should clamp to 0, got %d", a.Cursor())
	}
	a.Move(100)
	if a.Cursor() != len(a.Entries())-1 {
		t.Fatalf("cursor should clamp to last, got %d", a.Cursor())
	}
}

func TestAutocomplete_HideClears(t *testing.T) {
	a := NewAutocomplete(entries())
	a.Update("/")
	a.Hide()
	if a.Visible() {
		t.Fatal("expected hidden after Hide()")
	}
	if len(a.Entries()) != 0 {
		t.Fatal("expected empty entries after Hide()")
	}
}
