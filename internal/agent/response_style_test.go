package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResponseStyleContext(t *testing.T) {
	for _, blank := range []string{"", "balanced", "BALANCED", "garbage", "  "} {
		if got := responseStyleContext(Options{ResponseStyle: blank}); got != "" {
			t.Errorf("ResponseStyle %q should add nothing, got %q", blank, got)
		}
	}
	for _, style := range []string{"concise", "explanatory", "review", "Concise", "REVIEW"} {
		got := responseStyleContext(Options{ResponseStyle: style})
		if got == "" {
			t.Fatalf("style %q produced no directive", style)
		}
		if !strings.Contains(got, "Response style (STRICT") {
			t.Errorf("style %q directive should be strict, got %q", style, got)
		}
		if !strings.Contains(strings.ToLower(got), strings.ToLower(style)) {
			t.Errorf("style %q directive should name the style, got %q", style, got)
		}
	}
	// It lands in the assembled prompt for a real style (strict), and not for
	// balanced.
	if !strings.Contains(buildSystemPrompt(Options{ResponseStyle: "concise"}), "Response style (STRICT") {
		t.Error("buildSystemPrompt should inject the concise directive")
	}
	if strings.Contains(buildSystemPrompt(Options{ResponseStyle: "balanced"}), "Response style") {
		t.Error("balanced must not inject a style section (prompt stays byte-identical)")
	}
}

// TestResponseStylePersistedFileInjected verifies that a globally-persisted
// speaking style (RESPONSE_STYLE.md under the per-user config dir, written by
// the /style editor) is injected into the system prompt verbatim — and that the
// pre-existing enum behavior is unchanged when no such file exists.
func TestResponseStylePersistedFileInjected(t *testing.T) {
	dir := t.TempDir()
	defer withSystemPromptTestUserConfigDir(t, dir)()
	// Absent file => enum still drives (unchanged).
	if got := buildSystemPrompt(Options{ResponseStyle: "concise"}); !strings.Contains(got, "Response style (STRICT") {
		t.Fatal("no persisted file should fall back to the enum directive")
	}

	// Present persisted file wins over the enum.
	kajicodeDir := filepath.Join(dir, "kajicode")
	if err := os.MkdirAll(kajicodeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	style := "Talk like a friendly engineer: lead with the answer, keep every fact exact, and drop the jargon."
	if err := os.WriteFile(filepath.Join(kajicodeDir, userStyleFile), []byte(style+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	prompt := buildSystemPrompt(Options{ResponseStyle: "concise"})
	if !strings.Contains(prompt, "Response style (STRICT") {
		t.Fatal("persisted style block should be strict")
	}
	if !strings.Contains(prompt, style) {
		t.Fatalf("persisted style verbatim not present; prompt:\n%s", prompt)
	}
}
