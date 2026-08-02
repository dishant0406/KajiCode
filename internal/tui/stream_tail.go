package tui

import (
	"unicode"
	"unicode/utf8"
)

const (
	streamPreviewTailBytes   = 4096
	streamPreviewSuffixBytes = 2048
)

type boundedStreamWindow struct {
	text  string
	start int
	end   int
}

func appendBoundedStreamingTail(tail string, delta string) string {
	if delta == "" {
		return tail
	}
	return boundedUTF8TailString(tail+delta, streamPreviewTailBytes)
}

func boundedStreamingWindowBytes(value []byte, limit int) boundedStreamWindow {
	end := len(value)
	if limit <= 0 || end <= limit {
		return boundedStreamWindow{text: string(value), start: 0, end: end}
	}
	minStart := nextUTF8Start(value, end-limit)
	start := stableStreamWindowStart(value, minStart)
	return boundedStreamWindow{text: string(value[start:end]), start: start, end: end}
}

func stableStreamWindowStart(value []byte, minStart int) int {
	if minStart <= 0 {
		return 0
	}
	if start := boundaryAfterBlankLine(value, minStart); start >= 0 {
		return skipStreamWindowLeadingSpace(value, start)
	}
	if start := boundaryAfterByte(value, minStart, '\n'); start >= 0 {
		return skipStreamWindowLeadingSpace(value, start)
	}
	for index := minStart; index < len(value); index++ {
		switch value[index] {
		case ' ', '\t':
			return skipStreamWindowLeadingSpace(value, index+1)
		}
	}
	return minStart
}

func boundaryAfterBlankLine(value []byte, start int) int {
	for index := start; index+1 < len(value); index++ {
		if value[index] == '\n' && value[index+1] == '\n' {
			return index + 2
		}
	}
	return -1
}

func boundaryAfterByte(value []byte, start int, marker byte) int {
	for index := start; index < len(value); index++ {
		if value[index] == marker {
			return index + 1
		}
	}
	return -1
}

func skipStreamWindowLeadingSpace(value []byte, start int) int {
	for start < len(value) {
		switch value[start] {
		case ' ', '\t', '\r', '\n':
			start++
			continue
		}
		break
	}
	return nextUTF8Start(value, start)
}

func nextUTF8Start(value []byte, start int) int {
	for start < len(value) && !utf8.RuneStart(value[start]) {
		start++
	}
	return start
}

func boundedUTF8TailString(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	start := nextUTF8Start([]byte(value), len(value)-limit)
	return value[start:]
}

func boundedUTF8TailBytes(value []byte, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return string(value)
	}
	start := nextUTF8Start(value, len(value)-limit)
	return string(value[start:])
}

func containsNonSpace(value string) bool {
	for _, r := range value {
		if !unicode.IsSpace(r) {
			return true
		}
	}
	return false
}
