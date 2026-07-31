package tui

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	markdownTableColumnSeparator = " │ "
	markdownTableMinColumnWidth  = 4
	markdownTableHeaderMaxWidth  = 18
	markdownTableRuleBodyRows    = 4
	markdownBoldStart            = "\x1b[1m"
	markdownBoldEnd              = "\x1b[22m"
	markdownItalicStart          = "\x1b[3m"
	markdownItalicEnd            = "\x1b[23m"
	markdownLinkStart            = "\x1b[4m"
	markdownLinkEnd              = "\x1b[24m"
	markdownCodeStart            = "\x1b[7m"
	markdownCodeEnd              = "\x1b[27m"
	markdownStrikeStart          = "\x1b[9m"
	markdownStrikeEnd            = "\x1b[29m"
	markdownAccentStart          = "\x1b[95m"
	markdownAccentEnd            = "\x1b[39m"
	markdownGreenStart           = "\x1b[92m"
	markdownGreenEnd             = "\x1b[39m"
	markdownAmberStart           = "\x1b[93m"
	markdownAmberEnd             = "\x1b[39m"
	markdownRedStart             = "\x1b[91m"
	markdownRedEnd               = "\x1b[39m"
	markdownMutedStart           = "\x1b[90m"
	markdownMutedEnd             = "\x1b[39m"
)

var markdownBreakReplacer = strings.NewReplacer(
	"<br />", "\n",
	"<br/>", "\n",
	"<br>", "\n",
	"<BR />", "\n",
	"<BR/>", "\n",
	"<BR>", "\n",
)

type markdownDisplayStyle int

const (
	markdownDisplayNormal markdownDisplayStyle = iota
	markdownDisplayBold
	markdownDisplayItalic
	markdownDisplayLink
	markdownDisplayCode
	markdownDisplayStrike
	markdownDisplayRule
	markdownDisplayAccent
	markdownDisplayGreen
	markdownDisplayAmber
	markdownDisplayRed
	markdownDisplayMuted
)

type markdownTableAlignment int

const (
	markdownTableAlignLeft markdownTableAlignment = iota
	markdownTableAlignCenter
	markdownTableAlignRight
)

func renderAssistantMarkdownText(text string, proseMeasure int, tableMeasure int, allowHighlight bool) []string {
	if proseMeasure < 16 {
		proseMeasure = 16
	}
	if tableMeasure < proseMeasure {
		tableMeasure = proseMeasure
	}
	raw := strings.Split(strings.TrimRight(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"), "\n"), "\n")
	if len(raw) == 0 {
		return []string{""}
	}
	if allowHighlight && looksLikeBareCodeBlock(raw) {
		if highlighted, ok := highlightCodeAuto(raw, "", tableMeasure); ok {
			return trimMarkdownDisplayBlankEdges(highlighted)
		}
	}

	lines := []string{}
	// blankBefore inserts one separator blank line before a block (heading or
	// paragraph) for vertical breathing room, but never doubles an existing blank
	// and never leads with one (lines is empty at the top; trimMarkdownDisplay-
	// BlankEdges strips any leading blank anyway). Lists, code, and tables are left
	// tight — they don't call it.
	blankBefore := func() {
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
	}
	for index := 0; index < len(raw); {
		line := raw[index]
		trimmed := strings.TrimSpace(line)

		if trimmed == "" {
			lines = append(lines, "")
			index++
			continue
		}

		if fence, ok := markdownFenceMarker(trimmed); ok {
			info := strings.TrimSpace(strings.TrimPrefix(trimmed, fence))
			lang := ""
			if fields := strings.Fields(info); len(fields) > 0 {
				lang = fields[0] // first token only: "```go title=x" -> "go"
			}
			code := []string{}
			index++
			for index < len(raw) {
				if closing, ok := markdownFenceMarker(strings.TrimSpace(raw[index])); ok && closing == fence {
					index++
					break
				}
				code = append(code, strings.ReplaceAll(raw[index], "\t", "    "))
				index++
			}
			if allowHighlight {
				if highlighted, ok := highlightCodeAuto(code, lang, tableMeasure); ok {
					lines = append(lines, highlighted...)
					continue
				}
			}
			lines = append(lines, renderMarkdownCodeBlock(code, tableMeasure)...)
			continue
		}

		if index+1 < len(raw) && isMarkdownTableHeader(line, raw[index+1]) {
			tableRows := [][]string{parseMarkdownTableRow(line)}
			alignments := parseMarkdownTableAlignments(raw[index+1])
			index += 2
			for index < len(raw) && isMarkdownTableRow(raw[index]) {
				tableRows = append(tableRows, parseMarkdownTableRow(raw[index]))
				index++
			}
			lines = append(lines, renderMarkdownTable(tableRows, alignments, tableMeasure)...)
			continue
		}

		if isMarkdownHorizontalRule(trimmed) {
			blankBefore()
			lines = append(lines, renderMarkdownHorizontalRule(proseMeasure))
			index++
			continue
		}

		if level, heading := markdownHeading(line); heading != "" {
			blankBefore()
			lines = append(lines, renderMarkdownHeading(level, heading, proseMeasure)...)
			index++
			continue
		}

		if isMarkdownListLine(trimmed) {
			block := []string{}
			for index < len(raw) {
				next := raw[index]
				nextTrimmed := strings.TrimSpace(next)
				if nextTrimmed == "" {
					probe := index
					for probe < len(raw) && strings.TrimSpace(raw[probe]) == "" {
						probe++
					}
					if probe >= len(raw) || len(raw[probe])-len(strings.TrimLeft(raw[probe], " \t")) == 0 {
						break
					}
					block = append(block, next)
					index++
					continue
				}
				if !isMarkdownListLine(nextTrimmed) && len(next)-len(strings.TrimLeft(next, " \t")) == 0 {
					break
				}
				block = append(block, next)
				index++
			}
			lines = append(lines, renderMarkdownListBlock(block, proseMeasure)...)
			continue
		}

		if strings.HasPrefix(trimmed, ">") {
			block := []string{}
			for index < len(raw) {
				nextTrimmed := strings.TrimSpace(raw[index])
				if nextTrimmed == "" || !strings.HasPrefix(nextTrimmed, ">") {
					break
				}
				block = append(block, raw[index])
				index++
			}
			lines = append(lines, renderMarkdownQuoteBlock(block, proseMeasure)...)
			continue
		}

		if allowHighlight && looksLikeBareCodeLine(line) {
			code := []string{}
			probe := index
			for probe < len(raw) {
				next := raw[probe]
				nextTrimmed := strings.TrimSpace(next)
				if nextTrimmed == "" || markdownStartsBlock(raw, probe) || !looksLikeBareCodeLine(next) {
					break
				}
				code = append(code, strings.ReplaceAll(next, "\t", "    "))
				probe++
			}
			if looksLikeBareCodeBlock(code) {
				if highlighted, ok := highlightCodeAuto(code, "", tableMeasure); ok {
					lines = append(lines, highlighted...)
					index = probe
					continue
				}
			}
		}

		blankBefore()
		paragraph := []string{strings.TrimSpace(line)}
		index++
		for index < len(raw) {
			next := raw[index]
			nextTrimmed := strings.TrimSpace(next)
			if nextTrimmed == "" || markdownStartsBlock(raw, index) {
				break
			}
			paragraph = append(paragraph, nextTrimmed)
			index++
		}
		lines = append(lines, wrapMarkdownInline(strings.Join(paragraph, " "), proseMeasure)...)
	}

	return trimMarkdownDisplayBlankEdges(lines)
}

func renderStreamingAssistantMarkdownText(text string, proseMeasure int, tableMeasure int) []string {
	return renderStreamingAssistantMarkdownTextWith(text, proseMeasure, tableMeasure, renderAssistantMarkdownText)
}

func renderStreamingAssistantMarkdownTextWith(text string, proseMeasure int, tableMeasure int, renderMarkdown func(string, int, int, bool) []string) []string {
	stablePrefix := streamingMarkdownStablePrefix(text)
	completed, active, separatorLines := splitStreamingMarkdownCompleted(stablePrefix)
	lines := []string{}
	if completed != "" {
		render := func() string {
			return strings.Join(renderMarkdown(completed, proseMeasure, tableMeasure, true), "\n")
		}
		rendered := ""
		if defaultRenderCache != nil {
			key := streamingMarkdownRenderCacheKey(completed, proseMeasure, tableMeasure)
			rendered = defaultRenderCache.render(key, true, render)
		} else {
			rendered = render()
		}
		lines = append(lines, viewLines(rendered)...)
	}
	if completed != "" && active != "" {
		lines = append(lines, make([]string, separatorLines)...)
	}
	if active != "" || len(lines) == 0 {
		lines = append(lines, renderMarkdown(active, proseMeasure, tableMeasure, true)...)
	}
	return lines
}

func splitStreamingMarkdownCompleted(text string) (string, string, int) {
	openFence := ""
	lastBoundary := -1
	separatorLines := 0
	lineStart := 0
	for lineStart <= len(text) {
		lineEnd := strings.IndexByte(text[lineStart:], '\n')
		if lineEnd < 0 {
			break
		}
		lineEnd += lineStart
		trimmed := strings.TrimSpace(text[lineStart:lineEnd])
		if fence, ok := markdownFenceMarker(trimmed); ok {
			if openFence == "" {
				openFence = fence
			} else if openFence == fence {
				openFence = ""
			}
		}
		nextStart := lineEnd + 1
		if openFence == "" && trimmed == "" {
			if lastBoundary == lineStart {
				separatorLines++
			} else {
				separatorLines = 1
			}
			lastBoundary = nextStart
		}
		lineStart = nextStart
	}
	if lastBoundary < 0 {
		return "", text, 0
	}
	return strings.TrimRight(text[:lastBoundary], "\n"), strings.TrimLeft(text[lastBoundary:], "\n"), separatorLines
}

func streamingMarkdownRenderCacheKey(text string, proseMeasure int, tableMeasure int) string {
	sum := sha256.Sum256([]byte(text))
	return fmt.Sprintf("stream-md-v1:%d:%d:%x", proseMeasure, tableMeasure, sum)
}

func streamingMarkdownStablePrefix(text string) string {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	offset := 0
	openFenceStart := -1
	openFence := ""
	for offset <= len(text) {
		lineStart := offset
		next := strings.IndexByte(text[offset:], '\n')
		line := ""
		if next < 0 {
			line = text[offset:]
			offset = len(text) + 1
		} else {
			line = text[offset : offset+next]
			offset += next + 1
		}
		if fence, ok := markdownFenceMarker(strings.TrimSpace(line)); ok {
			if openFence == "" {
				openFence = fence
				openFenceStart = lineStart
			} else if fence == openFence {
				openFence = ""
				openFenceStart = -1
			}
		}
		if next < 0 {
			break
		}
	}
	if openFenceStart >= 0 {
		return strings.TrimRight(text[:openFenceStart], "\n")
	}
	return text
}

func renderAssistantMarkdownPlainText(text string, proseMeasure int, tableMeasure int) []string {
	lines := renderAssistantMarkdownText(text, proseMeasure, tableMeasure, false)
	for index := range lines {
		lines[index] = stripMarkdownRenderControls(lines[index])
	}
	return lines
}

func stripMarkdownRenderControls(text string) string {
	// renderAssistantMarkdownText embeds markdownBoldStart/markdownBoldEnd
	// markers for prose text, and highlightCodeAuto embeds ANSI color sequences
	// for fenced code blocks. Strip everything so clipboard text is pure plain text.
	return ansi.Strip(text)
}

func styleAssistantMarkdownLine(line string, base lipgloss.Style) string {
	if hasExternalANSIStyle(line) {
		return line
	}

	var builder strings.Builder
	style := markdownDisplayNormal
	var run strings.Builder

	flush := func() {
		if run.Len() == 0 {
			return
		}
		text := run.String()
		switch style {
		case markdownDisplayBold:
			builder.WriteString(kajicodeTheme.ink.Bold(true).Render(text))
		case markdownDisplayItalic:
			builder.WriteString(kajicodeTheme.muted.Italic(true).Render(text))
		case markdownDisplayLink:
			builder.WriteString(kajicodeTheme.blue.Underline(true).Render(text))
		case markdownDisplayCode:
			builder.WriteString(kajicodeTheme.accent.Render(text))
		case markdownDisplayStrike:
			builder.WriteString(kajicodeTheme.faint.Strikethrough(true).Render(text))
		case markdownDisplayRule:
			builder.WriteString(kajicodeTheme.lineStrong.Render(text))
		case markdownDisplayAccent:
			builder.WriteString(kajicodeTheme.accent.Bold(true).Render(text))
		case markdownDisplayGreen:
			builder.WriteString(kajicodeTheme.green.Bold(true).Render(text))
		case markdownDisplayAmber:
			builder.WriteString(kajicodeTheme.amber.Bold(true).Render(text))
		case markdownDisplayRed:
			builder.WriteString(kajicodeTheme.red.Bold(true).Render(text))
		case markdownDisplayMuted:
			builder.WriteString(kajicodeTheme.faint.Render(text))
		default:
			builder.WriteString(base.Render(text))
		}
		run.Reset()
	}

	for index := 0; index < len(line); {
		if markdownStyle, markerLen, ok := markdownSemanticStyleStart(line[index:]); ok {
			flush()
			style = markdownStyle
			index += markerLen
			continue
		}
		if markerLen, ok := markdownSemanticStyleEnd(line[index:]); ok {
			flush()
			style = markdownDisplayNormal
			index += markerLen
			continue
		}
		if line[index] == '\x1b' {
			// Already-styled input (highlighted code, headings, tables) carries real
			// ANSI. Emit each escape verbatim instead of treating its bytes as runes
			// and re-Render()ing them: re-wrapping doubles the SGR density and a
			// flush()/truncate can then slice mid-escape, leaking "[38;2;…" / "[1;4;…"
			// fragments into the visible text. Mirrors truncateStyledLine. The bold
			// markers above are matched first, so their semantic style switch is kept.
			if end := ansiSequenceEnd(line, index); end > index {
				flush()
				builder.WriteString(line[index:end])
				index = end
				continue
			}
		}

		r, size := utf8.DecodeRuneInString(line[index:])
		if r == utf8.RuneError && size == 0 {
			break
		}
		nextStyle := markdownDisplayStyleForRune(r, style)
		if nextStyle != style {
			flush()
			style = nextStyle
		}
		run.WriteRune(r)
		index += size
	}
	flush()
	return builder.String()
}

func hasExternalANSIStyle(line string) bool {
	for index := 0; index < len(line); {
		if line[index] != '\x1b' {
			index++
			continue
		}
		end := ansiSequenceEnd(line, index)
		if end <= index {
			index++
			continue
		}
		seq := line[index:end]
		if !isMarkdownSemanticControl(seq) {
			return true
		}
		index = end
	}
	return false
}

func markdownSemanticStyleStart(text string) (markdownDisplayStyle, int, bool) {
	for _, marker := range []struct {
		seq   string
		style markdownDisplayStyle
	}{
		{markdownBoldStart, markdownDisplayBold},
		{markdownItalicStart, markdownDisplayItalic},
		{markdownLinkStart, markdownDisplayLink},
		{markdownCodeStart, markdownDisplayCode},
		{markdownStrikeStart, markdownDisplayStrike},
		{markdownAccentStart, markdownDisplayAccent},
		{markdownGreenStart, markdownDisplayGreen},
		{markdownAmberStart, markdownDisplayAmber},
		{markdownRedStart, markdownDisplayRed},
		{markdownMutedStart, markdownDisplayMuted},
	} {
		if strings.HasPrefix(text, marker.seq) {
			return marker.style, len(marker.seq), true
		}
	}
	return markdownDisplayNormal, 0, false
}

func markdownSemanticStyleEnd(text string) (int, bool) {
	for _, marker := range []string{
		markdownBoldEnd,
		markdownItalicEnd,
		markdownLinkEnd,
		markdownCodeEnd,
		markdownStrikeEnd,
		markdownAccentEnd,
		markdownGreenEnd,
		markdownAmberEnd,
		markdownRedEnd,
		markdownMutedEnd,
	} {
		if strings.HasPrefix(text, marker) {
			return len(marker), true
		}
	}
	return 0, false
}

func isMarkdownSemanticControl(seq string) bool {
	if _, _, ok := markdownSemanticStyleStart(seq); ok {
		return true
	}
	_, ok := markdownSemanticStyleEnd(seq)
	return ok
}

func markdownDisplayStyleForRune(r rune, current markdownDisplayStyle) markdownDisplayStyle {
	switch r {
	case '│', '─', '┼', '╭', '╮', '╰', '╯', '├', '┤', '┬', '┴':
		return markdownDisplayRule
	default:
		if current == markdownDisplayRule {
			return markdownDisplayNormal
		}
		return current
	}
}

func markdownStartsBlock(lines []string, index int) bool {
	if index >= len(lines) {
		return false
	}
	trimmed := strings.TrimSpace(lines[index])
	if trimmed == "" {
		return true
	}
	if _, ok := markdownFenceMarker(trimmed); ok {
		return true
	}
	if markdownHeadingText(trimmed) != "" || isMarkdownHorizontalRule(trimmed) || isMarkdownListLine(trimmed) || strings.HasPrefix(trimmed, ">") {
		return true
	}
	return index+1 < len(lines) && isMarkdownTableHeader(lines[index], lines[index+1])
}

func isMarkdownHorizontalRule(trimmed string) bool {
	if trimmed == "" {
		return false
	}
	marker := rune(0)
	count := 0
	for _, r := range trimmed {
		if r == ' ' || r == '\t' {
			continue
		}
		if r != '-' && r != '_' && r != '*' {
			return false
		}
		if marker == 0 {
			marker = r
		} else if marker != r {
			return false
		}
		count++
	}
	return count >= 3
}

func renderMarkdownHorizontalRule(measure int) string {
	width := clampInt(measure, 16, 96)
	return strings.Repeat("─", width)
}

func looksLikeBareCodeLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	switch {
	case strings.HasPrefix(trimmed, "from ") && strings.Contains(trimmed, " import "):
		return true
	case strings.HasPrefix(trimmed, "import ") && !strings.Contains(trimmed, " from "):
		return true
	case strings.HasPrefix(trimmed, "def ") && strings.HasSuffix(trimmed, ":"):
		return true
	case strings.HasPrefix(trimmed, "class ") && strings.HasSuffix(trimmed, ":"):
		return true
	case strings.HasPrefix(trimmed, "if ") && strings.HasSuffix(trimmed, ":"):
		return true
	case strings.HasPrefix(trimmed, "elif ") && strings.HasSuffix(trimmed, ":"):
		return true
	case trimmed == "else:" || trimmed == "try:" || trimmed == "finally:":
		return true
	case strings.HasPrefix(trimmed, "for ") && strings.HasSuffix(trimmed, ":"):
		return true
	case strings.HasPrefix(trimmed, "while ") && strings.HasSuffix(trimmed, ":"):
		return true
	case strings.HasPrefix(trimmed, "with ") && strings.HasSuffix(trimmed, ":"):
		return true
	case strings.HasPrefix(trimmed, "except") && strings.HasSuffix(trimmed, ":"):
		return true
	case strings.HasPrefix(trimmed, "return "):
		return true
	case strings.HasPrefix(trimmed, "print("):
		return true
	case isSimpleFunctionCall(trimmed):
		return true
	case strings.HasPrefix(trimmed, "package "):
		return true
	case strings.HasPrefix(trimmed, "func ") && strings.Contains(trimmed, "{"):
		return true
	case strings.HasPrefix(trimmed, "const "), strings.HasPrefix(trimmed, "let "), strings.HasPrefix(trimmed, "var "):
		return true
	case strings.HasPrefix(trimmed, "function ") && strings.Contains(trimmed, "{"):
		return true
	case strings.HasPrefix(trimmed, "<!DOCTYPE "), strings.HasPrefix(trimmed, "<html"), strings.HasPrefix(trimmed, "<div"), strings.HasPrefix(trimmed, "<span"):
		return true
	}
	return false
}

func looksLikeBareCodeBlock(lines []string) bool {
	nonBlank := 0
	codeLike := 0
	strongSignals := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		nonBlank++
		if isIndentedCodeContinuation(line) {
			codeLike++
			strongSignals++
			continue
		}
		if looksLikeBareCodeLine(line) {
			codeLike++
			if looksLikeStrongBareCodeLine(line) {
				strongSignals++
			}
		}
	}
	return nonBlank >= 2 && codeLike == nonBlank && strongSignals > 0
}

func looksLikeStrongBareCodeLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(trimmed, "from ") && strings.Contains(trimmed, " import "):
		return true
	case strings.HasPrefix(trimmed, "import ") && !strings.Contains(trimmed, " from "):
		return true
	case strings.HasPrefix(trimmed, "def ") && strings.HasSuffix(trimmed, ":"):
		return true
	case strings.HasPrefix(trimmed, "class ") && strings.HasSuffix(trimmed, ":"):
		return true
	case strings.HasPrefix(trimmed, "print("):
		return true
	case isSimpleFunctionCall(trimmed):
		return true
	case strings.HasPrefix(trimmed, "package "):
		return true
	case strings.HasPrefix(trimmed, "func ") && strings.Contains(trimmed, "{"):
		return true
	case strings.HasPrefix(trimmed, "const "), strings.HasPrefix(trimmed, "let "), strings.HasPrefix(trimmed, "var "):
		return true
	case strings.HasPrefix(trimmed, "function ") && strings.Contains(trimmed, "{"):
		return true
	case strings.HasPrefix(trimmed, "<!DOCTYPE "), strings.HasPrefix(trimmed, "<html"), strings.HasPrefix(trimmed, "<div"), strings.HasPrefix(trimmed, "<span"):
		return true
	}
	return false
}

func isIndentedCodeContinuation(line string) bool {
	trimmed := strings.TrimSpace(line)
	return trimmed != "" && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t"))
}

func isSimpleFunctionCall(trimmed string) bool {
	open := strings.Index(trimmed, "(")
	return open > 0 &&
		strings.HasSuffix(trimmed, ")") &&
		isIdentifierText(trimmed[:open])
}

func isIdentifierText(text string) bool {
	if text == "" {
		return false
	}
	for index, r := range text {
		if r == '_' || unicode.IsLetter(r) || (index > 0 && unicode.IsDigit(r)) {
			continue
		}
		return false
	}
	return true
}

func markdownFenceMarker(trimmed string) (string, bool) {
	for _, marker := range []string{"```", "~~~"} {
		if strings.HasPrefix(trimmed, marker) {
			return marker, true
		}
	}
	return "", false
}

func renderMarkdownCodeBlock(code []string, measure int) []string {
	if len(code) == 0 {
		return []string{""}
	}
	lines := []string{}
	for _, line := range code {
		if line == "" {
			lines = append(lines, "")
			continue
		}
		for lipgloss.Width(line) > measure {
			head, tail := splitAtWidth(line, measure)
			lines = append(lines, head)
			line = tail
		}
		lines = append(lines, line)
	}
	return lines
}

func isMarkdownTableHeader(header string, separator string) bool {
	headerCells := parseMarkdownTableRow(header)
	separatorCells := parseMarkdownTableRow(separator)
	if len(headerCells) < 2 || len(headerCells) != len(separatorCells) {
		return false
	}
	for _, cell := range separatorCells {
		if !isMarkdownTableSeparatorCell(cell) {
			return false
		}
	}
	return true
}

func isMarkdownTableRow(line string) bool {
	cells := parseMarkdownTableRow(line)
	return len(cells) >= 2
}

func parseMarkdownTableRow(line string) []string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || !strings.Contains(trimmed, "|") {
		return nil
	}
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	parts := strings.Split(trimmed, "|")
	cells := make([]string, 0, len(parts))
	for _, part := range parts {
		cells = append(cells, normalizeMarkdownTableCell(part))
	}
	return cells
}

func normalizeMarkdownTableCell(cell string) string {
	return strings.TrimSpace(markdownBreakReplacer.Replace(cell))
}

func isMarkdownTableSeparatorCell(cell string) bool {
	cell = strings.TrimSpace(cell)
	cell = strings.Trim(cell, ":")
	if len(cell) < 3 {
		return false
	}
	for _, r := range cell {
		if r != '-' {
			return false
		}
	}
	return true
}

func parseMarkdownTableAlignments(separator string) []markdownTableAlignment {
	cells := parseMarkdownTableRow(separator)
	alignments := make([]markdownTableAlignment, len(cells))
	for index, cell := range cells {
		cell = strings.TrimSpace(cell)
		left := strings.HasPrefix(cell, ":")
		right := strings.HasSuffix(cell, ":")
		switch {
		case left && right:
			alignments[index] = markdownTableAlignCenter
		case right:
			alignments[index] = markdownTableAlignRight
		default:
			alignments[index] = markdownTableAlignLeft
		}
	}
	return alignments
}

func renderMarkdownTable(rows [][]string, alignments []markdownTableAlignment, measure int) []string {
	rows, columns := normalizeMarkdownTableRows(rows)
	if columns == 0 {
		return nil
	}
	alignments = normalizeMarkdownTableAlignments(alignments, columns)
	widths := markdownTableWidths(rows, columns, measure)
	renderedRows := make([][]string, len(rows))
	for rowIndex, row := range rows {
		renderedRows[rowIndex] = renderMarkdownTableRow(row, widths, alignments, rowIndex == 0)
	}
	separateBodyRows := markdownTableNeedsBodyRules(renderedRows)
	out := []string{markdownTableTopRule(widths)}
	for rowIndex, rowLines := range renderedRows {
		if separateBodyRows && rowIndex > 1 {
			out = append(out, markdownTableRule(widths))
		}
		out = append(out, rowLines...)
		if rowIndex == 0 && len(rows) > 1 {
			out = append(out, markdownTableRule(widths))
		}
	}
	out = append(out, markdownTableBottomRule(widths))
	return out
}

func markdownTableNeedsBodyRules(renderedRows [][]string) bool {
	if len(renderedRows) > markdownTableRuleBodyRows {
		return true
	}
	for _, row := range renderedRows[1:] {
		if len(row) > 1 {
			return true
		}
	}
	return false
}

func wrapMarkdownTableCellPart(prefix string, text string, measure int) []string {
	continuationPrefix := strings.Repeat(" ", lipgloss.Width(prefix))
	lines := []string{}
	for index, segment := range strings.Split(text, "\n") {
		firstPrefix := prefix
		if index > 0 {
			firstPrefix = continuationPrefix
		}
		lines = append(lines, wrapMarkdownInlineWithPrefixes(firstPrefix, continuationPrefix, segment, measure)...)
	}
	if len(lines) == 0 {
		return []string{prefix}
	}
	return lines
}

type markdownTableCellPart struct {
	text   string
	bullet bool
}

func markdownTableCellParts(value string) []markdownTableCellPart {
	value = strings.TrimSpace(value)
	starts := markdownInlineBulletStarts(value)
	if len(starts) == 0 {
		return []markdownTableCellPart{{text: value}}
	}

	parts := []markdownTableCellPart{}
	if starts[0] > 0 {
		if text := strings.TrimSpace(value[:starts[0]]); text != "" {
			parts = append(parts, markdownTableCellPart{text: text})
		}
	}
	for index, start := range starts {
		end := len(value)
		if index+1 < len(starts) {
			end = starts[index+1]
		}
		text := strings.TrimSpace(value[start:end])
		text = strings.TrimSpace(strings.TrimPrefix(text, "-"))
		if text != "" {
			parts = append(parts, markdownTableCellPart{text: text, bullet: true})
		}
	}
	if len(parts) == 0 {
		return []markdownTableCellPart{{text: value}}
	}
	return parts
}

func markdownInlineBulletStarts(value string) []int {
	starts := []int{}
	if strings.HasPrefix(value, "- ") {
		starts = append(starts, 0)
	}
	offset := 0
	for {
		index := strings.Index(value[offset:], " - ")
		if index < 0 {
			break
		}
		hyphen := offset + index + 1
		candidate := hyphen + 2
		if isProbableInlineBulletLabel(value[candidate:]) {
			starts = append(starts, hyphen)
		}
		offset = candidate
	}
	return starts
}

func isProbableInlineBulletLabel(text string) bool {
	colon := strings.IndexByte(text, ':')
	if colon <= 0 || colon > 48 {
		return false
	}
	label := text[:colon]
	return !strings.ContainsAny(label, ".!?;")
}

func normalizeMarkdownTableRows(rows [][]string) ([][]string, int) {
	columns := 0
	for _, row := range rows {
		if len(row) > columns {
			columns = len(row)
		}
	}
	if columns == 0 {
		return nil, 0
	}
	normalized := make([][]string, 0, len(rows))
	for _, row := range rows {
		next := make([]string, columns)
		copy(next, row)
		normalized = append(normalized, next)
	}
	return normalized, columns
}

func normalizeMarkdownTableAlignments(alignments []markdownTableAlignment, columns int) []markdownTableAlignment {
	normalized := make([]markdownTableAlignment, columns)
	copy(normalized, alignments)
	return normalized
}

func markdownTableWidths(rows [][]string, columns int, measure int) []int {
	separatorWidth := lipgloss.Width(markdownTableColumnSeparator)*maxInt(0, columns-1) + 4
	contentWidth := maxInt(columns*markdownTableMinColumnWidth, measure-separatorWidth)
	widths := make([]int, columns)
	minWidths := make([]int, columns)
	for column := range widths {
		headerWidth := 0
		if len(rows) > 0 && column < len(rows[0]) {
			headerWidth = lipgloss.Width(markdownInlinePlain(rows[0][column]))
		}
		minWidths[column] = clampInt(headerWidth, markdownTableMinColumnWidth, markdownTableHeaderMaxWidth)
		widths[column] = minWidths[column]
	}
	for _, row := range rows {
		for column, cell := range row {
			if column >= len(widths) {
				continue
			}
			for _, line := range markdownTableCellWidthParts(cell) {
				plainLine := markdownInlinePlain(line)
				lineWidth := lipgloss.Width(plainLine)
				if wordWidth := longestMarkdownWordWidth(plainLine); wordWidth > lineWidth {
					lineWidth = wordWidth
				}
				if lineWidth > widths[column] {
					widths[column] = minInt(lineWidth, contentWidth)
				}
			}
		}
	}
	for sumInts(widths) > contentWidth && shrinkMarkdownTableWidth(widths, minWidths) {
	}
	for sumInts(widths) > contentWidth {
		largest := 0
		for index, width := range widths {
			if width > widths[largest] {
				largest = index
			}
		}
		if widths[largest] <= markdownTableMinColumnWidth {
			break
		}
		widths[largest]--
	}
	return widths
}

func shrinkMarkdownTableWidth(widths []int, minWidths []int) bool {
	largest := -1
	for index, width := range widths {
		if width <= minWidths[index] {
			continue
		}
		if largest < 0 || width > widths[largest] {
			largest = index
		}
	}
	if largest < 0 {
		return false
	}
	widths[largest]--
	return true
}

func renderMarkdownTableRow(row []string, widths []int, alignments []markdownTableAlignment, header bool) []string {
	wrapped := make([][]string, len(widths))
	height := 1
	for column, width := range widths {
		cell := ""
		if column < len(row) {
			cell = row[column]
		}
		wrapped[column] = markdownTableCellLines(cell, width)
		if len(wrapped[column]) == 0 {
			wrapped[column] = []string{""}
		}
		if len(wrapped[column]) > height {
			height = len(wrapped[column])
		}
	}

	lines := make([]string, 0, height)
	for lineIndex := 0; lineIndex < height; lineIndex++ {
		parts := make([]string, len(widths))
		for column, width := range widths {
			cell := ""
			if lineIndex < len(wrapped[column]) {
				cell = wrapped[column][lineIndex]
			}
			if header && cell != "" {
				cell = renderMarkdownBoldText(cell)
			}
			parts[column] = alignMarkdownTableCell(cell, width, alignments[column])
		}
		lines = append(lines, "│ "+strings.Join(parts, markdownTableColumnSeparator)+" │")
	}
	return lines
}

func markdownTableCellWidthParts(cell string) []string {
	parts := markdownTableCellParts(cell)
	lines := []string{}
	for _, part := range parts {
		line := part.text
		if part.bullet {
			line = "- " + line
		}
		for _, segment := range strings.Split(line, "\n") {
			if segment != "" {
				lines = append(lines, segment)
			}
		}
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func markdownTableCellLines(cell string, width int) []string {
	parts := markdownTableCellParts(cell)
	if len(parts) == 1 && !parts[0].bullet {
		return wrapMarkdownInline(cell, width)
	}
	lines := []string{}
	for _, part := range parts {
		prefix := ""
		if part.bullet {
			prefix = "- "
		}
		lines = append(lines, wrapMarkdownTableCellPart(prefix, part.text, width)...)
	}
	return lines
}

func alignMarkdownTableCell(text string, width int, alignment markdownTableAlignment) string {
	padding := width - lipgloss.Width(text)
	if padding <= 0 {
		return text
	}
	switch alignment {
	case markdownTableAlignRight:
		return strings.Repeat(" ", padding) + text
	case markdownTableAlignCenter:
		left := padding / 2
		right := padding - left
		return strings.Repeat(" ", left) + text + strings.Repeat(" ", right)
	default:
		return text + strings.Repeat(" ", padding)
	}
}

func markdownTableRule(widths []int) string {
	return markdownTableBorderRule(widths, "├", "┼", "┤")
}

func markdownTableTopRule(widths []int) string {
	return markdownTableBorderRule(widths, "╭", "┬", "╮")
}

func markdownTableBottomRule(widths []int) string {
	return markdownTableBorderRule(widths, "╰", "┴", "╯")
}

func markdownTableBorderRule(widths []int, left string, separator string, right string) string {
	parts := make([]string, len(widths))
	for index, width := range widths {
		parts[index] = strings.Repeat("─", maxInt(1, width+2))
	}
	return left + strings.Join(parts, separator) + right
}

func markdownHeadingText(trimmed string) string {
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || level >= len(trimmed) || trimmed[level] != ' ' {
		return ""
	}
	return strings.TrimSpace(trimmed[level:])
}

func isMarkdownListLine(trimmed string) bool {
	if len(trimmed) >= 2 {
		if strings.Contains("-*+", string(trimmed[0])) && trimmed[1] == ' ' {
			return true
		}
	}
	dot := strings.IndexByte(trimmed, '.')
	if dot <= 0 || dot+1 >= len(trimmed) || trimmed[dot+1] != ' ' {
		return false
	}
	for _, r := range trimmed[:dot] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func renderMarkdownStandaloneLine(line string) string {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, ">") {
		return "│ " + strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))
	}
	return strings.TrimRight(line, " ")
}

type markdownInlineKind int

const (
	markdownInlineText markdownInlineKind = iota
	markdownInlineCode
	markdownInlineLink
	markdownInlineMark
	markdownInlineInsert
)

type markdownInlineSegment struct {
	text   string
	kind   markdownInlineKind
	bold   bool
	italic bool
	strike bool
}

func wrapMarkdownInline(text string, measure int) []string {
	if measure < 1 {
		measure = 1
	}
	text = normalizeMarkdownInlineBreaks(text)
	out := []string{}
	for _, paragraph := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(paragraph) == "" {
			out = append(out, "")
			continue
		}
		body := strings.TrimLeft(paragraph, " \t")
		indent := strings.ReplaceAll(paragraph[:len(paragraph)-len(body)], "\t", "    ")
		if len(indent) >= measure {
			indent = strings.Repeat(" ", measure/2)
		}
		out = append(out, wrapMarkdownInlineWithPrefixes(indent, indent, body, measure)...)
	}
	return out
}

func wrapMarkdownInlineWithPrefixes(firstPrefix string, continuationPrefix string, text string, measure int) []string {
	words := markdownInlineWords(parseMarkdownInline(text))
	if len(words) == 0 {
		return []string{firstPrefix}
	}

	lines := []string{}
	line := ""
	lineWidth := 0
	prefix := firstPrefix
	available := maxInt(1, measure-lipgloss.Width(prefix))

	flush := func() {
		lines = append(lines, prefix+line)
		line = ""
		lineWidth = 0
		prefix = continuationPrefix
		available = maxInt(1, measure-lipgloss.Width(prefix))
	}

	for _, word := range words {
		for lipgloss.Width(word.text) > available {
			if line != "" {
				flush()
			}
			head, tail := splitAtWidth(word.text, available)
			lines = append(lines, prefix+renderMarkdownInlineSegment(markdownInlineSegment{text: head, kind: word.kind, bold: word.bold, italic: word.italic, strike: word.strike}))
			word.text = tail
			prefix = continuationPrefix
			available = maxInt(1, measure-lipgloss.Width(prefix))
		}
		wordWidth := lipgloss.Width(word.text)
		rendered := renderMarkdownInlineSegment(word)
		separator := " "
		separatorWidth := 1
		if joinsPreviousMarkdownWord(word.text) {
			separator = ""
			separatorWidth = 0
		}
		switch {
		case line == "":
			line = rendered
			lineWidth = wordWidth
		case lineWidth+separatorWidth+wordWidth <= available:
			line += separator + rendered
			lineWidth += separatorWidth + wordWidth
		default:
			flush()
			line = rendered
			lineWidth = wordWidth
		}
	}
	if line != "" {
		lines = append(lines, prefix+line)
	}
	return lines
}

func wrapANSITextWithPrefixes(firstPrefix string, continuationPrefix string, text string, measure int) []string {
	available := measure - maxInt(lipgloss.Width(firstPrefix), lipgloss.Width(continuationPrefix))
	wrapped := strings.Split(ansi.Wordwrap(text, maxInt(1, available), ""), "\n")
	for index := range wrapped {
		prefix := continuationPrefix
		if index == 0 {
			prefix = firstPrefix
		}
		wrapped[index] = prefix + wrapped[index]
	}
	return wrapped
}

func markdownInlineWords(segments []markdownInlineSegment) []markdownInlineSegment {
	words := []markdownInlineSegment{}
	for _, segment := range segments {
		for _, word := range strings.Fields(segment.text) {
			words = append(words, markdownInlineSegment{text: word, kind: segment.kind, bold: segment.bold, italic: segment.italic, strike: segment.strike})
		}
	}
	return words
}

func joinsPreviousMarkdownWord(text string) bool {
	return text != "" && strings.Trim(text, ".,!?;:%)]}") == ""
}

func renderMarkdownInline(text string) string {
	segments := parseMarkdownInline(text)
	var builder strings.Builder
	for _, segment := range segments {
		builder.WriteString(renderMarkdownInlineSegment(segment))
	}
	return builder.String()
}

func renderMarkdownInlineSegment(segment markdownInlineSegment) string {
	if segment.text == "" {
		return ""
	}
	text := segment.text
	switch segment.kind {
	case markdownInlineCode:
		text = markdownCodeStart + text + markdownCodeEnd
	case markdownInlineLink:
		text = markdownLinkStart + text + markdownLinkEnd
	case markdownInlineMark:
		text = markdownAmberStart + text + markdownAmberEnd
	case markdownInlineInsert:
		text = markdownGreenStart + text + markdownGreenEnd
	}
	if segment.bold && segment.kind != markdownInlineCode {
		text = renderMarkdownBoldText(text)
	}
	if segment.italic && segment.kind != markdownInlineCode {
		text = markdownItalicStart + text + markdownItalicEnd
	}
	if segment.strike && segment.kind != markdownInlineCode {
		text = markdownStrikeStart + text + markdownStrikeEnd
	}
	return text
}

func renderMarkdownBoldText(text string) string {
	return markdownBoldStart + text + markdownBoldEnd
}

func markdownInlinePlain(text string) string {
	segments := parseMarkdownInline(text)
	var builder strings.Builder
	for _, segment := range segments {
		builder.WriteString(segment.text)
	}
	return strings.TrimSpace(builder.String())
}

func parseMarkdownInline(text string) []markdownInlineSegment {
	text = normalizeMarkdownInlineBreaks(text)
	segments := []markdownInlineSegment{}
	var builder strings.Builder
	bold := false
	emphasis := false
	code := false
	strike := false
	mark := false
	insert := false
	flush := func() {
		if builder.Len() == 0 {
			return
		}
		segments = append(segments, markdownInlineSegment{
			text:   builder.String(),
			kind:   markdownInlineKindForState(code, mark, insert),
			bold:   bold && !code,
			italic: emphasis && !code,
			strike: strike && !code,
		})
		builder.Reset()
	}

	for index := 0; index < len(text); {
		if text[index] == '<' {
			switch {
			case !code && markdownHasPrefixFold(text[index:], "<kbd>"):
				flush()
				code = true
				index += len("<kbd>")
				continue
			case code && markdownHasPrefixFold(text[index:], "</kbd>"):
				flush()
				code = false
				index += len("</kbd>")
				continue
			case !code && markdownHasPrefixFold(text[index:], "<code>"):
				flush()
				code = true
				index += len("<code>")
				continue
			case code && markdownHasPrefixFold(text[index:], "</code>"):
				flush()
				code = false
				index += len("</code>")
				continue
			case !code && markdownHasPrefixFold(text[index:], "<strong>"):
				flush()
				bold = true
				index += len("<strong>")
				continue
			case !code && markdownHasPrefixFold(text[index:], "</strong>"):
				flush()
				bold = false
				index += len("</strong>")
				continue
			case !code && markdownHasPrefixFold(text[index:], "<b>"):
				flush()
				bold = true
				index += len("<b>")
				continue
			case !code && markdownHasPrefixFold(text[index:], "</b>"):
				flush()
				bold = false
				index += len("</b>")
				continue
			case !code && markdownHasPrefixFold(text[index:], "<em>"):
				flush()
				emphasis = true
				index += len("<em>")
				continue
			case !code && markdownHasPrefixFold(text[index:], "</em>"):
				flush()
				emphasis = false
				index += len("</em>")
				continue
			case !code && markdownHasPrefixFold(text[index:], "<i>"):
				flush()
				emphasis = true
				index += len("<i>")
				continue
			case !code && markdownHasPrefixFold(text[index:], "</i>"):
				flush()
				emphasis = false
				index += len("</i>")
				continue
			case !code && markdownHasPrefixFold(text[index:], "<mark>"):
				flush()
				mark = true
				index += len("<mark>")
				continue
			case !code && markdownHasPrefixFold(text[index:], "</mark>"):
				flush()
				mark = false
				index += len("</mark>")
				continue
			case !code && markdownHasPrefixFold(text[index:], "<ins>"):
				flush()
				insert = true
				index += len("<ins>")
				continue
			case !code && markdownHasPrefixFold(text[index:], "</ins>"):
				flush()
				insert = false
				index += len("</ins>")
				continue
			case !code && markdownHasPrefixFold(text[index:], "<del>"):
				flush()
				strike = true
				index += len("<del>")
				continue
			case !code && markdownHasPrefixFold(text[index:], "</del>"):
				flush()
				strike = false
				index += len("</del>")
				continue
			case !code && markdownHasPrefixFold(text[index:], "<s>"):
				flush()
				strike = true
				index += len("<s>")
				continue
			case !code && markdownHasPrefixFold(text[index:], "</s>"):
				flush()
				strike = false
				index += len("</s>")
				continue
			case !code:
				if label, end, ok := markdownAutolinkLabel(text, index); ok {
					flush()
					segments = append(segments, markdownInlineSegment{text: label, kind: markdownInlineLink})
					index = end
					continue
				}
			}
		}

		switch {
		case !code && text[index] == '!' && index+1 < len(text) && text[index+1] == '[':
			if label, end, ok := markdownLinkLabel(text, index+1); ok {
				flush()
				segments = append(segments, markdownInlineSegment{text: "image: " + label, italic: true})
				index = end
				continue
			}
		case !code && text[index] == '[':
			if label, end, ok := markdownLinkLabel(text, index); ok {
				flush()
				segments = append(segments, markdownInlineSegment{text: label, kind: markdownInlineLink})
				index = end
				continue
			}
		case text[index] == '\\' && index+1 < len(text) && strings.ContainsRune("\\`*_{}[]()#+-.!>|~", rune(text[index+1])):
			builder.WriteByte(text[index+1])
			index += 2
			continue
		case strings.HasPrefix(text[index:], "~~"):
			if !code && (strike || strings.Contains(text[index+2:], "~~")) {
				flush()
				strike = !strike
				index += 2
				continue
			}
		case strings.HasPrefix(text[index:], "=="):
			if !code && (mark || strings.Contains(text[index+2:], "==")) {
				flush()
				mark = !mark
				index += 2
				continue
			}
		case strings.HasPrefix(text[index:], "++"):
			if !code && (insert || strings.Contains(text[index+2:], "++")) {
				flush()
				insert = !insert
				index += 2
				continue
			}
		case strings.HasPrefix(text[index:], "**"):
			if !code && ((bold && canCloseMarkdownDelimiter(text, index, "**")) || (!bold && canOpenMarkdownDelimiter(text, index, "**") && hasClosingMarkdownDelimiter(text, index+2, "**"))) {
				flush()
				bold = !bold
				index += 2
				continue
			}
		case strings.HasPrefix(text[index:], "__"):
			if !code && ((bold && canCloseMarkdownDelimiter(text, index, "__")) || (!bold && canOpenMarkdownDelimiter(text, index, "__") && hasClosingMarkdownDelimiter(text, index+2, "__"))) {
				flush()
				bold = !bold
				index += 2
				continue
			}
		case text[index] == '`':
			if code || strings.Contains(text[index+1:], "`") {
				flush()
				code = !code
				index++
				continue
			}
		case text[index] == '*':
			if !code && !strings.HasPrefix(text[index:], "**") && ((emphasis && canCloseMarkdownDelimiter(text, index, "*")) || (!emphasis && canOpenMarkdownDelimiter(text, index, "*") && hasClosingMarkdownDelimiter(text, index+1, "*"))) {
				flush()
				emphasis = !emphasis
				index++
				continue
			}
		}

		r, size := utf8.DecodeRuneInString(text[index:])
		if r == utf8.RuneError && size == 0 {
			break
		}
		builder.WriteRune(r)
		index += size
	}
	flush()
	return segments
}

func markdownInlineKindForState(code bool, mark bool, insert bool) markdownInlineKind {
	switch {
	case code:
		return markdownInlineCode
	case mark:
		return markdownInlineMark
	case insert:
		return markdownInlineInsert
	default:
		return markdownInlineText
	}
}

func normalizeMarkdownInlineBreaks(text string) string {
	return markdownBreakReplacer.Replace(text)
}

func markdownHasPrefixFold(text string, prefix string) bool {
	return len(text) >= len(prefix) && strings.EqualFold(text[:len(prefix)], prefix)
}

func markdownLinkLabel(text string, open int) (string, int, bool) {
	close := findUnescapedByte(text, open+1, ']')
	if close < 0 || close+1 >= len(text) || text[close+1] != '(' {
		return "", 0, false
	}
	destinationEnd := findUnescapedByte(text, close+2, ')')
	if destinationEnd < 0 {
		return "", 0, false
	}
	return text[open+1 : close], destinationEnd + 1, true
}

func markdownAutolinkLabel(text string, open int) (string, int, bool) {
	close := findUnescapedByte(text, open+1, '>')
	if close < 0 {
		return "", 0, false
	}
	label := strings.TrimSpace(text[open+1 : close])
	lower := strings.ToLower(label)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.Contains(label, "@") {
		return label, close + 1, true
	}
	return "", 0, false
}

func findUnescapedByte(text string, start int, want byte) int {
	escaped := false
	for index := start; index < len(text); index++ {
		switch {
		case escaped:
			escaped = false
		case text[index] == '\\':
			escaped = true
		case text[index] == want:
			return index
		}
	}
	return -1
}

func hasClosingMarkdownDelimiter(text string, start int, marker string) bool {
	for index := start; index < len(text); index++ {
		if !strings.HasPrefix(text[index:], marker) {
			continue
		}
		if marker == "*" && index+1 < len(text) && text[index+1] == '*' {
			index++
			continue
		}
		if canCloseMarkdownDelimiter(text, index, marker) {
			return true
		}
		index += len(marker) - 1
	}
	return false
}

func canOpenMarkdownDelimiter(text string, index int, marker string) bool {
	before, hasBefore := markdownRuneBefore(text, index)
	after, hasAfter := markdownRuneAfter(text, index+len(marker))
	if !hasAfter || unicode.IsSpace(after) {
		return false
	}
	if hasBefore && markdownIsWordRune(before) {
		return false
	}
	if marker == "__" && isMarkdownDunderIdentifier(text, index) {
		return false
	}
	return true
}

func canCloseMarkdownDelimiter(text string, index int, marker string) bool {
	before, hasBefore := markdownRuneBefore(text, index)
	after, hasAfter := markdownRuneAfter(text, index+len(marker))
	if !hasBefore || unicode.IsSpace(before) {
		return false
	}
	if hasAfter && markdownIsWordRune(after) {
		return false
	}
	return true
}

func isMarkdownDunderIdentifier(text string, index int) bool {
	end := strings.Index(text[index+2:], "__")
	if end < 0 {
		return false
	}
	body := text[index+2 : index+2+end]
	if body == "" {
		return false
	}
	for _, r := range body {
		if !markdownIsWordRune(r) {
			return false
		}
	}
	return true
}

func markdownRuneBefore(text string, index int) (rune, bool) {
	if index <= 0 {
		return 0, false
	}
	r, _ := utf8.DecodeLastRuneInString(text[:index])
	return r, r != utf8.RuneError
}

func markdownRuneAfter(text string, index int) (rune, bool) {
	if index >= len(text) {
		return 0, false
	}
	r, _ := utf8.DecodeRuneInString(text[index:])
	return r, r != utf8.RuneError
}

func markdownIsWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func longestMarkdownWordWidth(text string) int {
	longest := 0
	for _, word := range strings.Fields(text) {
		if width := lipgloss.Width(word); width > longest {
			longest = width
		}
	}
	return longest
}

func sumInts(values []int) int {
	sum := 0
	for _, value := range values {
		sum += value
	}
	return sum
}

func trimMarkdownDisplayBlankEdges(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}
