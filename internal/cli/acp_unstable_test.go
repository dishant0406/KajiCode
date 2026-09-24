package cli

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/dishant0406/KajiCode/internal/config"
)

func TestStripNesReply(t *testing.T) {
	cases := map[string]string{
		"NONE":                     "",
		"":                         "",
		"  print(1)  ":             "print(1)",
		"```go\nprint(1)\n```":     "print(1)",
		"```\nprint(1)\n```":       "print(1)",
		"text := ctx.Value(\"k\")": "text := ctx.Value(\"k\")",
		"  textbox = 1":            "textbox = 1",
	}
	for raw, want := range cases {
		if got := stripNesReply(raw); got != want {
			t.Errorf("stripNesReply(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestParseNesEditCoversCursorLine(t *testing.T) {
	edit, ok := parseNesEdit("newLine", "old")
	if !ok {
		t.Fatal("expected an edit")
	}
	if edit.Range.Start.Line != 0 || edit.Range.End.Line != 0 {
		t.Fatalf("range lines = %+v", edit.Range)
	}
	if edit.Range.Start.Character != 0 || edit.Range.End.Character != len("old") {
		t.Fatalf("range chars = %+v (want 0..%d)", edit.Range, len("old"))
	}
	if edit.NewText != "newLine" {
		t.Fatalf("newText = %q", edit.NewText)
	}
	if _, ok := parseNesEdit("NONE", "old"); ok {
		t.Fatal("NONE must not produce an edit")
	}
}

func TestCursorLineAndTruncate(t *testing.T) {
	if got := cursorLine("a\nb\nc", 1); got != "b" {
		t.Fatalf("cursorLine = %q", got)
	}
	if got := cursorLine("a\nb", 9); got != "" {
		t.Fatalf("out-of-range line = %q", got)
	}
	long := strings.Repeat("é", 100)
	got := truncateBytes(long, 101)
	if len(got) > 101 || !utf8.ValidString(got) {
		t.Fatalf("truncateBytes = %q (len %d)", got, len(got))
	}
}

func TestACPResolveContextWindow(t *testing.T) {
	resolve := acpResolveContextWindow()
	// A curated model resolves to its catalog window.
	if got := resolve(config.ProviderProfile{Model: "gpt-4o"}); got != 128_000 {
		t.Errorf("gpt-4o window = %d, want 128000", got)
	}
	// An unknown model resolves to 0 (no denominator) rather than a guess.
	if got := resolve(config.ProviderProfile{Model: "no-such-model-xyz"}); got != 0 {
		t.Errorf("unknown model window = %d, want 0", got)
	}
	// An empty model id is 0, never a panic.
	if got := resolve(config.ProviderProfile{}); got != 0 {
		t.Errorf("empty model window = %d, want 0", got)
	}
}
