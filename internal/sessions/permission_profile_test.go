package sessions

import "testing"

func TestPermissionProfilePersistsUpdatesAndForks(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir()})
	parent, err := store.Create(CreateInput{
		SessionID:         "permission_parent",
		PermissionProfile: "read-only",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if parent.PermissionProfile != "read-only" {
		t.Fatalf("PermissionProfile = %q, want read-only", parent.PermissionProfile)
	}

	updated, err := store.UpdatePermissionProfile(parent.SessionID, "read-write")
	if err != nil {
		t.Fatalf("UpdatePermissionProfile: %v", err)
	}
	if updated.PermissionProfile != "read-write" {
		t.Fatalf("updated PermissionProfile = %q, want read-write", updated.PermissionProfile)
	}
	loaded, err := store.Get(parent.SessionID)
	if err != nil || loaded == nil {
		t.Fatalf("Get: session=%#v err=%v", loaded, err)
	}
	if loaded.PermissionProfile != "read-write" {
		t.Fatalf("loaded PermissionProfile = %q, want read-write", loaded.PermissionProfile)
	}

	fork, err := store.Fork(parent.SessionID, ForkInput{SessionID: "permission_fork"})
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if fork.PermissionProfile != "read-write" {
		t.Fatalf("fork PermissionProfile = %q, want read-write", fork.PermissionProfile)
	}
}

func TestLegacySessionHasEmptyPermissionProfile(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir()})
	session, err := store.Create(CreateInput{SessionID: "permission_legacy"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if session.PermissionProfile != "" {
		t.Fatalf("PermissionProfile = %q, want empty legacy fallback", session.PermissionProfile)
	}
}
