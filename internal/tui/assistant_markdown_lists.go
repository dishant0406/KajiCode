package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

type markdownListItemLine struct {
	indent string
	marker string
	style  markdownDisplayStyle
	body   string
}

func renderMarkdownListBlock(lines []string, measure int) []string {
	out := []string{}
	for _, line := range normalizeMarkdownListDisplayBlock(lines) {
		if strings.TrimSpace(line) == "" {
			if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
				out = append(out, "")
			}
			continue
		}
		if item, ok := parseMarkdownListItemLine(line, measure); ok {
			firstPrefix, continuationPrefix := markdownListItemPrefixes(item, measure)
			out = append(out, wrapMarkdownInlineWithPrefixes(firstPrefix, continuationPrefix, item.body, measure)...)
			continue
		}
		prefix, body := markdownListContinuationParts(line, measure)
		out = append(out, wrapMarkdownInlineWithPrefixes(prefix, prefix, body, measure)...)
	}
	return out
}

func normalizeMarkdownListDisplayBlock(lines []string) []string {
	normalized := make([]string, 0, len(lines))
	for index, line := range lines {
		if strings.TrimSpace(line) == "" && nextMarkdownListBlockLineIndented(lines, index+1) {
			continue
		}
		normalized = append(normalized, line)
	}
	return normalized
}

func nextMarkdownListBlockLineIndented(lines []string, start int) bool {
	if start >= len(lines) {
		return false
	}
	return markdownDisplayIndent(lines[start]) != ""
}

func parseMarkdownListItemLine(line string, measure int) (markdownListItemLine, bool) {
	indent, body := markdownListContinuationParts(line, measure)
	if len(body) >= 2 && body[1] == ' ' && isMarkdownUnorderedListMarker(body[0]) {
		itemBody := strings.TrimLeft(body[2:], " ")
		if marker, taskBody, ok := parseMarkdownTaskMarker(itemBody); ok {
			return markdownListItemLine{indent: indent, marker: marker, style: markdownTaskMarkerStyle(marker), body: taskBody}, true
		}
		return markdownListItemLine{indent: indent, marker: "• ", style: markdownBulletMarkerStyle(indent), body: itemBody}, true
	}
	dot := strings.IndexByte(body, '.')
	if dot <= 0 || dot+1 >= len(body) || body[dot+1] != ' ' {
		return markdownListItemLine{}, false
	}
	for _, r := range body[:dot] {
		if r < '0' || r > '9' {
			return markdownListItemLine{}, false
		}
	}
	return markdownListItemLine{indent: indent, marker: body[:dot+2], style: markdownDisplayAmber, body: strings.TrimLeft(body[dot+2:], " ")}, true
}

func isMarkdownUnorderedListMarker(marker byte) bool {
	return marker == '-' || marker == '*' || marker == '+'
}

func parseMarkdownTaskMarker(text string) (string, string, bool) {
	if len(text) < len("[ ]") || text[0] != '[' || text[2] != ']' {
		return "", "", false
	}
	if len(text) > len("[ ]") && text[3] != ' ' {
		return "", "", false
	}
	switch text[1] {
	case ' ', 'x', 'X':
		marker := "[ ] "
		if text[1] == 'x' || text[1] == 'X' {
			marker = "[x] "
		}
		return marker, strings.TrimLeft(text[minInt(4, len(text)):], " "), true
	default:
		return "", "", false
	}
}

func markdownListContinuationParts(line string, measure int) (string, string) {
	expanded := strings.ReplaceAll(strings.TrimRight(line, " "), "\t", "    ")
	body := strings.TrimLeft(expanded, " ")
	indent := markdownListDisplayIndent(len(expanded) - len(body))
	if lipgloss.Width(indent) >= measure {
		indent = strings.Repeat(" ", maxInt(0, measure/2))
	}
	return indent, body
}

func markdownDisplayIndent(line string) string {
	expanded := strings.ReplaceAll(line, "\t", "    ")
	body := strings.TrimLeft(expanded, " ")
	return markdownListDisplayIndent(len(expanded) - len(body))
}

func markdownListDisplayIndent(width int) string {
	if width <= 0 {
		return ""
	}
	level := (width + 3) / 4
	return strings.Repeat(" ", level*2)
}

func markdownListItemPrefixes(item markdownListItemLine, measure int) (string, string) {
	markerWidth := lipgloss.Width(item.marker)
	maxIndent := maxInt(0, measure-markerWidth-1)
	indent := item.indent
	if lipgloss.Width(indent) > maxIndent {
		indent = strings.Repeat(" ", maxIndent)
	}
	firstPrefix := indent + markdownStyledMarker(item.marker, item.style)
	return firstPrefix, strings.Repeat(" ", lipgloss.Width(firstPrefix))
}

func markdownStyledMarker(marker string, style markdownDisplayStyle) string {
	start, end := markdownStyleMarkers(style)
	return start + marker + end
}

func markdownBulletMarkerStyle(indent string) markdownDisplayStyle {
	switch lipgloss.Width(indent) / 2 {
	case 0:
		return markdownDisplayAccent
	case 1:
		return markdownDisplayLink
	default:
		return markdownDisplayGreen
	}
}

func markdownTaskMarkerStyle(marker string) markdownDisplayStyle {
	if strings.HasPrefix(strings.ToLower(marker), "[x]") {
		return markdownDisplayGreen
	}
	return markdownDisplayAmber
}
