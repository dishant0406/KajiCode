package sessions

import (
	"testing"
)

func TestSessionStateRoundTrip(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir()})

	// Missing state: empty map, no error.
	got, err := store.ReadSessionState("sess-1")
	if err != nil {
		t.Fatalf("ReadSessionState(missing): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty state, got %+v", got)
	}

	// Write then read back.
	state := SessionState{
		"todo": []map[string]any{
			{"content": "one", "status": "in_progress"},
			{"content": "two", "status": "pending"},
		},
	}
	if err := store.WriteSessionState("sess-1", state); err != nil {
		t.Fatalf("WriteSessionState: %v", err)
	}
	got, err = store.ReadSessionState("sess-1")
	if err != nil {
		t.Fatalf("ReadSessionState(after write): %v", err)
	}
	todos, ok := got["todo"].([]any)
	if !ok {
		t.Fatalf("expected todo to be []any, got %T", got["todo"])
	}
	if len(todos) != 2 {
		t.Fatalf("expected 2 todos, got %d", len(todos))
	}

	// Overwrite completely.
	if err := store.WriteSessionState("sess-1", SessionState{"other": "value"}); err != nil {
		t.Fatalf("WriteSessionState(overwrite): %v", err)
	}
	got, err = store.ReadSessionState("sess-1")
	if err != nil {
		t.Fatalf("ReadSessionState(after overwrite): %v", err)
	}
	if _, exists := got["todo"]; exists {
		t.Fatalf("expected todo to be removed, got %+v", got)
	}

	// Empty session id is a no-op.
	if err := store.WriteSessionState("", SessionState{"x": "y"}); err != nil {
		t.Fatalf("WriteSessionState empty id: %v", err)
	}
}

func TestSessionStateIsolatesSessions(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir()})
	if err := store.WriteSessionState("sess-a", SessionState{"k": "a"}); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteSessionState("sess-b", SessionState{"k": "b"}); err != nil {
		t.Fatal(err)
	}
	got, err := store.ReadSessionState("sess-a")
	if err != nil || got["k"] != "a" {
		t.Fatalf("sess-a = %v, %v", got, err)
	}
	got, err = store.ReadSessionState("sess-b")
	if err != nil || got["k"] != "b" {
		t.Fatalf("sess-b = %v, %v", got, err)
	}
}
