package tui

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func writeUserCommand(t *testing.T, root, name, content string) {
	t.Helper()
	dir := filepath.Join(root, ".kajicode", "commands")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestUserCommandExpandsIntoComposerWithoutSubmitting(t *testing.T) {
	root := t.TempDir()
	writeUserCommand(t, root, "greet.md", "Say hello to $1 from the team.")

	m := newModel(context.Background(), Options{Cwd: root})
	if len(m.userCommands) != 1 {
		t.Fatalf("expected the user command to load, got %d", len(m.userCommands))
	}
	m.input.SetValue("/greet world")

	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if got := next.composerValue(); got != "Say hello to world from the team." {
		t.Fatalf("expanded user-command prompt should be editable in the composer, got %q", got)
	}
	if transcriptContains(next.transcript, "Say hello to world from the team.") {
		t.Fatalf("expanding a prompt must not submit it, got %#v", next.transcript)
	}
	if len(next.inputHistory) != 0 {
		t.Fatalf("temporary slash invocation must not enter composer history, got %#v", next.inputHistory)
	}
}

func TestUnknownSlashStillReportsUnknown(t *testing.T) {
	m := newModel(context.Background(), Options{Cwd: t.TempDir()})
	m.input.SetValue("/definitelynotacommand")

	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if !transcriptContains(next.transcript, "unknown command") {
		t.Fatalf("a /command with no builtin or user match should report unknown, got %#v", next.transcript)
	}
}

func TestUserCommandAppearsInAutocomplete(t *testing.T) {
	root := t.TempDir()
	writeUserCommand(t, root, "deploy.md", "Deploy it.")
	m := newModel(context.Background(), Options{Cwd: root})

	got := m.matchCommandSuggestions("/dep")
	found := false
	for _, s := range got {
		if s.Name == "/deploy" {
			found = true
		}
	}
	if !found {
		t.Fatalf("user command /deploy should appear in autocomplete for '/dep', got %#v", got)
	}
}
