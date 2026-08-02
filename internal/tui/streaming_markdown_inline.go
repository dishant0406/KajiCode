package tui

import "strings"

func renderStreamingMarkdownInlinePreview(text string) string {
	if !strings.ContainsAny(text, "`*_[!<~=+") {
		return text
	}
	var out strings.Builder
	for index := 0; index < len(text); {
		switch {
		case text[index] == '\\' && index+1 < len(text):
			out.WriteString(text[index : index+2])
			index += 2
			continue
		case text[index] == '`':
			if close := findUnescapedByte(text, index+1, '`'); close > index {
				writeStreamingMarkdownSpan(&out, text[index:close+1], markdownCodeStart, markdownCodeEnd)
				index = close + 1
				continue
			}
		case strings.HasPrefix(text[index:], "**") && canOpenMarkdownDelimiter(text, index, "**"):
			if close := streamingMarkdownClosingDelimiter(text, index+2, "**"); close > index {
				writeStreamingMarkdownSpan(&out, text[index:close+2], markdownBoldStart, markdownBoldEnd)
				index = close + 2
				continue
			}
		case strings.HasPrefix(text[index:], "__") && canOpenMarkdownDelimiter(text, index, "__"):
			if close := streamingMarkdownClosingDelimiter(text, index+2, "__"); close > index {
				writeStreamingMarkdownSpan(&out, text[index:close+2], markdownBoldStart, markdownBoldEnd)
				index = close + 2
				continue
			}
		case strings.HasPrefix(text[index:], "~~"):
			if close := streamingMarkdownClosingSequence(text, index+2, "~~"); close > index {
				writeStreamingMarkdownSpan(&out, text[index:close+2], markdownStrikeStart, markdownStrikeEnd)
				index = close + 2
				continue
			}
		case strings.HasPrefix(text[index:], "=="):
			if close := streamingMarkdownClosingSequence(text, index+2, "=="); close > index {
				writeStreamingMarkdownSpan(&out, text[index:close+2], markdownAmberStart, markdownAmberEnd)
				index = close + 2
				continue
			}
		case strings.HasPrefix(text[index:], "++"):
			if close := streamingMarkdownClosingSequence(text, index+2, "++"); close > index {
				writeStreamingMarkdownSpan(&out, text[index:close+2], markdownGreenStart, markdownGreenEnd)
				index = close + 2
				continue
			}
		case text[index] == '!' && index+1 < len(text) && text[index+1] == '[':
			if _, end, ok := markdownLinkLabel(text, index+1); ok {
				writeStreamingMarkdownSpan(&out, text[index:end], markdownItalicStart, markdownItalicEnd)
				index = end
				continue
			}
		case text[index] == '[':
			if _, end, ok := markdownLinkLabel(text, index); ok {
				writeStreamingMarkdownSpan(&out, text[index:end], markdownLinkStart, markdownLinkEnd)
				index = end
				continue
			}
		case text[index] == '<':
			if _, end, ok := markdownAutolinkLabel(text, index); ok {
				writeStreamingMarkdownSpan(&out, text[index:end], markdownLinkStart, markdownLinkEnd)
				index = end
				continue
			}
		case text[index] == '*' && !strings.HasPrefix(text[index:], "**") && canOpenMarkdownDelimiter(text, index, "*"):
			if close := streamingMarkdownClosingDelimiter(text, index+1, "*"); close > index {
				writeStreamingMarkdownSpan(&out, text[index:close+1], markdownItalicStart, markdownItalicEnd)
				index = close + 1
				continue
			}
		}
		out.WriteByte(text[index])
		index++
	}
	return out.String()
}

func writeStreamingMarkdownSpan(out *strings.Builder, text string, start string, end string) {
	out.WriteString(start)
	out.WriteString(text)
	out.WriteString(end)
}

func streamingMarkdownClosingDelimiter(text string, start int, marker string) int {
	for index := start; index < len(text); index++ {
		if text[index] == '\\' {
			index++
			continue
		}
		if strings.HasPrefix(text[index:], marker) && canCloseMarkdownDelimiter(text, index, marker) {
			return index
		}
	}
	return -1
}

func streamingMarkdownClosingSequence(text string, start int, marker string) int {
	for index := start; index < len(text); index++ {
		if text[index] == '\\' {
			index++
			continue
		}
		if strings.HasPrefix(text[index:], marker) {
			return index
		}
	}
	return -1
}
