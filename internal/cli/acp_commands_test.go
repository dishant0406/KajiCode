package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/dishant0406/KajiCode/internal/skills"
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

// TestACPSkillCommandsAndExpand proves a skill installed in the skills dir is
// advertised as /name and that acpExpandSkill inlines its body plus the request.
func TestACPSkillCommandsAndExpand(t *testing.T) {
	skillsDir := t.TempDir()
	writeSourceSkillDir(t, filepath.Join(skillsDir, "code-review"),
		"---\nname: code-review\ndescription: Review a diff.\n---\nReview the diff carefully.\n")
	// A name with a space has no slash form and must not be advertised.
	writeSourceSkillDir(t, filepath.Join(skillsDir, "bad name"),
		"---\nname: bad name\ndescription: Not slash-able.\n---\nbody\n")

	deps := appDeps{skillsDir: func() string { return skillsDir }}

	names := map[string]bool{}
	for _, c := range acpCommands(deps)("") {
		names[c.Name] = true
	}
	if !names["code-review"] {
		t.Fatal("installed skill must be advertised as a /command")
	}
	if names["bad name"] {
		t.Fatal("a skill whose name is not slash-shaped must not be advertised")
	}
	if !names[skills.BuiltinCustomizeKajicodeName] {
		t.Fatal("the built-in skill must be advertised")
	}

	expand := acpExpandSkill(deps)
	got, ok := expand("", "code-review", "on this diff")
	if !ok {
		t.Fatal("acpExpandSkill did not resolve an installed skill")
	}
	if !strings.Contains(got, "Review the diff carefully.") || !strings.Contains(got, "on this diff") {
		t.Fatalf("expand = %q, want body + request", got)
	}
	if _, ok := expand("", "no-such-skill", ""); ok {
		t.Fatal("unknown skill must not resolve")
	}
	// A name claimed by an ACP command keeps command precedence.
	if _, ok := expand("", "skills", ""); ok {
		t.Fatal("a name colliding with an ACP command must not resolve as a skill")
	}
}

// TestACPSkillCommandsSkipBodyless proves a skill with an empty body is not
// advertised and does not expand (it could never run), so the palette never
// offers a dead end.
func TestACPSkillCommandsSkipBodyless(t *testing.T) {
	dir := t.TempDir()
	writeSourceSkillDir(t, filepath.Join(dir, "empty-body"),
		"---\nname: empty-body\ndescription: No body.\n---\n")
	deps := appDeps{skillsDir: func() string { return dir }}
	for _, c := range acpCommands(deps)("") {
		if c.Name == "empty-body" {
			t.Fatal("a bodyless skill must not be advertised")
		}
	}
	if _, ok := acpExpandSkill(deps)("", "empty-body", "x"); ok {
		t.Fatal("a bodyless skill must not expand")
	}
}
