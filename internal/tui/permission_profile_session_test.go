package tui

import (
	"context"
	"testing"

	"github.com/dishant0406/KajiCode/internal/agent"
	"github.com/dishant0406/KajiCode/internal/sessions"
)

func TestSessionPermissionProfileCreateChangeAndResume(t *testing.T) {
	store := testSessionStore(t)
	m := newModel(context.Background(), Options{
		SessionStore:   store,
		PermissionMode: agent.PermissionModeReadOnly,
	})
	m, err := m.ensureActiveSession("test permissions")
	if err != nil {
		t.Fatalf("ensureActiveSession: %v", err)
	}
	if m.activeSession.PermissionProfile != string(agent.PermissionModeReadOnly) {
		t.Fatalf("created profile = %q, want read-only", m.activeSession.PermissionProfile)
	}

	m, err = m.setPermissionProfile(agent.PermissionModeBypassAll)
	if err != nil {
		t.Fatalf("setPermissionProfile: %v", err)
	}
	loaded, err := store.Get(m.activeSession.SessionID)
	if err != nil || loaded == nil {
		t.Fatalf("Get: session=%#v err=%v", loaded, err)
	}
	if loaded.PermissionProfile != string(agent.PermissionModeBypassAll) {
		t.Fatalf("persisted profile = %q, want bypass-all", loaded.PermissionProfile)
	}

	other := newModel(context.Background(), Options{
		SessionStore:   store,
		PermissionMode: agent.PermissionModeAskAll,
	})
	other, message := other.handleResumeCommand(m.activeSession.SessionID)
	if message != "" {
		t.Fatalf("handleResumeCommand message = %q", message)
	}
	if other.permissionMode != agent.PermissionModeBypassAll {
		t.Fatalf("resumed mode = %q, want bypass-all", other.permissionMode)
	}
}

func TestResumeLegacySessionKeepsCurrentPermissionProfile(t *testing.T) {
	store := testSessionStore(t)
	session, err := store.Create(sessions.CreateInput{Title: "legacy"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	m := newModel(context.Background(), Options{
		SessionStore:   store,
		PermissionMode: agent.PermissionModeReadWrite,
	})
	m, message := m.handleResumeCommand(session.SessionID)
	if message != "" {
		t.Fatalf("handleResumeCommand message = %q", message)
	}
	if m.permissionMode != agent.PermissionModeReadWrite {
		t.Fatalf("legacy resume mode = %q, want read-write", m.permissionMode)
	}
}
