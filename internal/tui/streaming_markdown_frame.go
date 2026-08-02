package tui

import "strings"

type streamingMarkdownFrame struct {
	stableLines    []string
	liveLines      []string
	stableBytes    int
	separatorLines int
}

func (frame streamingMarkdownFrame) lines() []string {
	lines := append([]string(nil), frame.stableLines...)
	if len(frame.stableLines) > 0 && len(frame.liveLines) > 0 {
		lines = append(lines, make([]string, frame.separatorLines)...)
	}
	lines = append(lines, frame.liveLines...)
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func renderStreamingAssistantMarkdownFrame(text string, proseMeasure int, tableMeasure int) streamingMarkdownFrame {
	return renderStreamingAssistantMarkdownFrameWith(text, proseMeasure, tableMeasure, renderAssistantMarkdownText)
}

func renderStreamingAssistantMarkdownFrameWith(text string, proseMeasure int, tableMeasure int, renderMarkdown func(string, int, int, bool) []string) streamingMarkdownFrame {
	partition := partitionStreamingMarkdown(text)
	frame := streamingMarkdownFrame{
		stableBytes:    partition.stableBytes,
		separatorLines: partition.separatorLines,
	}
	if partition.stable != "" {
		render := func() string {
			return strings.Join(renderMarkdown(partition.stable, proseMeasure, tableMeasure, true), "\n")
		}
		rendered := ""
		if defaultRenderCache != nil {
			key := streamingMarkdownRenderCacheKey(partition.stable, proseMeasure, tableMeasure)
			rendered = defaultRenderCache.render(key, true, render)
		} else {
			rendered = render()
		}
		frame.stableLines = viewLines(rendered)
	}
	frame.liveLines = renderStreamingMarkdownLiveTail(partition.live, proseMeasure)
	return frame
}

type streamingMarkdownPartition struct {
	stable         string
	live           string
	stableBytes    int
	separatorLines int
}

func partitionStreamingMarkdown(text string) streamingMarkdownPartition {
	text = normalizeStreamingMarkdown(text)
	displayText := streamingMarkdownStablePrefix(text)
	boundary := streamingMarkdownBoundary(displayText)
	if boundary.end <= 0 {
		return streamingMarkdownPartition{live: strings.TrimRight(displayText, "\n")}
	}
	live := strings.TrimLeft(displayText[boundary.end:], "\n")
	separatorLines := boundary.separatorLines
	if strings.TrimSpace(live) == "" {
		separatorLines = 0
	}
	return streamingMarkdownPartition{
		stable:         strings.TrimRight(displayText[:boundary.end], "\n"),
		live:           live,
		stableBytes:    boundary.end,
		separatorLines: separatorLines,
	}
}

type streamingMarkdownBoundaryResult struct {
	end            int
	separatorLines int
}

func streamingMarkdownBoundary(text string) streamingMarkdownBoundaryResult {
	openFence := ""
	last := streamingMarkdownBoundaryResult{}
	for lineStart := 0; lineStart < len(text); {
		relativeEnd := strings.IndexByte(text[lineStart:], '\n')
		hasNewline := relativeEnd >= 0
		lineEnd := len(text)
		nextStart := len(text)
		if hasNewline {
			lineEnd = lineStart + relativeEnd
			nextStart = lineEnd + 1
		}
		line := text[lineStart:lineEnd]
		trimmed := strings.TrimSpace(line)
		if fence, ok := markdownFenceMarker(trimmed); ok {
			if openFence == "" {
				openFence = fence
			} else if fence == openFence {
				openFence = ""
				last = streamingMarkdownBoundaryResult{end: nextStart}
			}
			lineStart = nextStart
			continue
		}
		if openFence != "" {
			lineStart = nextStart
			continue
		}
		switch {
		case trimmed == "":
			separatorLines := 1
			if last.end == lineStart {
				separatorLines = last.separatorLines + 1
			}
			last = streamingMarkdownBoundaryResult{end: nextStart, separatorLines: separatorLines}
		case hasNewline && streamingMarkdownLineIsStable(line, trimmed):
			last = streamingMarkdownBoundaryResult{end: nextStart}
		}
		lineStart = nextStart
	}
	return last
}

func streamingMarkdownLineIsStable(line string, trimmed string) bool {
	if _, heading := markdownHeading(line); heading != "" {
		return true
	}
	return isMarkdownHorizontalRule(trimmed)
}

func renderStreamingMarkdownLiveTail(text string, proseMeasure int) []string {
	text = normalizeStreamingMarkdown(text)
	text = streamingMarkdownStablePrefix(text)
	text = strings.TrimRight(strings.TrimLeft(text, "\r\n"), "\r\n")
	if strings.TrimSpace(text) == "" {
		return nil
	}
	window := boundedStreamingWindowBytes([]byte(text), streamPreviewSuffixBytes)
	lines := strings.Split(strings.TrimRight(window.text, "\r\n"), "\n")
	if len(lines) > 8 {
		lines = lines[len(lines)-8:]
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = renderStreamingMarkdownInlinePreview(strings.TrimRight(line, "\r"))
		out = append(out, wrapANSITextWithPrefixes("", "", line, proseMeasure)...)
	}
	return out
}

func normalizeStreamingMarkdown(text string) string {
	return strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
}
