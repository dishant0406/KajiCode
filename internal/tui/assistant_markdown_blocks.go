package tui

import (
	"strings"
)

type markdownCallout struct {
	label string
	style markdownDisplayStyle
	title string
}

func markdownHeading(line string) (int, string) {
	trimmed := strings.TrimSpace(line)
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || level >= len(trimmed) || trimmed[level] != ' ' {
		return 0, ""
	}
	return level, strings.TrimSpace(trimmed[level:])
}

func renderMarkdownHeading(level int, text string, measure int) []string {
	styleStart := markdownAccentStart
	styleEnd := markdownAccentEnd
	prefix := ""
	switch {
	case level <= 1:
		prefix = "▸ "
	case level == 2:
		prefix = "◇ "
		styleStart = markdownLinkStart
		styleEnd = markdownLinkEnd
	case level == 3:
		prefix = "· "
		styleStart = markdownGreenStart
		styleEnd = markdownGreenEnd
	default:
		styleStart = markdownMutedStart
		styleEnd = markdownMutedEnd
	}
	rendered := renderMarkdownInline(text)
	lines := wrapANSITextWithPrefixes(prefix, strings.Repeat(" ", len(prefix)), stripMarkdownRenderControls(rendered), measure)
	for index := range lines {
		lines[index] = styleStart + lines[index] + styleEnd
	}
	return lines
}

func renderMarkdownQuoteBlock(lines []string, measure int) []string {
	content := markdownQuoteContent(lines)
	if len(content) == 0 {
		return nil
	}
	callout, hasCallout := parseMarkdownCallout(content[0])
	if hasCallout {
		content = content[1:]
		return renderMarkdownCalloutBlock(callout, content, measure)
	}
	out := []string{}
	for _, line := range content {
		out = append(out, wrapMarkdownInlineWithPrefixes(markdownMutedStart+"│ "+markdownMutedEnd, markdownMutedStart+"│ "+markdownMutedEnd, line, measure)...)
	}
	return out
}

func markdownQuoteContent(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, ">") {
			continue
		}
		content := strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))
		out = append(out, content)
	}
	return out
}

func parseMarkdownCallout(line string) (markdownCallout, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "[!") {
		return markdownCallout{}, false
	}
	close := strings.IndexByte(trimmed, ']')
	if close < 3 {
		return markdownCallout{}, false
	}
	kind := strings.ToUpper(strings.TrimSpace(trimmed[2:close]))
	title := strings.TrimSpace(trimmed[close+1:])
	callout := markdownCallout{label: kind, title: title, style: markdownDisplayAccent}
	switch kind {
	case "NOTE", "INFO":
		callout.label, callout.style = "NOTE", markdownDisplayLink
	case "TIP", "SUCCESS":
		callout.label, callout.style = "TIP", markdownDisplayGreen
	case "WARNING", "WARN", "IMPORTANT":
		callout.label, callout.style = "WARN", markdownDisplayAmber
	case "CAUTION", "DANGER", "ERROR":
		callout.label, callout.style = "ERROR", markdownDisplayRed
	}
	if callout.title == "" {
		callout.title = markdownTitle(callout.label)
	}
	return callout, true
}

func renderMarkdownCalloutBlock(callout markdownCallout, content []string, measure int) []string {
	start, end := markdownStyleMarkers(callout.style)
	prefix := start + "▌ " + end
	title := start + callout.label + end + " " + markdownBoldStart + callout.title + markdownBoldEnd
	out := wrapANSITextWithPrefixes(prefix, markdownMutedStart+"│ "+markdownMutedEnd, title, measure)
	for _, line := range content {
		if strings.TrimSpace(line) == "" {
			out = append(out, markdownMutedStart+"│"+markdownMutedEnd)
			continue
		}
		out = append(out, wrapMarkdownInlineWithPrefixes(markdownMutedStart+"│ "+markdownMutedEnd, markdownMutedStart+"│ "+markdownMutedEnd, line, measure)...)
	}
	return out
}

func markdownStyleMarkers(style markdownDisplayStyle) (string, string) {
	switch style {
	case markdownDisplayMuted:
		return markdownMutedStart, markdownMutedEnd
	case markdownDisplayGreen:
		return markdownGreenStart, markdownGreenEnd
	case markdownDisplayAmber:
		return markdownAmberStart, markdownAmberEnd
	case markdownDisplayRed:
		return markdownRedStart, markdownRedEnd
	case markdownDisplayLink:
		return markdownLinkStart, markdownLinkEnd
	default:
		return markdownAccentStart, markdownAccentEnd
	}
}

func markdownTitle(label string) string {
	label = strings.ToLower(strings.TrimSpace(label))
	if label == "" {
		return ""
	}
	return strings.ToUpper(label[:1]) + label[1:]
}
