package tui

import (
	"unicode"
	"unicode/utf8"
)

const (
	streamPreviewTailBytes   = 4096
	streamPreviewSuffixBytes = 2048
)

func appendBoundedStreamingTail(tail string, delta string) string {
	if delta == "" {
		return tail
	}
	return boundedUTF8TailString(tail+delta, streamPreviewTailBytes)
}

func boundedUTF8TailString(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	start := len(value) - limit
	for start < len(value) && !utf8.RuneStart(value[start]) {
		start++
	}
	return value[start:]
}

func boundedUTF8TailBytes(value []byte, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return string(value)
	}
	start := len(value) - limit
	for start < len(value) && !utf8.RuneStart(value[start]) {
		start++
	}
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
