package sessions

import (
	"context"
	"fmt"
	"strings"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
)

// Manual compaction ("compact now") lives here so the TUI, the headless CLI,
// and ACP all apply the same logic: plan the compaction, summarize the prompt
// the plan already built, then record it. The in-run agent auto-compaction is a
// separate path (internal/agent).

// SummarizePlan asks the provider to summarize the plan's summary prompt and
// returns the trimmed result. It errors on an empty summary so a caller never
// records a blank compaction. It is provider-shaped like the title call: one
// system + one user turn, no tools.
func SummarizePlan(ctx context.Context, provider kajicoderuntime.Provider, plan CompactionPlan) (string, error) {
	if provider == nil {
		return "", fmt.Errorf("no provider configured")
	}
	if strings.TrimSpace(plan.SummaryPrompt) == "" {
		return "", fmt.Errorf("compaction plan has no summary prompt")
	}
	stream, err := provider.StreamCompletion(ctx, kajicoderuntime.CompletionRequest{
		Messages: []kajicoderuntime.Message{
			{Role: kajicoderuntime.MessageRoleSystem, Content: CompactionSystemPrompt},
			{Role: kajicoderuntime.MessageRoleUser, Content: plan.SummaryPrompt},
		},
	})
	if err != nil {
		return "", fmt.Errorf("summarize compacted session: %w", err)
	}
	collected := kajicoderuntime.CollectStream(ctx, stream)
	if collected.Error != "" {
		return "", fmt.Errorf("summarize compacted session: %s", collected.Error)
	}
	summary := strings.TrimSpace(collected.Text)
	if summary == "" {
		return "", fmt.Errorf("summarize compacted session: empty summary")
	}
	return summary, nil
}

// CompactionSystemPrompt is the instruction paired with the plan's summary
// prompt for manual compaction.
const CompactionSystemPrompt = "Summarize compacted KajiCode session events for future coding context. " +
	"Preserve user goals, decisions, files, tool outcomes, blockers, and exact next steps. " +
	"Omit secrets and do not invent details."

// DeterministicCompactionSummary is the no-provider fallback: a factual one-line
// description of what the compaction replaces. It never requires a model.
func DeterministicCompactionSummary(plan CompactionPlan) string {
	lines := []string{
		fmt.Sprintf("Compacted earlier session context: %d event(s) summarized, %d recent event(s) preserved.", plan.CompactableCount, plan.PreservedCount),
	}
	if len(plan.CompactableEvents) > 0 {
		first := plan.CompactableEvents[0]
		last := plan.CompactableEvents[len(plan.CompactableEvents)-1]
		lines = append(lines, fmt.Sprintf("Compacted range: #%d %s through #%d %s.", first.Sequence, first.Type, last.Sequence, last.Type))
	}
	if len(plan.PreservedEvents) > 0 {
		first := plan.PreservedEvents[0]
		last := plan.PreservedEvents[len(plan.PreservedEvents)-1]
		lines = append(lines, fmt.Sprintf("Preserved recent range: #%d %s through #%d %s.", first.Sequence, first.Type, last.Sequence, last.Type))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// CompactSession applies a manual compaction to a session: it plans with the
// caller's options, summarizes via the provider (falling back to a deterministic
// summary when provider is nil), and records the compaction event — including the
// compacted model-history snapshot (summary + preserved tail) so a resumed
// session feeds the compacted context straight into the agent. A session with
// nothing compactable returns ErrNothingToCompact.
func (store *Store) CompactSession(ctx context.Context, sessionID string, provider kajicoderuntime.Provider, options CompactionOptions) (Event, error) {
	events, err := store.ReadEvents(sessionID)
	if err != nil {
		return Event{}, err
	}
	plan, err := store.PlanCompaction(sessionID, options)
	if err != nil {
		return Event{}, err
	}
	if len(plan.CompactableEvents) == 0 {
		return Event{}, ErrNothingToCompact
	}
	var summary string
	if provider == nil {
		summary = DeterministicCompactionSummary(plan)
	} else {
		summary, err = SummarizePlan(ctx, provider, plan)
		if err != nil {
			return Event{}, err
		}
	}
	payload, err := CompactionPayloadFromPlan(summary, plan)
	if err != nil {
		return Event{}, err
	}
	tailFrom := len(events) - plan.PreservedCount
	if tailFrom < 0 {
		tailFrom = 0
	}
	payload.ModelMessages = CompactedModelMessages(summary, events[tailFrom:])
	return store.AppendEvent(sessionID, AppendEventInput{Type: EventCompaction, Payload: payload})
}

// ErrNothingToCompact marks a compaction request with no events to compact, so a
// caller can report that clearly instead of writing an empty summary.
var ErrNothingToCompact = fmt.Errorf("session has no compactable events")
