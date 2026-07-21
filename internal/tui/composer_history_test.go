package tui

import (
	"context"
	"testing"

	"github.com/dishant0406/KajiCode/internal/sessions"
)

func TestNewModelLoadsWorkspaceComposerHistory(t *testing.T) {
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir()})
	appendHistory := func(id, cwd, text string) {
		t.Helper()
		if _, err := store.Create(sessions.CreateInput{SessionID: id, Cwd: cwd}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.AppendEvent(id, sessions.AppendEventInput{
			Type: sessions.EventComposerInput, Payload: map[string]string{"text": text},
		}); err != nil {
			t.Fatal(err)
		}
	}
	appendHistory("current", "/repo", "remember me")
	appendHistory("other", "/other", "not me")

	m := newModel(context.Background(), Options{Cwd: "/repo", SessionStore: store})
	if len(m.inputHistory) != 1 || m.inputHistory[0].text != "remember me" {
		t.Fatalf("loaded history = %#v", m.inputHistory)
	}
}

func TestComposerHistoryRestoresExactMultilineDraft(t *testing.T) {
	m := sizedTestModel(80)
	m.inputHistory = []composerHistoryEntry{{text: "first\nsecond"}}
	m.historyIdx = len(m.inputHistory)
	m.setComposerState(composerState{text: "draft\nbody", cursor: 5})

	m = m.recallHistory(-1)
	if got := m.composerValue(); got != "first\nsecond" {
		t.Fatalf("recalled value = %q", got)
	}
	m = m.recallHistory(1)
	if got := m.composerValue(); got != "draft\nbody" {
		t.Fatalf("restored draft = %q", got)
	}
	if m.composer.cursor != 5 {
		t.Fatalf("restored cursor = %d, want 5", m.composer.cursor)
	}
}
