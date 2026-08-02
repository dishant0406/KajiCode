package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/dishant0406/KajiCode/internal/agent"
	"github.com/dishant0406/KajiCode/internal/config"
)

func TestHarnessAndPromptInspectParseAsBuiltins(t *testing.T) {
	tests := []struct {
		input string
		kind  commandKind
		name  string
	}{
		{input: "/harness", kind: commandHarness, name: "/harness"},
		{input: "/prompt-inspect", kind: commandPromptInspect, name: "/prompt-inspect"},
		{input: "/prompt-report", kind: commandPromptInspect, name: "/prompt-inspect"},
		{input: "/prompt", kind: commandPromptEditor, name: "/prompt"},
	}
	for _, tc := range tests {
		got := parseCommand(tc.input)
		if got.kind != tc.kind || got.name != tc.name {
			t.Fatalf("parseCommand(%q) = kind %v name %q, want kind %v name %q", tc.input, got.kind, got.name, tc.kind, tc.name)
		}
	}
}

func TestPromptInspectCommandShowsHarnessPromptSection(t *testing.T) {
	m := newModel(context.Background(), Options{
		Cwd:          t.TempDir(),
		ProviderName: "openai",
		ModelName:    "gpt-4.1",
		AgentOptions: agent.Options{
			SystemPrompt: "base prompt",
			Harness: config.HarnessConfig{PromptAddenda: []config.HarnessPromptAddendum{{
				ID:   "team",
				Text: "Prefer rg before broad scans.",
			}}},
		},
	})
	m.input.SetValue("/prompt-inspect --summary")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /prompt-inspect to be handled without starting an agent run")
	}
	text := transcriptText(next.transcript)
	for _, want := range []string{"Prompt inspect", "estimated tokens", "harness-config", "Editable layers", "/prompt-inspect prints the complete prompt"} {
		assertContains(t, text, want)
	}
}

func TestPromptInspectFullCommandPrintsRawPrompt(t *testing.T) {
	m := newModel(context.Background(), Options{
		Cwd:          t.TempDir(),
		ProviderName: "openai",
		ModelName:    "gpt-4.1",
		AgentOptions: agent.Options{
			SystemPrompt: "base prompt",
			Harness: config.HarnessConfig{PromptAddenda: []config.HarnessPromptAddendum{{
				ID:   "team",
				Text: "Prefer rg before broad scans.",
			}}},
		},
	})
	m.input.SetValue("/prompt-inspect")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /prompt-inspect to be handled without starting an agent run")
	}
	text := transcriptText(next.transcript)
	for _, want := range []string{"Full prompt:", "summary: /prompt-inspect --summary", "base prompt", "<harness_prompt_addenda>", "Prefer rg before broad scans."} {
		assertContains(t, text, want)
	}
}

func TestHarnessCommandPersistsAndReloadsUserConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	m := newModel(context.Background(), Options{UserConfigPath: configPath})

	m.input.SetValue(`/harness prompt add team --text "Prefer rg."`)
	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd != nil {
		t.Fatal("expected /harness prompt add to be synchronous")
	}
	if got := next.agentOptions.Harness.PromptAddenda; len(got) != 1 || got[0].Text != "Prefer rg." {
		t.Fatalf("live prompt addenda = %#v", got)
	}

	next.input.SetValue(`/harness rule add shell --match bash --action ask --command-contains "curl " --reason "Review network shell."`)
	updated, cmd = next.Update(testKey(tea.KeyEnter))
	next = updated.(model)
	if cmd != nil {
		t.Fatal("expected /harness rule add to be synchronous")
	}
	if got := next.agentOptions.Harness.PermissionRules; len(got) != 1 || got[0].CommandContains[0] != "curl" {
		t.Fatalf("live permission rules = %#v", got)
	}

	cfg := readTUIHarnessConfig(t, configPath)
	if len(cfg.Harness.PromptAddenda) != 1 || len(cfg.Harness.PermissionRules) != 1 {
		t.Fatalf("persisted harness = %#v", cfg.Harness)
	}

	next.input.SetValue("/harness")
	updated, cmd = next.Update(testKey(tea.KeyEnter))
	next = updated.(model)
	if cmd != nil {
		t.Fatal("expected /harness list to be synchronous")
	}
	text := transcriptText(next.transcript)
	for _, want := range []string{"Harness", "team [enabled] - Prefer rg.", "shell [enabled] ask bash contains=curl"} {
		assertContains(t, text, want)
	}
}

func TestHarnessCommandReloadUsesProcessEnvironment(t *testing.T) {
	t.Setenv("KAJICODE_TUI_HARNESS_TEST_KEY", "test-key")
	configPath := filepath.Join(t.TempDir(), "config.json")
	seed := config.FileConfig{
		ActiveProvider: "env-openai",
		Providers: []config.ProviderProfile{{
			Name:         "env-openai",
			ProviderKind: config.ProviderKindOpenAI,
			APIKeyEnv:    "KAJICODE_TUI_HARNESS_TEST_KEY",
			Model:        "gpt-4.1",
		}},
	}
	data, err := json.Marshal(seed)
	if err != nil {
		t.Fatalf("marshal seed config: %v", err)
	}
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatalf("write seed config: %v", err)
	}
	m := newModel(context.Background(), Options{UserConfigPath: configPath})
	m.input.SetValue(`/harness prompt add team --text "Keep provider env reloads working."`)

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /harness prompt add to be synchronous")
	}
	text := transcriptText(next.transcript)
	assertContains(t, text, "Saved harness prompt addendum team")
	if strings.Contains(text, "reload failed") {
		t.Fatalf("reload should use process environment, got transcript:\n%s", text)
	}
	if got := next.agentOptions.Harness.PromptAddenda; len(got) != 1 || got[0].Text != "Keep provider env reloads working." {
		t.Fatalf("live prompt addenda = %#v", got)
	}
}

func TestHarnessCommandRejectsProjectAllowRule(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.json")
	m := newModel(context.Background(), Options{Cwd: root, UserConfigPath: configPath})
	m.input.SetValue(`/harness rule add repo-allow --match bash --action allow --project`)

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /harness rule add rejection to be synchronous")
	}
	text := transcriptText(next.transcript)
	assertContains(t, text, "project harness permission rules cannot use action allow")
	if _, err := os.Stat(filepath.Join(root, ".kajicode", "config.json")); err == nil {
		t.Fatal("project config should not be created for a rejected allow rule")
	}
}

func readTUIHarnessConfig(t *testing.T, path string) config.FileConfig {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg config.FileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("decode config: %v\n%s", err, strings.TrimSpace(string(data)))
	}
	return cfg
}
