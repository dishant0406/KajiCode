package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestACPCatalogNamesAreUniqueAndNonEmpty(t *testing.T) {
	specs := acpCommandSpecs()
	if len(specs) == 0 {
		t.Fatal("expected a non-empty ACP command catalog")
	}
	seen := map[string]bool{}
	for _, spec := range specs {
		if spec.name == "" || spec.description == "" {
			t.Fatalf("incomplete command spec: %+v", spec)
		}
		if !spec.session && spec.run == nil {
			t.Fatalf("CLI command spec %q needs a run function", spec.name)
		}
		if spec.session && spec.run != nil {
			t.Fatalf("session command spec %q must not have a CLI run function", spec.name)
		}
		if strings.HasPrefix(spec.name, "/") || strings.ContainsAny(spec.name, " \t") {
			t.Fatalf("command name %q must be a bare, slash-free token", spec.name)
		}
		if seen[spec.name] {
			t.Fatalf("duplicate command name %q", spec.name)
		}
		seen[spec.name] = true
	}
}

func TestACPLookupCommandNormalizesInput(t *testing.T) {
	if _, ok := lookupACPCommand("/config"); !ok {
		t.Fatal("leading slash should still resolve")
	}
	if _, ok := lookupACPCommand("  CONFIG  "); !ok {
		t.Fatal("case and surrounding whitespace should be ignored")
	}
	if _, ok := lookupACPCommand("does-not-exist"); ok {
		t.Fatal("unknown command must not resolve")
	}
}

func TestACPCommandsProjectInputHints(t *testing.T) {
	byName := map[string]bool{}
	for _, c := range acpCommands(appDeps{})("") {
		byName[c.Name] = c.Input != nil
	}
	if !byName["sessions"] {
		t.Fatal("sessions should advertise an input hint (base arg list)")
	}
	if byName["config"] {
		t.Fatal("config takes no input and must not advertise a hint")
	}
}

func TestLimitedWriterCapsAllocationAndMarksTruncation(t *testing.T) {
	w := &limitedWriter{cap: 16}
	if _, err := w.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if got := w.String(); got != "hello" {
		t.Fatalf("small write changed: %q", got)
	}
	// A write far larger than the cap must be discarded past the limit, not
	// buffered in full.
	if _, err := w.Write(bytes.Repeat([]byte("x"), 10_000)); err != nil {
		t.Fatal(err)
	}
	if w.buf.Len() > 16 {
		t.Fatalf("buffered %d bytes, want <= cap", w.buf.Len())
	}
	if !strings.Contains(w.String(), "truncated") {
		t.Fatal("expected a truncation marker")
	}
}

func TestLimitedWriterTruncatesOnRuneBoundary(t *testing.T) {
	// "é" is two bytes; a cap that lands mid-rune must still yield valid UTF-8.
	w := &limitedWriter{cap: 5}
	if _, err := w.Write(bytes.Repeat([]byte("é"), 10)); err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(w.String()) {
		t.Fatal("truncated output must remain valid UTF-8")
	}
}

func TestACPUserCommandSnippetsSaveExpandAndAdvertise(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.json")
	deps := appDeps{userConfigPath: func() (string, error) { return cfg, nil }}

	// Save a snippet via the wired handler.
	name, err := acpSavePromptSnippet(deps)("", "release", "Open a PR titled $1. Summarize: $ARGUMENTS")
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if name != "release" {
		t.Fatalf("name = %q", name)
	}
	// It appears in the advertised catalog...
	var advertised bool
	for _, c := range acpCommands(deps)("") {
		if c.Name == "release" {
			advertised = true
			if c.Input == nil {
				t.Error("snippet must advertise an input hint")
			}
		}
	}
	if !advertised {
		t.Fatal("saved snippet not advertised in the command catalog")
	}
	// ...and expands with its placeholders filled.
	expanded, ok := acpExpandPromptSnippet(deps)("", "release", "v1.2")
	if !ok || !strings.Contains(expanded, "v1.2") {
		t.Fatalf("expand = %q ok=%v", expanded, ok)
	}
	if _, ok := acpExpandPromptSnippet(deps)("", "nope", ""); ok {
		t.Fatal("unknown snippet must not resolve")
	}
	// A builtin name wins over a same-named snippet.
	if _, err := acpSavePromptSnippet(deps)("", "config", "shadow me"); err != nil {
		t.Fatalf("save shadow: %v", err)
	}
	for _, c := range acpCommands(deps)("") {
		if c.Name == "config" && strings.Contains(c.Description, "shadow") {
			t.Fatal("a snippet must not shadow a builtin command")
		}
	}
}

func TestACPSnippetCannotShadowBuiltinAtInvocation(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.json")
	deps := appDeps{userConfigPath: func() (string, error) { return cfg, nil }}

	if _, err := acpSavePromptSnippet(deps)("", "config", "shadow"); err != nil {
		t.Fatalf("save: %v", err)
	}
	// A snippet whose name matches a builtin must NOT expand (it would hijack
	// the builtin at dispatch time).
	if _, ok := acpExpandPromptSnippet(deps)("", "config", ""); ok {
		t.Fatal("a snippet named after a builtin command must not expand")
	}
	// A session-scoped ACP command name is likewise protected.
	if _, err := acpSavePromptSnippet(deps)("", "compact", "shadow"); err != nil {
		t.Fatalf("save compact: %v", err)
	}
	if _, ok := acpExpandPromptSnippet(deps)("", "compact", ""); ok {
		t.Fatal("a snippet named after a session command must not expand")
	}
	// A non-colliding snippet still expands.
	if _, err := acpSavePromptSnippet(deps)("", "mysnip", "hello $1"); err != nil {
		t.Fatalf("save mysnip: %v", err)
	}
	if _, ok := acpExpandPromptSnippet(deps)("", "mysnip", "world"); !ok {
		t.Fatal("a non-colliding snippet must expand")
	}
}
