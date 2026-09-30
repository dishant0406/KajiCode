package agent

import (
	"fmt"
	"strings"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
)

// Stale tool-output pruning reclaims context at zero token/latency cost before
// the loop falls back to the paid LLM summarizer. A long, dump-heavy session
// accumulates large read_file/grep/glob/bash tool results that the model has
// long since acted on; their bodies are dead weight. Pruning replaces those
// older bodies with a compact placeholder (keeping the tool message + its
// ToolCallID so provider replay stays valid and the model knows what was
// there), preserving recent turns exactly — no paraphrase loss.
//
// Two deterministic reductions run in the same pass:
//   - stale bodies: large tool output older than the protection window;
//   - duplicates: an older byte-identical copy of a result that appears again
//     later (a re-run of the same command, a re-read of an unchanged file)
//     carries nothing new, so it is collapsed to a pointer at the newer copy.
//
// Both keep the newest copy of any content. Neither calls a model.
const (
	// pruneProtectRecentTokens is the trailing window of tool output kept
	// verbatim — the model is most likely still using it.
	pruneProtectRecentTokens = 40000
	// pruneMinReclaimTokens gates the whole pass: only prune when the reclaimable
	// (older, large) tool output exceeds this, so a short session is untouched.
	pruneMinReclaimTokens = 20000
	// pruneMinBodyTokens skips small tool results — replacing a tiny body saves
	// nothing and just loses information.
	pruneMinBodyTokens = 200
)

// prunedPlaceholder is the body a stale tool result is replaced with. It names
// the tool and the original size so the model can re-fetch if it needs to.
func prunedPlaceholder(toolName string, originalTokens int) string {
	return fmt.Sprintf("[pruned %s output (~%d tokens) to reclaim context — re-run the tool if you need it again]", toolLabel(toolName), originalTokens)
}

// spillPathMarker introduces the recovery path in a pruned placeholder. It is a
// constant so the writer (prunedPlaceholderWithPath) and the reader
// (recoveryPathOf) cannot drift.
const spillPathMarker = " full output saved to "

// prunedPlaceholderWithPath is prunedPlaceholder for a body that was spilled to a
// readable file first. It names the file so "dropped" is not "lost": the model
// reads or greps the spill instead of re-running a tool that may be expensive or
// non-idempotent. It keeps the "[pruned " prefix so an already-pruned body is
// still recognized (and never re-pruned) on a later pass.
func prunedPlaceholderWithPath(toolName string, originalTokens int, path string) string {
	return fmt.Sprintf("[pruned %s output (~%d tokens) to reclaim context —%s%s; read_file or grep it, or re-run the tool if you need it again]", toolLabel(toolName), originalTokens, spillPathMarker, path)
}

// recoveryPathOf returns the spill path a pruned placeholder names, or "" when
// the placeholder carries no recovery path. It is the inverse of
// prunedPlaceholderWithPath and reads only what that function wrote, so it
// requires the same ';' terminator: a malformed or truncated placeholder yields
// "" rather than a garbage "path".
func recoveryPathOf(placeholder string) string {
	idx := strings.Index(placeholder, spillPathMarker)
	if idx < 0 {
		return ""
	}
	rest := placeholder[idx+len(spillPathMarker):]
	end := strings.IndexByte(rest, ';')
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}

// prunedBodyRewriter builds the placeholder that replaces a stale tool-result
// body when it is dropped. It is the seam that makes a drop recoverable without
// touching the pure prune: the default rewriter returns today's re-run hint, and
// a spill-backed rewriter stores the body and returns a placeholder naming the
// file. It is only ever called on the drop path, so nothing is spilled for a
// body the judge keeps.
type prunedBodyRewriter func(tool string, tokens int, body string) string

// defaultPrunedBodyRewriter is the no-op recovery net: it returns the plain
// re-run hint, so a drop with no configured recovery writes nothing to disk.
func defaultPrunedBodyRewriter(tool string, tokens int, _ string) string {
	return prunedPlaceholder(tool, tokens)
}

// duplicatePlaceholder is the body an older duplicate tool result is replaced
// with. It points at the kept (newer) copy still in context.
func duplicatePlaceholder(toolName string, originalTokens int) string {
	return fmt.Sprintf("[pruned duplicate %s output (~%d tokens) — identical to a later result still in context]", toolLabel(toolName), originalTokens)
}

func toolLabel(toolName string) string {
	if toolName == "" {
		return "tool"
	}
	return toolName
}

// pruneStaleToolOutput walks messages newest-first, protecting the last
// preserveLast messages and a trailing pruneProtectRecentTokens of tool output,
// then replaces the bodies of older large tool results with a placeholder. It
// also collapses older byte-identical duplicates of a later tool result.
// It returns the (possibly) rewritten slice and the number of tokens reclaimed.
// When nothing meets the bar it returns the input unchanged with reclaimed=0.
//
// It never drops a message, never touches non-tool messages, and never
// re-processes an already-pruned body — so it is idempotent and safe to run
// every turn.
func pruneStaleToolOutput(messages []kajicoderuntime.Message, preserveLast int) ([]kajicoderuntime.Message, int) {
	if len(messages) == 0 {
		return messages, 0
	}
	if preserveLast < 0 {
		preserveLast = 0
	}
	protectUntil := len(messages) - preserveLast // indices >= this are protected
	toolNameByID := toolNamesByCallID(messages)

	// replacements maps a message index to the compact body that replaces its
	// current body. A body is only recorded when it actually saves tokens.
	replacements := map[int]string{}
	record := func(index int, body string) {
		if ApproxTextTokens(body) >= ApproxTextTokens(messages[index].Content) {
			return
		}
		replacements[index] = body
	}

	// Pass 1: duplicate results. Scan newest-first so the first occurrence of any
	// (tool, body) pair is kept; every older byte-identical copy is collapsed.
	// Duplicates inside the protected recent window are left alone so that window
	// stays exactly as the model produced it.
	seen := map[string]bool{}
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role != kajicoderuntime.MessageRoleTool || isPrunedPlaceholder(msg.Content) {
			continue
		}
		key := toolNameByID[msg.ToolCallID] + "\x00" + msg.Content
		if !seen[key] {
			seen[key] = true
			continue
		}
		if i >= protectUntil || ApproxTextTokens(msg.Content) < pruneMinBodyTokens {
			continue
		}
		record(i, duplicatePlaceholder(toolNameByID[msg.ToolCallID], ApproxTextTokens(msg.Content)))
	}

	// Pass 2: stale bodies older than the recent-output protection window.
	recentToolTokens := 0
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role != kajicoderuntime.MessageRoleTool {
			continue
		}
		if _, done := replacements[i]; done {
			continue
		}
		bodyTokens := ApproxTextTokens(msg.Content)
		if i >= protectUntil {
			recentToolTokens += bodyTokens
			continue
		}
		// Still within the trailing-output protection window?
		if recentToolTokens < pruneProtectRecentTokens {
			recentToolTokens += bodyTokens
			continue
		}
		if bodyTokens < pruneMinBodyTokens || isPrunedPlaceholder(msg.Content) {
			continue
		}
		record(i, prunedPlaceholder(toolNameByID[msg.ToolCallID], bodyTokens))
	}

	reclaimable := 0
	for index, body := range replacements {
		reclaimable += ApproxTextTokens(messages[index].Content) - ApproxTextTokens(body)
	}
	if reclaimable < pruneMinReclaimTokens || len(replacements) == 0 {
		return messages, 0
	}

	// Copy-on-write so we never mutate the caller's slice in place.
	out := make([]kajicoderuntime.Message, len(messages))
	copy(out, messages)
	for index, body := range replacements {
		out[index].Content = body
	}
	return out, reclaimable
}

// toolNamesByCallID maps each tool-result ToolCallID to the name of the tool
// that produced it, so a result's body can be labelled without rescanning the
// transcript for every candidate.
func toolNamesByCallID(messages []kajicoderuntime.Message) map[string]string {
	names := map[string]string{}
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			if call.ID != "" && call.Name != "" {
				names[call.ID] = call.Name
			}
		}
	}
	return names
}

func isPrunedPlaceholder(content string) bool {
	return strings.HasPrefix(strings.TrimSpace(content), "[pruned ")
}

// Pruned-body spill bounds. The spill runs synchronously on the turn path, so a
// single compaction must not write an unbounded number or size of files before
// the provider request.
const (
	// maxPrunedSpillsPerCompaction caps how many dropped bodies one compaction
	// spills. Beyond it the drop keeps today's re-run hint, so the worst case
	// (a pass dropping up to judgeMaxWork bodies) stays bounded.
	maxPrunedSpillsPerCompaction = 64
	// maxPrunedSpillBytes skips spilling an individual body larger than this
	// (2 MiB). A body that large is rare and re-runnable; the cap keeps one
	// drop from writing a large file on the turn path.
	maxPrunedSpillBytes = 2 << 20
)
