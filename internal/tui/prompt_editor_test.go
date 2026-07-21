package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func promptEditorModel(t *testing.T) model {
	t.Helper()
	root := t.TempDir()
	return newModel(context.Background(), Options{
		Cwd:            root,
		UserConfigPath: filepath.Join(root, "config", "config.json"),
	})
}

func TestPromptCommandOpensEditorAndPreservesDraftOnCancel(t *testing.T) {
	m := promptEditorModel(t)
	m.setComposerState(composerState{text: "keep this draft", cursor: 5})
	m = m.openPromptEditor()
	if m.promptEditor == nil || m.suggestionsActive() {
		t.Fatal("prompt editor should open as the only active modal")
	}

	updated, _ := m.Update(testKey(tea.KeyEsc))
	next := updated.(model)
	if next.promptEditor != nil {
		t.Fatal("Esc should close the slug step")
	}
	if got := next.composerValue(); got != "keep this draft" {
		t.Fatalf("cancel should preserve the underlying draft, got %q", got)
	}
}

func TestPromptEditorValidatesSlugAndBuiltinCollision(t *testing.T) {
	m := promptEditorModel(t).openPromptEditor()
	m.promptEditor.slug = "Bad Name"
	updated, _ := m.handlePromptEditorKey(testKey(tea.KeyEnter))
	next := updated.(model)
	if next.promptEditor.step != promptEditorSlug || next.promptEditor.err == "" {
		t.Fatalf("invalid slug should remain on slug step with an error: %#v", next.promptEditor)
	}

	next.promptEditor.slug = "help"
	updated, _ = next.handlePromptEditorKey(testKey(tea.KeyEnter))
	next = updated.(model)
	if !strings.Contains(next.promptEditor.err, "reserved") {
		t.Fatalf("builtin collision error = %q", next.promptEditor.err)
	}
}

func TestPromptEditorSavesReloadsAndAutocompleteInserts(t *testing.T) {
	m := promptEditorModel(t).openPromptEditor()
	m.promptEditor.slug = "review-pr"
	updated, _ := m.handlePromptEditorKey(testKey(tea.KeyEnter))
	m = updated.(model)
	m = m.handlePromptEditorPaste("Review $ARGUMENTS\nThen summarize.")
	updated, _ = m.handlePromptEditorKey(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	next := updated.(model)

	if next.promptEditor != nil {
		t.Fatalf("successful save should close the editor: %#v", next.promptEditor)
	}
	path := filepath.Join(filepath.Dir(next.userConfigPath), "commands", "review-pr.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved prompt: %v", err)
	}
	if string(raw) != "Review $ARGUMENTS\nThen summarize.\n" {
		t.Fatalf("saved body = %q", raw)
	}
	matches := next.matchCommandSuggestions("/review")
	if len(matches) != 1 || !matches[0].UserCommand {
		t.Fatalf("new prompt should refresh autocomplete immediately, got %#v", matches)
	}

	next.input.SetValue("/review")
	next.recomputeSuggestions()
	updated, _ = next.chooseSuggestion()
	next = updated.(model)
	if got := next.composerValue(); got != "Review $ARGUMENTS\nThen summarize." {
		t.Fatalf("autocomplete should insert the editable template, got %q", got)
	}
	if next.pending {
		t.Fatal("inserting a snippet must not launch a run")
	}
}

func TestPromptEditorRequiresExplicitOverwrite(t *testing.T) {
	m := promptEditorModel(t)
	if _, err := os.Stat(filepath.Dir(m.userCommandPaths.UserDir)); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	m = m.openPromptEditor()
	m.promptEditor.slug = "daily"
	m.promptEditor.step = promptEditorBody
	m.promptEditor.body = composerState{text: "first", cursor: 5}
	updated, _ := m.savePromptEditor(false)
	m = updated.(model)

	m = m.openPromptEditor()
	m.promptEditor.slug = "daily"
	m.promptEditor.step = promptEditorBody
	m.promptEditor.body = composerState{text: "second", cursor: 6}
	updated, _ = m.savePromptEditor(false)
	m = updated.(model)
	if m.promptEditor.step != promptEditorOverwrite {
		t.Fatalf("existing prompt should require overwrite confirmation: %#v", m.promptEditor)
	}
	updated, _ = m.handlePromptEditorKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(model)
	if m.promptEditor != nil {
		t.Fatal("confirmed overwrite should close the editor")
	}
	cmd, ok := m.lookupUserCommand("daily")
	if !ok || cmd.Template != "second" {
		t.Fatalf("overwritten prompt was not reloaded: %#v, %v", cmd, ok)
	}
}

func TestPromptEditorPasteIsModal(t *testing.T) {
	m := promptEditorModel(t)
	m.setComposerState(composerState{text: "underlying", cursor: 3})
	m = m.openPromptEditor()
	m.promptEditor.slug = "paste"
	m.promptEditor.step = promptEditorBody

	updated, _ := m.routePaste("line one\r\nline two")
	next := updated.(model)
	if got := next.promptEditor.body.text; got != "line one\nline two" {
		t.Fatalf("modal paste body = %q", got)
	}
	if got := next.composerValue(); got != "underlying" {
		t.Fatalf("modal paste leaked into underlying composer: %q", got)
	}
}
