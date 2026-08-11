package agent

import (
	"strings"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
)

// Turn/budget-aware tail selection.
//
// opencode-style compaction keeps a *recent window of complete user turns*
// verbatim (assistant/tool messages always travel with the user turn that
// started them) and budgets that tail to a fraction of usable context, rather
// than blindly keeping the last N messages. This protects the most relevant
// working context while still reclaiming the bloated head. It also *splits* an
// over-budget turn so the newest part of a single huge turn stays verbatim
// instead of being dumped wholesale into the summary.
//
// These helpers are pure: they never call a provider. The summarizer (which
// consumes the selected head) is injected by the caller, exactly as in the rest
// of the compaction pipeline.

// tailTurn is a contiguous group of messages that form one user turn: it begins
// at a user message and runs until (but not including) the next user message.
// Assistant and tool messages always belong to the turn that started them.
type tailTurn struct {
	// start is the index of the turn's first (user) message.
	start int
	// end is the exclusive index one past the turn's last message.
	end int
}

// splitTurns returns the complete user turns beginning at or after startIndex,
// ending at len(messages). A user message whose content is the injected summary
// marker is skipped so a prior compaction's summary does not begin a new turn.
func splitTurns(messages []kajicoderuntime.Message, startIndex int) []tailTurn {
	var turns []tailTurn
	lastStart := -1
	for i := startIndex; i < len(messages); i++ {
		if messages[i].Role != kajicoderuntime.MessageRoleUser {
			continue // assistant/tool follow their user
		}
		if isSummaryMarkerMessage(messages[i]) {
			continue
		}
		if lastStart >= 0 {
			turns[len(turns)-1].end = i
		}
		turns = append(turns, tailTurn{start: i, end: len(messages)})
		lastStart = i
	}
	return turns
}

// isSummaryMarkerMessage reports whether a user message is an injected
// compaction summary (the marker that a prior Compact injected).
func isSummaryMarkerMessage(message kajicoderuntime.Message) bool {
	return strings.HasPrefix(message.Content, summaryLabel)
}

// turnTokens estimates the model-token cost of a whole turn (user + assistant +
// tool messages) using the same dependency-free estimator as the rest of the
// compaction pipeline, so tail budgeting is consistent with the trigger check.
func (turn tailTurn) turnTokens(messages []kajicoderuntime.Message) int {
	return estimateTokens(messages[turn.start:turn.end])
}

// planTail selects the newest tailTurns complete user turns to keep verbatim,
// budgeting them to budget estimated tokens, and returns the boundary index at
// which the kept suffix begins. The mandatory newest user turn (the active
// prompt and its turn) is ALWAYS retained in full, even when it alone exceeds
// budget — a run must keep the ask it is answering. Older turns are retained
// newest-first while they fit the remainder of the budget; a single over-budget
// turn is split so its newest suffix remains verbatim and the rest folds into
// the summary head.
//
// Returns len(messages) when the whole history is kept (nothing to summarize),
// and a boundary at or after headLimit when there is room to summarize a head.
func planTail(messages []kajicoderuntime.Message, headLimit int, tailTurns int, budget int) int {
	if len(messages) == 0 {
		return 0
	}
	if tailTurns <= 0 {
		tailTurns = 1
	}
	// Build turns from headLimit (after the leading system messages) forward.
	turns := splitTurns(messages, headLimit)
	if len(turns) == 0 {
		// No real user turns; nothing beyond the head to pin. Keep everything.
		return len(messages)
	}

	// The newest turn always survives, at minimum its leading user message. If
	// the newest user message alone exceeds budget, still keep the whole turn we
	// are answering (the run's active ask is never dropped).
	newest := turns[len(turns)-1]
	mandatoryTokens := newest.turnTokens(messages)
	remaining := budget
	if remaining < mandatoryTokens {
		remaining = mandatoryTokens
	}

	keptStart := newest.start
	keptTurns := 1
	remaining -= mandatoryTokens

	// Walk newest → oldest, retaining older turns within the budget and the
	// tailTurns window.
	for i := len(turns) - 2; i >= 0; i-- {
		if keptTurns >= tailTurns {
			break
		}
		t := turns[i]
		if t.start < headLimit {
			break
		}
		size := t.turnTokens(messages)
		if size <= remaining {
			keptStart = t.start
			remaining -= size
			keptTurns++
			continue
		}
		// The turn doesn't fit whole; keep its newest suffix that fits.
		if split := splitTurnToBudget(messages, t, remaining); split >= 0 {
			keptStart = split
		}
		break
	}
	return keptStart
}

// splitTurnToBudget returns the newest start index within turn whose kept suffix
// fits within remaining budget, or -1 when even the newest message of the turn
// cannot be kept within budget (in which case the whole turn is summarized).
// It walks newest-first inside the turn, accumulating suffix tokens, and stops
// at the farthest-back index whose suffix still fits. The newest user message of
// the turn is always the anchor and is retained regardless.
func splitTurnToBudget(messages []kajicoderuntime.Message, turn tailTurn, remaining int) int {
	// Walk newest → oldest within the turn, accumulating suffix tokens.
	keepFrom := turn.end
	used := 0
	for i := turn.end - 1; i >= turn.start; i-- {
		msgTokens := estimateTokens(messages[i : i+1])
		// The newest message in the turn is mandatory; older suffixes must fit
		// within remaining, else we stop extending backward.
		if i == turn.end-1 {
			keepFrom = i
			used = msgTokens
			continue
		}
		if used+msgTokens > remaining {
			break
		}
		keepFrom = i
		used += msgTokens
	}
	if keepFrom == turn.end {
		return -1 // turn entirely unrepresentable without overflow at suffix level
	}
	return keepFrom
}

// tailTokenBudget derives a tail token budget (the verbatim recent working
// window) from a context window, matching opencode's clamp of
// max(2000, min(8000, usable*0.25)).
func tailTokenBudget(contextWindow int) int {
	if contextWindow <= 0 {
		return 0
	}
	budget := int(float64(contextWindow) * 0.25)
	if budget < 2000 {
		budget = 2000
	}
	if budget > 8000 {
		budget = 8000
	}
	return budget
}

// defaultBudgetedTailMinWindow is the smallest context window at which the
// compactor turns on the budgeted tail path by default. Real coding models are
// >= 128k; harness and unit tests exercise tiny windows (<= ~10k) and rely on
// the message-count PreserveLast tail, so they keep the legacy path below this
// floor. The budgeted path is always available on demand by setting TailTurns.
const defaultBudgetedTailMinWindow = 32_000

// defaultCompactionTailTurns is how many complete recent user turns the
// budgeted tail keeps verbatim (beyond the mandatory newest turn) before
// older turns fold into the summary.
const defaultCompactionTailTurns = 2
