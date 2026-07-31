package tui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestAssistantMarkdownListsRenderWithoutSourceMarkers(t *testing.T) {
	lines := renderAssistantMarkdownText(strings.Join([]string{
		"1. **first** [guide](https://example.com/guide) item",
		"  2. nested ordered item",
		"- [ ] open task with ~~old~~ text",
		"+ plus marker with escaped \\*literal\\* text",
		"* star marker",
	}, "\n"), 56, 56, true)
	rendered := strings.Join(lines, "\n")
	plain := ansiPattern.ReplaceAllString(rendered, "")

	for _, unwanted := range []string{"https://example.com/guide", "- [ ]", "+ plus", "* star", "\\*literal\\*"} {
		if strings.Contains(plain, unwanted) {
			t.Fatalf("local list renderer leaked source syntax %q in:\n%s", unwanted, plain)
		}
	}
	for _, want := range []string{"1. first guide item", "  2. nested ordered item", "[ ] open task with old text", "• plus marker with escaped *literal* text", "• star marker"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("local list renderer missing %q in:\n%s", want, plain)
		}
	}
	if !strings.Contains(rendered, markdownBoldStart+"first"+markdownBoldEnd) {
		t.Fatalf("local list renderer should preserve inline bold styling, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, markdownStrikeStart+"old"+markdownStrikeEnd) {
		t.Fatalf("local list renderer should preserve inline strike styling, got:\n%s", rendered)
	}
}

func TestAssistantMarkdownLongListsStayWithinMeasure(t *testing.T) {
	lines := renderAssistantMarkdownText(longAssistantMarkdownListAnswer(35), 48, 48, true)
	if len(lines) == 0 {
		t.Fatal("long list answer rendered no lines")
	}
	for index, line := range lines {
		if width := lipgloss.Width(line); width > 48 {
			t.Fatalf("line %d width = %d, want <= 48: %q", index, width, line)
		}
	}
}

func BenchmarkAssistantMarkdownLongListAnswer(b *testing.B) {
	text := longAssistantMarkdownListAnswer(100)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		lines := renderAssistantMarkdownText(text, 96, 96, true)
		if len(lines) == 0 {
			b.Fatal("long list answer rendered no lines")
		}
	}
}

func longAssistantMarkdownListAnswer(items int) string {
	var builder strings.Builder
	for i := 0; i < items; i++ {
		fmt.Fprintf(&builder, "## Section %03d\n", i)
		fmt.Fprintf(&builder, "- **Scope**: analyse package %03d and explain the current behavior with enough words to wrap naturally.\n", i)
		fmt.Fprintf(&builder, "  - nested point with [docs](https://example.com/%03d) and more prose that also wraps.\n", i)
		fmt.Fprintf(&builder, "- [x] completed task %03d with ~~obsolete~~ detail replaced by the current path.\n", i)
		fmt.Fprintf(&builder, "1. ordered follow-up %03d with escaped \\*literal\\* characters.\n", i)
		fmt.Fprintf(&builder, "| Field | Value |\n|---|---|\n| Item | %03d |\n\n", i)
	}
	return builder.String()
}
