package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/dishant0406/KajiCode/internal/agent"
	"github.com/dishant0406/KajiCode/internal/config"
)

func TestEmptyStateShowsBrandAndTaglineOnly(t *testing.T) {
	m := newModel(context.Background(), Options{ProviderName: "anthropic", ModelName: "claude-sonnet-4.5"})
	m.width, m.height = 120, 30

	view := plainRender(t, m.View())
	assertContains(t, view, "██  ██   ████     ██  █████")
	assertContains(t, view, composerPlaceholder)
	assertContains(t, view, "Shift+Tab mode")
	assertNotContains(t, view, emptyStateTagline)
	assertNotContains(t, view, "running kajicode against ")
	assertNotContains(t, view, "add a --version flag")
	assertNotContains(t, view, "explain internal/agent/loop.go")
	assertNotContains(t, view, "fix the failing test in internal/tools")
}

// TestWordmarkIsPlain guards the --version banner: it must carry no ANSI
// escapes, because this renderer never resolves --theme/KAJICODE_THEME and any
// palette color could be unreadable on the user's background.
func TestWordmarkIsPlain(t *testing.T) {
	wordmark := Wordmark()
	if strings.Contains(wordmark, "\x1b") {
		t.Fatalf("expected uncolored wordmark, got %q", wordmark)
	}
	lines := strings.Split(wordmark, "\n")
	if len(lines) != len(kajicodeWordmarkLines) {
		t.Fatalf("expected %d wordmark lines, got %d", len(kajicodeWordmarkLines), len(lines))
	}
	for index, line := range lines {
		if want := kajicodeWordmarkLines[index]; line != want {
			t.Fatalf("wordmark line %d: expected %q, got %q", index, want, line)
		}
	}
}

func TestEmptyStateUsesCompactWordmarkWhenNarrow(t *testing.T) {
	width := widestLine(kajicodeHomeWordmarkLines) - 1

	lines := plainRender(t, strings.Join(themedWordmarkLines(width), "\n"))
	assertContains(t, lines, "KajiCode")
	assertNotContains(t, lines, "██  ██   ████     ██  █████")
}

func TestEmptyStateShowsVersion(t *testing.T) {
	m := newModel(context.Background(), Options{Version: "0.2.0"})
	m.width, m.height = 100, 30

	view := plainRender(t, m.View())
	assertContains(t, view, "v0.2.0")
}

func TestEmptyStateCentersComposerAndShowsRuntimeContext(t *testing.T) {
	m := newModel(context.Background(), Options{
		Cwd:       "/workspace/kajicode",
		Version:   "0.0.5",
		ModelName: "gpt-5.6-sol",
		MCPConfig: config.MCPConfig{Servers: map[string]config.MCPServerConfig{
			"docs":     {},
			"disabled": {Disabled: true},
		}},
	})
	m.width, m.height = 100, 30
	m.gitBranch = "main"

	view := plainRender(t, m.View())
	assertContains(t, view, "/workspace/kajicode:main  ·  ● MCP 1  ·  v0.0.5")
	assertContains(t, view, "gpt-5.6-sol")
	assertContains(t, view, "Shift+Tab mode: auto-approve  ·  Ctrl+X ? commands  ·  ? shortcuts")
	if count := strings.Count(view, composerPlaceholder); count != 1 {
		t.Fatalf("fresh home should contain one live composer, got %d in %q", count, view)
	}
	composerTop := strings.Index(view, "╭")
	if composerTop < 0 {
		t.Fatalf("fresh home composer missing from %q", view)
	}
	lineStart := strings.LastIndex(view[:composerTop], "\n") + 1
	if got := composerTop - lineStart; got != (m.width-homeComposerWidth(m.width))/2 {
		t.Fatalf("composer left edge = %d, want centered at %d", got, (m.width-homeComposerWidth(m.width))/2)
	}
}

func TestEmptyStateShortcutsProvideVisibleFeedback(t *testing.T) {
	m := newModel(context.Background(), Options{PermissionMode: agent.PermissionModeAuto})
	m.width, m.height = 100, 30

	updated, _ := m.Update(testKeyShift(tea.KeyTab))
	next := updated.(model)
	view := plainRender(t, next.View())

	assertContains(t, view, "Shift+Tab mode: ask")
	assertContains(t, view, "Ctrl+X ? commands")
}

func TestDisplayVersion(t *testing.T) {
	cases := []struct{ in, want string }{
		{"0.2.0", "v0.2.0"},
		{"v0.2.0", "v0.2.0"},
		{"dev", "dev"},
		{"  ", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := displayVersion(tc.in); got != tc.want {
			t.Fatalf("displayVersion(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestEmptyStateDisappearsAfterFirstRow(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.width, m.height = 100, 30
	m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendUser, text: "hello"})

	view := viewString(m.View())
	if strings.Contains(view, emptyStateTagline) {
		t.Fatal("empty state must disappear once the transcript has content")
	}
	assertContains(t, view, "hello")
}

func TestDigitsTypeNormallyOnEmptySurface(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.width, m.height = 100, 30

	m = typeRunes(t, m, "2")
	if got := m.input.Value(); got != "2" {
		t.Fatalf("digit should type normally on the empty surface, got %q", got)
	}

	// With text already in the composer the digit keeps typing normally.
	m = newModel(context.Background(), Options{})
	m.input.SetValue("count to ")
	m.resetComposerFromInput()
	m = typeRunes(t, m, "3")
	if got := m.input.Value(); got != "count to 3" {
		t.Fatalf("digit should append to a non-empty composer, got %q", got)
	}

	// Once the transcript has content, a bare digit types normally too.
	fresh := newModel(context.Background(), Options{})
	fresh.transcript = reduceTranscript(fresh.transcript, transcriptAction{kind: actionAppendUser, text: "hi"})
	fresh = typeRunes(t, fresh, "1")
	if got := fresh.input.Value(); got != "1" {
		t.Fatalf("digit should type normally after the first turn, got %q", got)
	}
}

func TestBorderedBlockFitsLongPlainLines(t *testing.T) {
	block := borderedBlock(24, []string{"this line should truncate inside the border"})

	assertContains(t, block, "\u2026")
	assertRenderedLineWidths(t, block, 24)
}

func TestBorderedBlockFitsLongStyledLines(t *testing.T) {
	block := borderedBlock(26, []string{
		kajicodeTheme.accent.Render("styled line should truncate inside the border"),
	})

	assertContains(t, block, "\u2026")
	assertRenderedLineWidths(t, block, 26)
}

func assertRenderedLineWidths(t *testing.T, block string, width int) {
	t.Helper()

	for _, line := range strings.Split(block, "\n") {
		if got := lipgloss.Width(line); got != width {
			t.Fatalf("expected line width %d, got %d for %q", width, got, line)
		}
	}
}
