package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

func TestAssistantMarkdownRendersHeadingsQuotesAndCallouts(t *testing.T) {
	lines := renderAssistantMarkdownText(strings.Join([]string{
		"# Top **Plan**",
		"",
		"> [!WARNING] Watch",
		"> check **limits** and [docs](https://example.com/docs)",
		"",
		"> ordinary quote with `code`",
	}, "\n"), 64, 64, true)
	rendered := strings.Join(lines, "\n")
	plain := ansiPattern.ReplaceAllString(rendered, "")

	for _, unwanted := range []string{"# Top", "[!WARNING]", "https://example.com/docs", "**", "> ordinary"} {
		if strings.Contains(plain, unwanted) {
			t.Fatalf("rich markdown leaked source marker %q in:\n%s", unwanted, plain)
		}
	}
	for _, want := range []string{"▸ Top Plan", "▌ WARN Watch", "│ check limits and docs", "│ ordinary quote with code"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("rich markdown missing %q in:\n%s", want, plain)
		}
	}
	if !strings.Contains(rendered, markdownAmberStart+"WARN"+markdownAmberEnd) {
		t.Fatalf("warning callout should carry semantic amber styling, got:\n%s", rendered)
	}
	for index, line := range lines {
		if width := lipgloss.Width(line); width > 64 {
			t.Fatalf("line %d width = %d, want <= 64: %q", index, width, line)
		}
	}
}

func TestMarkdownInlineRendersRichLocalSyntax(t *testing.T) {
	input := "Use <kbd>Esc</kbd> <mark>hot</mark> ==warm== <ins>new</ins> ++add++ <s>gone</s> <strong>bold</strong> <em>soft</em> <code>x</code> <https://example.com> ![Chart](chart.png)"
	rendered := renderMarkdownInline(input)
	plain := ansiPattern.ReplaceAllString(rendered, "")

	if plain != "Use Esc hot warm new add gone bold soft x https://example.com image: Chart" {
		t.Fatalf("rich inline plain text = %q", plain)
	}
	for _, marker := range []string{
		markdownCodeStart,
		markdownAmberStart,
		markdownGreenStart,
		markdownStrikeStart,
		markdownBoldStart,
		markdownItalicStart,
		markdownLinkStart,
	} {
		if !strings.Contains(rendered, marker) {
			t.Fatalf("rich inline render missing semantic marker %q in %q", marker, rendered)
		}
	}
}

func TestMarkdownListMarkersCarrySemanticStyles(t *testing.T) {
	rendered := strings.Join(renderAssistantMarkdownText(strings.Join([]string{
		"- top",
		"  - nested",
		"- [x] done",
		"- [ ] todo",
		"1. ordered",
	}, "\n"), 48, 48, true), "\n")
	plain := ansiPattern.ReplaceAllString(rendered, "")

	for _, want := range []string{"• top", "  • nested", "[x] done", "[ ] todo", "1. ordered"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("list render missing %q in:\n%s", want, plain)
		}
	}
	for _, want := range []string{
		markdownAccentStart + "• " + markdownAccentEnd,
		markdownLinkStart + "• " + markdownLinkEnd,
		markdownGreenStart + "[x] " + markdownGreenEnd,
		markdownAmberStart + "[ ] " + markdownAmberEnd,
		markdownAmberStart + "1. " + markdownAmberEnd,
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("list render missing semantic marker %q in:\n%s", want, rendered)
		}
	}
}

func TestStyleAssistantMarkdownLineConsumesSemanticMarkers(t *testing.T) {
	old := lipgloss.Writer.Profile
	lipgloss.Writer.Profile = colorprofile.TrueColor
	defer func() { lipgloss.Writer.Profile = old }()

	line := markdownAccentStart + "A" + markdownAccentEnd + " " + markdownCodeStart + "b" + markdownCodeEnd
	out := styleAssistantMarkdownLine(line, lipgloss.NewStyle())

	if strings.Contains(out, markdownAccentStart) || strings.Contains(out, markdownCodeStart) {
		t.Fatalf("semantic markers should be consumed by the theme styler, got %q", out)
	}
	if got := ansiPattern.ReplaceAllString(out, ""); got != "A b" {
		t.Fatalf("styled semantic line plain text = %q", got)
	}
}
