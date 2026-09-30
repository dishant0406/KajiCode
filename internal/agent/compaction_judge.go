package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/dishant0406/KajiCode/internal/classifier"
	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/redaction"
)

// The compaction judge is the relevance-aware stage that filters the stale
// history before the paid LLM summarizer runs. The free positional prune
// (prune.go) drops old tool bodies by age and size alone; the judge asks a fast
// classifier whether each stale item is still needed, keeps the ones that are,
// and removes the rest:
//
//   - a stale tool body is reduced to the deterministic pruned placeholder,
//   - a stale tool call's arguments are reduced to "{}",
//   - stale assistant narration is cleared.
//
// The free prune is the deterministic floor: every stale body is reduced to its
// placeholder first (duplicates included), then the judge restores the bodies it
// judged relevant and removes the arguments/narration it judged irrelevant. So
// the judge can only ever keep MORE than the blind prune, never less.
//
// Note a fallback gap: the judge only covers what it is asked about (bounded by
// judgeMaxWork) and only when it answers. A body dropped by the free prune floor
// — a duplicate collapse, a body past the cap, or any body when the classifier
// answers nothing — keeps the plain re-run hint and is NOT spilled.
//
// A drop is a two-threshold decision (see judgeOptions): only a confident-no is
// dropped, and the uncertain band is kept. The production drop-recovery hook
// spills a dropped body and names the file in its placeholder, so a drop is
// recoverable rather than lost.
//
// It never drops a message — it only rewrites a body, an Arguments string, or an
// assistant's text while keeping the message, its ToolCallID, and its ToolCalls —
// so provider replay stays valid. It is fail-open: when the classifier is
// unavailable the caller falls back to the free prune unchanged.
const (
	// judgeStateByteBudget bounds the classifier state for ONE request. Items are
	// packed greedily until this is reached, then a new request carries the next
	// batch. ~30 KB packs ~15 items, sized so a batch completes within the request
	// timeout even on a slow classifier.
	judgeStateByteBudget = 30_000
	// judgeMaxWork bounds how many items one compaction asks about, newest-first,
	// so classification latency stays bounded on a very large history. Items past
	// it fall to the deterministic prune floor (safe: the oldest bodies are the
	// least likely to still be relevant).
	judgeMaxWork = 1000
	// judgeMinArgTokens / judgeMinProseTokens skip small items: rewriting them
	// saves too little to justify classifying them, or the risk of losing a key a
	// preserved-state reader parses.
	judgeMinArgTokens   = 200
	judgeMinProseTokens = 200
	// judgeGoalBytes / judgeInputBytes / judgeResultBytes bound each part of the
	// judge state. The full body is kept when a result is judged relevant; only
	// what is sent to the classifier is truncated.
	judgeGoalBytes   = 2000
	judgeInputBytes  = 600
	judgeResultBytes = 1500
)

// judgeEmptyArguments is what a stale tool call's arguments are reduced to. It is
// the normalizer's proven-safe fallback (historySafeToolCalls), so it always
// parses as JSON for every provider.
const judgeEmptyArguments = "{}"

// Candidate kinds. The kind decides both what the classifier is asked about and
// what replaces the item when it is judged irrelevant.
const (
	judgeKindToolResult = "tool_result"
	judgeKindToolArgs   = "tool_args"
	judgeKindAssistant  = "assistant"
)

// judgeCandidate is one stale item offered to the classifier.
type judgeCandidate struct {
	index     int // message index
	callIndex int // index into the message's ToolCalls, or -1
	id        string
	kind      string
	tool      string
	argument  string
	body      string
	tokens    int
}

// judgeOptions carries the judge's decision band and its drop-recovery hook.
// A struct (rather than positional floats) keeps the two thresholds, which are
// both probabilities, from being swapped at a call site.
type judgeOptions struct {
	// dropThreshold is the probability below which an item is dropped. An item
	// at or above it is KEPT: the uncertain band between dropThreshold and
	// keepThreshold keeps, so only a confident-no is dropped. <= 0 (or above
	// keepThreshold) makes the band EMPTY, i.e. the plain keep threshold.
	dropThreshold float64
	// keepThreshold is the probability at or above which an item is
	// confidently kept. It bounds the decision (only the band floor
	// dropThreshold gates a drop) and, when no dropThreshold is set, IS the
	// decision. <= 0 uses classifier.DefaultKeepResultThreshold. The
	// durable-artifact ranking is independent of it.
	keepThreshold float64
	// recover, when non-nil, returns the placeholder that replaces a dropped
	// body, so a drop can be made recoverable (the production rewriter spills
	// the body and names the file). nil is today's re-run hint.
	recover prunedBodyRewriter
}

// judgeStaleHistory filters the stale history with a fast classifier and reports
// whether it ran. It keeps the bodies the classifier judges relevant, replaces
// the rest with the deterministic pruned placeholder, reduces irrelevant
// tool-call arguments to "{}", and clears irrelevant assistant narration.
//
// It also reports the most relevant stale results (path targets first, then by
// classifier probability, then newest) as durable artifacts, so the caller can
// carry their targets into the injected summary — the only channel through which
// a kept body reaches the model, since the summarizer compresses its input away
// (see compaction_artifacts.go).
//
// floor is the deterministic free prune of the same messages and preserveLast;
// the judge restores relevant bodies over it and removes irrelevant
// arguments/narration. The caller computes it once, so the prune is not run
// twice over a large history.
//
// ok is false when the classifier is nil, errors, or answers nothing — the
// caller then falls back to the free positional prune (byte-identical to the
// judge-off path). The input slice is never mutated.
func judgeStaleHistory(
	ctx context.Context,
	cl classifier.Classifier,
	goal string,
	messages []kajicoderuntime.Message,
	floor []kajicoderuntime.Message,
	preserveLast int,
	opts judgeOptions,
) ([]kajicoderuntime.Message, []durableArtifact, bool) {
	if cl == nil || len(messages) == 0 {
		return messages, nil, false
	}
	if opts.keepThreshold <= 0 {
		opts.keepThreshold = classifier.DefaultKeepResultThreshold
	}
	// No recovery hook means the plain re-run hint. No drop threshold (or one
	// above keep) means an EMPTY band, i.e. the shipped single-threshold DECISION.
	// (The placeholder TEXT still depends on the hook: production installs a
	// spill-backed rewriter, so a dropped body names its recovery file. The
	// decision is what is byte-identical, not the placeholder string.)
	if opts.recover == nil {
		opts.recover = defaultPrunedBodyRewriter
	}
	if opts.dropThreshold <= 0 || opts.dropThreshold > opts.keepThreshold {
		opts.dropThreshold = opts.keepThreshold
	}
	candidates := judgeCandidates(messages, preserveLast)
	if len(candidates) == 0 {
		return messages, nil, false
	}

	answered := judgeAnswers(ctx, cl, goal, candidates)
	if len(answered) == 0 {
		return messages, nil, false
	}

	// Deterministic floor: the free prune drops every stale body (duplicates
	// included). The judge then restores what it judged relevant and removes the
	// arguments/narration it judged irrelevant.
	base := floor
	if len(base) != len(messages) {
		base, _ = pruneStaleToolOutput(messages, preserveLast)
	}

	contentEdits := map[int]string{}
	argEdits := map[int]map[int]string{}
	// ranked collects every judged tool result with the classifier's
	// probability, so the caller can carry the MOST relevant targets into the
	// summary. The ranking is the whole point: the block is capped, so it must
	// name the still-relevant reads first and drop the stale bash dumps. It is
	// independent of the keep/drop decision below, which governs only whether
	// the body stays verbatim in the history.
	type rankedArtifact struct {
		probability float64
		artifact    durableArtifact
	}
	var ranked []rankedArtifact
	seenTargets := map[string]bool{}
	for _, candidate := range candidates {
		answer, ok := answered[candidate.id]
		if !ok || answer.Type != classifier.KindNoul {
			// No answer for this item: keep the prune floor's decision.
			continue
		}
		switch candidate.kind {
		case judgeKindToolResult:
			if artifact, ok := newDurableArtifact(candidate); ok && !seenTargets[artifact.target] {
				seenTargets[artifact.target] = true
				ranked = append(ranked, rankedArtifact{probability: answer.Probability, artifact: artifact})
			}
			replace := candidate.body
			if answer.Probability < opts.dropThreshold {
				// Only a dropped body goes through the recovery hook, so a kept
				// body is never spilled.
				replace = opts.recover(candidate.tool, candidate.tokens, candidate.body)
			}
			if replace != base[candidate.index].Content {
				contentEdits[candidate.index] = replace
			}
		case judgeKindToolArgs:
			if answer.Probability >= opts.dropThreshold {
				continue
			}
			edits := argEdits[candidate.index]
			if edits == nil {
				edits = map[int]string{}
				argEdits[candidate.index] = edits
			}
			edits[candidate.callIndex] = judgeEmptyArguments
		case judgeKindAssistant:
			if answer.Probability >= opts.dropThreshold || base[candidate.index].Content == "" {
				continue
			}
			contentEdits[candidate.index] = ""
		}
	}
	// Carry the most relevant targets. A file path leads its probability band:
	// the model can re-read a file, and a stale read_file/grep result is exactly
	// the artifact whose target it is most likely to need next. On ties
	// (including equal probabilities) the newest candidate leads, because the
	// candidate list is collected newest-first.
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].artifact.path != ranked[j].artifact.path {
			return ranked[i].artifact.path
		}
		return ranked[i].probability > ranked[j].probability
	})
	artifacts := make([]durableArtifact, 0, min(len(ranked), maxDurableArtifacts))
	for _, entry := range ranked {
		if len(artifacts) >= maxDurableArtifacts {
			break
		}
		artifacts = append(artifacts, entry.artifact)
	}

	if len(contentEdits) == 0 && len(argEdits) == 0 {
		return base, artifacts, true
	}

	out := make([]kajicoderuntime.Message, len(base))
	copy(out, base)
	for index, content := range contentEdits {
		out[index].Content = content
	}
	for index, edits := range argEdits {
		out[index].ToolCalls = rewriteToolCallArguments(out[index].ToolCalls, edits)
	}
	return out, artifacts, true
}

// judgeAnswers sends the candidates in size-bounded batches and unions the
// answers. A batch that errors contributes nothing (fail-open per batch), so a
// partial outage still applies the batch that succeeded.
func judgeAnswers(ctx context.Context, cl classifier.Classifier, goal string, candidates []judgeCandidate) map[string]classifier.Answer {
	answered := map[string]classifier.Answer{}
	batch := make([]judgeCandidate, 0, len(candidates))
	bytes := 0
	flush := func() {
		if len(batch) == 0 {
			return
		}
		result, err := cl.Classify(ctx, classifier.Request{
			State:     judgeState(goal, batch),
			Questions: judgeQuestions(batch),
		})
		if err == nil {
			for id, answer := range result {
				answered[id] = answer
			}
		}
		batch = batch[:0]
		bytes = 0
	}
	work := candidates
	if len(work) > judgeMaxWork {
		work = work[:judgeMaxWork]
	}
	for _, candidate := range work {
		candidateBytes := judgeCandidateBytes(candidate)
		if len(batch) > 0 && bytes+candidateBytes > judgeStateByteBudget {
			flush()
		}
		batch = append(batch, candidate)
		bytes += candidateBytes
	}
	flush()
	return answered
}

// judgeCandidateBytes approximates the state cost of one candidate after the
// scrub/truncate step (mirroring judgeState), used only to pack batches under
// judgeStateByteBudget.
func judgeCandidateBytes(candidate judgeCandidate) int {
	total := len(candidate.id) + len(candidate.kind) + len(candidate.tool) + 64
	if candidate.kind == judgeKindToolArgs {
		total += min(len(candidate.argument), judgeResultBytes)
	} else {
		total += min(len(candidate.argument), judgeInputBytes)
		total += min(len(candidate.body), judgeResultBytes)
	}
	return total
}

// rewriteToolCallArguments returns a copy of calls with the named indices'
// arguments replaced. Message identity and every other field survive.
func rewriteToolCallArguments(calls []ToolCall, edits map[int]string) []ToolCall {
	out := make([]ToolCall, len(calls))
	copy(out, calls)
	for index, arguments := range edits {
		if index < 0 || index >= len(out) {
			continue
		}
		out[index].Arguments = arguments
	}
	return out
}

// judgeCandidates collects the stale items a judge may rewrite: tool bodies,
// tool-call arguments, and assistant narration that all sit outside the
// preserved recent window and the trailing tool-output protection window. It
// mirrors pruneStaleToolOutput's staleness rule exactly, so the judge sees every
// item the free prune would touch (and nothing newer).
func judgeCandidates(messages []kajicoderuntime.Message, preserveLast int) []judgeCandidate {
	if preserveLast < 0 {
		preserveLast = 0
	}
	protectUntil := len(messages) - preserveLast
	toolNameByID := toolNamesByCallID(messages)

	recentToolTokens := 0
	candidates := make([]judgeCandidate, 0)
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		stale := i < protectUntil && recentToolTokens >= pruneProtectRecentTokens
		// Provider reasoning blocks must be replayed verbatim, so a message that
		// carries them is never rewritten.
		if stale && len(msg.Reasoning) == 0 {
			switch msg.Role {
			case kajicoderuntime.MessageRoleTool:
				bodyTokens := ApproxTextTokens(msg.Content)
				if bodyTokens >= pruneMinBodyTokens && !isPrunedPlaceholder(msg.Content) {
					candidates = append(candidates, judgeCandidate{
						index:     i,
						callIndex: -1,
						id:        judgeItemID("result", msg.ToolCallID, i, 0),
						kind:      judgeKindToolResult,
						tool:      toolLabel(toolNameByID[msg.ToolCallID]),
						argument:  judgeToolArgument(messages, msg.ToolCallID),
						body:      msg.Content,
						tokens:    bodyTokens,
					})
				}
			case kajicoderuntime.MessageRoleAssistant:
				if len(msg.ToolCalls) > 0 && ApproxTextTokens(msg.Content) >= judgeMinProseTokens {
					candidates = append(candidates, judgeCandidate{
						index:     i,
						callIndex: -1,
						id:        judgeItemID("prose", "", i, 0),
						kind:      judgeKindAssistant,
						tool:      "assistant",
						body:      msg.Content,
						tokens:    ApproxTextTokens(msg.Content),
					})
				}
				for callIndex, call := range msg.ToolCalls {
					if !judgeableToolCall(call.Name) {
						continue
					}
					tokens := ApproxTextTokens(call.Arguments)
					if tokens < judgeMinArgTokens {
						continue
					}
					candidates = append(candidates, judgeCandidate{
						index:     i,
						callIndex: callIndex,
						id:        judgeItemID("args", call.ID, i, callIndex),
						kind:      judgeKindToolArgs,
						tool:      toolLabel(call.Name),
						argument:  call.Arguments,
						tokens:    tokens,
					})
				}
			}
		}
		if msg.Role == kajicoderuntime.MessageRoleTool {
			recentToolTokens += ApproxTextTokens(msg.Content)
		}
	}
	return candidates
}

// judgeableToolCall reports whether a tool call's arguments may be reduced. A
// call whose arguments a preserved-state reader parses (the active plan, loaded
// skills, deferred tool schemas, or the recent-edit list) must keep them intact,
// or that state silently drops out of the summary.
func judgeableToolCall(name string) bool {
	switch name {
	case toolNameTodoWrite, toolNameToolSearch, toolNameSkill, toolNameWriteFile, toolNameEditFile:
		return false
	}
	return strings.TrimSpace(name) != ""
}

// judgeItemID is a stable, JSON-safe question key. A tool call id may contain
// characters a router dislikes, so it is only used when it is clean.
func judgeItemID(prefix, toolCallID string, index, sub int) string {
	if id := cleanToken(toolCallID); id != "" {
		return prefix + "_" + id
	}
	return fmt.Sprintf("%s_index_%d_%d", prefix, index, sub)
}

func cleanToken(value string) string {
	id := strings.TrimSpace(value)
	if id == "" {
		return ""
	}
	for _, r := range id {
		if !(r == '-' || r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return ""
		}
	}
	return id
}

// judgeToolArgument finds the arguments of the tool call that produced a result,
// so the classifier can weigh the result against what was asked for.
func judgeToolArgument(messages []kajicoderuntime.Message, toolCallID string) string {
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			if call.ID == toolCallID {
				return call.Arguments
			}
		}
	}
	return ""
}

func judgeState(goal string, candidates []judgeCandidate) map[string]any {
	items := make([]map[string]string, 0, len(candidates))
	for _, candidate := range candidates {
		item := map[string]string{
			"id":   candidate.id,
			"kind": candidate.kind,
			"tool": candidate.tool,
		}
		if candidate.kind == judgeKindToolArgs {
			// The arguments ARE the judged content for a tool-call candidate.
			item["input"] = scrubForClassifier(truncateHead(candidate.argument, judgeResultBytes))
		} else {
			item["input"] = scrubForClassifier(truncateHead(candidate.argument, judgeInputBytes))
			item["result"] = scrubForClassifier(truncateHead(candidate.body, judgeResultBytes))
		}
		items = append(items, item)
	}
	return map[string]any{
		"goal":  scrubForClassifier(truncateHead(goal, judgeGoalBytes)),
		"items": items,
	}
}

// scrubForClassifier strips recognizable secrets from the text the judge sends to
// a remote endpoint. The classifier only needs the *shape* of a result to judge
// relevance, never the credential inside it, so every judge-bound string passes
// through the same redaction the CLI's JSON surfaces use.
func scrubForClassifier(value string) string {
	return redaction.RedactString(value, redaction.Options{})
}

func judgeQuestions(candidates []judgeCandidate) map[string]classifier.Question {
	questions := make(map[string]classifier.Question, len(candidates))
	for _, candidate := range candidates {
		var instructions string
		switch candidate.kind {
		case judgeKindToolArgs:
			instructions = "For item " + candidate.id + ": are the arguments of this " + candidate.tool + " call still needed to make progress on the goal, or is the tool name enough to know what was done?"
		case judgeKindAssistant:
			instructions = "For item " + candidate.id + ": is this assistant narration still needed to make progress on the goal, or is it safe to discard? The tool calls it introduced are kept."
		default:
			instructions = "For item " + candidate.id + ": is this " + candidate.tool + " result still needed in full to make progress on the goal, or is it safe to discard and re-run the tool later if needed?"
		}
		questions[candidate.id] = classifier.Noul(
			instructions,
			"still needed",
			"safe to discard, can be re-fetched",
		)
	}
	return questions
}

// compactionGoal returns the most recent user objective, the anchor the judge
// reasons against. The synthetic post-compaction continuation cue is skipped so
// it never becomes the goal.
func compactionGoal(messages []kajicoderuntime.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if message.Role != kajicoderuntime.MessageRoleUser || isCompactionContinuation(message) {
			continue
		}
		if content := strings.TrimSpace(message.Content); content != "" {
			return content
		}
	}
	return ""
}

func truncateHead(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	// Back off to a rune boundary so a truncated body is still valid UTF-8.
	cut := limit
	for cut > 0 && !utf8.ValidString(value[:cut]) {
		cut--
	}
	return value[:cut] + fmt.Sprintf("…[+%d chars]", len(value)-cut)
}
