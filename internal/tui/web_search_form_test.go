package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestWebSearchCommandOpensForm(t *testing.T) {
	m := model{}.handleWebSearchCommand("")
	if m.webSearchForm == nil {
		t.Fatal("bare /web-search should open the setup form")
	}
	if len(m.webSearchForm.providers) == 0 {
		t.Fatal("form should expose providers")
	}
	// Default selection is Exa (requires a key).
	if !m.webSearchForm.providers[0].RequiresKey {
		t.Error("first provider should require a key")
	}
}

func TestWebSearchCommandStatus(t *testing.T) {
	m := model{userConfigPath: filepath.Join(t.TempDir(), "config.json")}
	next := m.handleWebSearchCommand("status")
	if !transcriptHasText(next, "Web search configuration") {
		t.Error("status should print a configuration header")
	}
	if next.webSearchForm != nil {
		t.Error("status should not open a form")
	}
}

func TestWebSearchFormProviderSelectionAndKeyFlow(t *testing.T) {
	dir := t.TempDir()
	m := model{userConfigPath: filepath.Join(dir, "config.json")}
	// Isolate HOME so detectShellRC/sourceRC don't touch the real profile.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SHELL", "/bin/sh")
	// Ensure no KAJICODE_WEBSEARCH_PROVIDER leaks from the env.
	t.Setenv("KAJICODE_WEBSEARCH_PROVIDER", "")
	t.Setenv("EXA_API_KEY", "")
	t.Setenv("KAJICODE_WEBSEARCH_BASE_URL", "")

	m = m.openWebSearchForm()
	if m.webSearchForm == nil || m.webSearchForm.step != webSearchStepProvider {
		t.Fatalf("form not open at provider step: %+v", m.webSearchForm)
	}

	// Provider step: ↓ should move the highlight down, NOT select it. Pressing ↓
	// once should land on Tavily but stay on the provider step, then Enter selects.
	down, _ := m.handleWebSearchKey(testKey(tea.KeyDown))
	afterDown := down.(model)
	if afterDown.webSearchForm.step != webSearchStepProvider {
		t.Fatalf("down arrow on provider step must not select; step=%v", afterDown.webSearchForm.step)
	}
	if afterDown.webSearchForm.selected != 1 {
		t.Fatalf("down arrow should move highlight to index 1, got %d", afterDown.webSearchForm.selected)
	}
	next, _ := afterDown.handleWebSearchKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter, Text: ""}))
	n := next.(model)
	if n.webSearchForm == nil || n.webSearchForm.step != webSearchStepKey {
		t.Fatalf("selecting provider should advance to key step, got %+v", n.webSearchForm)
	}
	if n.webSearchForm.baseURL != "https://api.tavily.com/search" {
		t.Errorf("tavily base URL not prefilled: %q", n.webSearchForm.baseURL)
	}

	// Type an API key (focus starts on the key for key-requiring providers).
	for _, r := range []string{"t", "v", "-", "1", "2", "3"} {
		u, _ := n.handleWebSearchKey(testKeyText(r))
		n = u.(model)
	}
	if n.webSearchForm.apiKey != "tv-123" {
		t.Fatalf("api key = %q, want tv-123", n.webSearchForm.apiKey)
	}

	// Submit.
	done, _ := n.handleWebSearchKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter, Text: ""}))
	got := done.(model)
	if got.webSearchForm != nil {
		t.Fatal("form should close after submit")
	}
	if !transcriptHasText(got, "Web search configured") {
		t.Error("expected a configured notice")
	}
	// The key must be live in the process env (same-process os.Setenv).
	if os.Getenv("TAVILY_API_KEY") != "tv-123" {
		t.Errorf("TAVILY_API_KEY not live: %q", os.Getenv("TAVILY_API_KEY"))
	}
	// And persisted to the env fallback within the config dir.
	envPath := filepath.Join(dir, "envfile")
	if _, err := os.Stat(envPath); err != nil {
		t.Errorf("env fallback not written: %v", err)
	}
}

func TestWebSearchRequiredKeyValidation(t *testing.T) {
	dir := t.TempDir()
	m := model{userConfigPath: filepath.Join(dir, "config.json")}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("EXA_API_KEY", "")
	m = m.openWebSearchForm()
	// Exa (index 0) requires a key. Advance to key step and submit empty.
	next, _ := m.handleWebSearchKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter, Text: ""}))
	n := next.(model)
	if n.webSearchForm == nil || n.webSearchForm.step != webSearchStepKey {
		t.Fatalf("expected key step, got %+v", n.webSearchForm)
	}
	done, _ := n.handleWebSearchKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter, Text: ""}))
	got := done.(model)
	if got.webSearchForm == nil {
		t.Fatal("empty key should reopen the form with an error")
	}
	if got.webSearchForm.err == "" {
		t.Error("expected a required-key error")
	}
}

func TestWebSearchEscCancels(t *testing.T) {
	m := model{userConfigPath: filepath.Join(t.TempDir(), "config.json")}
	m = m.openWebSearchForm()
	if m.webSearchForm == nil {
		t.Fatal("form should open")
	}
	esc, _ := m.handleWebSearchKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc, Text: ""}))
	if esc.(model).webSearchForm != nil {
		t.Error("Esc should cancel the form")
	}
}

func TestWebSearchRemove(t *testing.T) {
	dir := t.TempDir()
	// Pre-seed env fallback with keys.
	t.Setenv("EXA_API_KEY", "old")
	t.Setenv("TAVILY_API_KEY", "old2")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SHELL", "/bin/sh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	m := model{userConfigPath: filepath.Join(dir, "config.json")}
	got := m.webSearchRemove()
	if !transcriptHasText(got, "Web search configuration removed") {
		t.Error("expected a removal notice")
	}
	if os.Getenv("EXA_API_KEY") != "" || os.Getenv("TAVILY_API_KEY") != "" {
		t.Error("live env vars should be unset after remove")
	}
}

func TestWebSearchStatusReflectsLiveKey(t *testing.T) {
	t.Setenv("TAVILY_API_KEY", "present")
	t.Setenv("KAJICODE_WEBSEARCH_PROVIDER", "")
	m := model{userConfigPath: filepath.Join(t.TempDir(), "config.json")}
	next := m.handleWebSearchCommand("status")
	text := statusText(next)
	if text == "" {
		t.Fatal("status should render text")
	}
	if !strings.Contains(text, "Tavily") {
		t.Errorf("status should mention Tavily, got:\n%s", text)
	}
}

// statusText extracts the appended system text from a status command result.
func statusText(m model) string {
	for _, row := range m.transcript {
		if row.text != "" {
			return row.text
		}
	}
	return ""
}
