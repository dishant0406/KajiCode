package sessions

import "strings"

// Compaction summaries are model output that is persisted into the session store
// and later re-injected as context. A misbehaving summarizer can return a
// transcript echo (observed at 272 KB, with 5,391 identical lines) or a lazy
// one-liner ("Now the registry:") instead of a summary. These checks reject that
// output structurally, before it is recorded or shown, so neither the manual
// "compact now" path (this package) nor the in-run agent path persists garbage.
//
// They are template-agnostic on purpose: the two paths ask for different shapes
// (the agent uses a strict Markdown outline, the manual path asks for a free-form
// events digest), so only the properties common to any real summary live here.
// A caller may layer its own format checks on top.

const (
	// MaxCompactionSummaryBytes bounds a summary. A real summary runs to a few
	// kilobytes (the observed worst legitimate one was 15.3 KB); the degenerate
	// transcript echoes ran to 272 KB.
	MaxCompactionSummaryBytes = 32000

	// MaxCompactionSummaryRepeatRun is the longest run of byte-identical
	// consecutive lines a summary MAY contain; a run reaching this length is
	// rejected, so the effective maximum is one less.
	MaxCompactionSummaryRepeatRun = 20
)

// ValidCompactionSummaryShape reports whether summary has the structure of a
// real compaction summary: non-empty, within MaxCompactionSummaryBytes, and free
// of long runs of identical lines.
func ValidCompactionSummaryShape(summary string) bool {
	trimmed := strings.TrimSpace(summary)
	if trimmed == "" {
		return false
	}
	if len(trimmed) > MaxCompactionSummaryBytes {
		return false
	}
	return LongestIdenticalLineRun(summary) < MaxCompactionSummaryRepeatRun
}

// LongestIdenticalLineRun returns the length of the longest run of consecutive
// byte-identical non-blank lines, ignoring surrounding whitespace. Blank lines
// are skipped WITHOUT breaking a run, so an echo that interleaves blank lines
// between repeated lines is still detected.
func LongestIdenticalLineRun(text string) int {
	longest, current := 0, 0
	previous := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == previous {
			current++
		} else {
			current = 1
		}
		if current > longest {
			longest = current
		}
		previous = line
	}
	return longest
}
