package tui

import (
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
	glamouransi "github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

const glamourInlineWordWrap = 1 << 20

var glamourInline = newGlamourInlineRenderer()

type glamourInlineRenderer struct {
	mu       sync.Mutex
	renderer *glamour.TermRenderer
}

func newGlamourInlineRenderer() *glamourInlineRenderer {
	zero := uint(0)
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStyles(glamouransi.StyleConfig{
			Document: glamouransi.StyleBlock{Margin: &zero},
			List: glamouransi.StyleList{
				StyleBlock:  glamouransi.StyleBlock{Margin: &zero},
				LevelIndent: 2,
			},
			BlockQuote: glamouransi.StyleBlock{
				Indent:      &zero,
				IndentToken: stringPointer("│ "),
			},
			Strong: glamouransi.StylePrimitive{
				BlockPrefix: markdownBoldStart,
				BlockSuffix: markdownBoldEnd,
			},
			Strikethrough: glamouransi.StylePrimitive{
				BlockPrefix: markdownStrikeStart,
				BlockSuffix: markdownStrikeEnd,
			},
			Item: glamouransi.StylePrimitive{BlockPrefix: "• "},
			Enumeration: glamouransi.StylePrimitive{
				BlockPrefix: ". ",
			},
			Task: glamouransi.StyleTask{
				Ticked:   "[x] ",
				Unticked: "[ ] ",
			},
		}),
		glamour.WithColorProfile(termenv.Ascii),
		glamour.WithWordWrap(glamourInlineWordWrap),
	)
	if err != nil {
		return &glamourInlineRenderer{}
	}
	return &glamourInlineRenderer{renderer: renderer}
}

func (r *glamourInlineRenderer) render(text string) (string, bool) {
	rendered, ok := r.renderBlock(text)
	if !ok || strings.Contains(rendered, "\n") {
		return "", false
	}
	width := ansi.StringWidth(strings.TrimRight(ansi.Strip(rendered), " "))
	return ansi.Truncate(rendered, width, ""), true
}

func (r *glamourInlineRenderer) renderBlock(text string) (string, bool) {
	if r == nil || r.renderer == nil {
		return "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rendered, err := r.renderer.Render(text)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(rendered), true
}

func hasExtendedMarkdownInline(text string) bool {
	return strings.Contains(text, "~~") || strings.Contains(text, `\`)
}

func shouldUseGlamourInline(text string) bool {
	return hasExtendedMarkdownInline(text)
}

func stringPointer(value string) *string {
	return &value
}
