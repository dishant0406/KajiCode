// Package trace records per-turn timing for a Zero agent run.
//
// Tracing is opt-in. A *Recorder is attached to agent.Options and threaded
// through the run via context (see FromContext / WithContext). When the
// recorder is nil, every stamp is a no-op and the agent loop, providers, and
// tools are byte-identical to an untraced run.
//
// The emitted NDJSON is compatible with internal/agenteval's trace contract:
// one JSON object per line carrying a "type" (and usually "name") field, so
// agenteval.ParseTraceEventKeys / MissingTraceEvents can validate a run.
package trace

import "time"

// Span names. These are the "name" keys emitted in the NDJSON event stream
// (e.g. {"type":"span","name":"generation","duration_ms":123.4}) and the
// single source of truth for what a run's wall time is attributed to.
const (
	SpanPromptBuild     = "prompt_build"
	SpanProviderConnect = "provider_connect"
	SpanProviderQueue   = "provider_queue"
	SpanGeneration      = "generation"
	SpanToolQueue       = "tool_queue"
	SpanToolExecution   = "tool_execution"
	SpanPermissionWait  = "permission_wait"
	SpanProcessWait     = "process_wait"
	SpanVerification    = "verification"
	SpanCompaction      = "compaction"
	SpanPersistence     = "persistence"
)

// Counter names. Emitted as {"type":"counter","name":"tool_calls","value":7}.
const (
	CounterModelRequests     = "model_requests"
	CounterToolCalls         = "tool_calls"
	CounterRetryCount        = "retry_count"
	CounterReconnectCount    = "reconnect_count"
	CounterCompactionCount   = "compaction_count"
	CounterCompletionNudges  = "completion_nudges"
	CounterAcceptanceChecks  = "acceptance_checks"
	CounterPollingTurn       = "polling_turn"
	CounterModelSwitches     = "model_switches"
	CounterInputTokens       = "input_tokens"
	CounterCachedInputTokens = "cached_input_tokens"
	CounterOutputTokens      = "output_tokens"
)

// Span is a named duration attributed to part of a run. Spans accumulate by
// name: stamping the same name twice (e.g. prompt_build across turns) adds to
// the existing entry rather than creating a duplicate.
type Span struct {
	Name     string        `json:"name"`
	Duration time.Duration `json:"duration"`
}

// Counter is a named integer accumulated during a run (counts and token totals).
type Counter struct {
	Name  string `json:"name"`
	Value int64  `json:"value"`
}

// TurnTrace is the finished record for one agent.Run. It is the value
// emitters serialize; it is not mutated after Finish returns a snapshot.
type TurnTrace struct {
	SessionID           string    `json:"session_id"`
	RunID               string    `json:"run_id"`
	Profile             string    `json:"profile,omitempty"`
	StartedAt           time.Time `json:"started_at"`
	FirstVisibleEventAt time.Time `json:"first_visible_event_at,omitempty"`
	FirstUsefulActionAt time.Time `json:"first_useful_action_at,omitempty"`
	FirstTokenAt        time.Time `json:"first_token_at,omitempty"`
	CompletedAt         time.Time `json:"completed_at"`
	Spans               []Span    `json:"spans"`
	Counters            []Counter `json:"counters"`
}

// WallDuration is the total traced wall time of the run.
func (t *TurnTrace) WallDuration() time.Duration {
	if t == nil || t.CompletedAt.IsZero() || t.StartedAt.IsZero() {
		return 0
	}
	return t.CompletedAt.Sub(t.StartedAt)
}

// AttributedDuration is the sum of all span durations. A run is considered
// well-attributed when this is >= 95% of WallDuration.
func (t *TurnTrace) AttributedDuration() time.Duration {
	if t == nil {
		return 0
	}
	var total time.Duration
	for _, span := range t.Spans {
		total += span.Duration
	}
	return total
}

// AttributionRatio is AttributedDuration / WallDuration, or 0 when unattributable.
func (t *TurnTrace) AttributionRatio() float64 {
	wall := t.WallDuration()
	if wall <= 0 {
		return 0
	}
	return float64(t.AttributedDuration()) / float64(wall)
}

// Span returns the duration recorded for name, or zero if absent.
func (t *TurnTrace) Span(name string) time.Duration {
	if t == nil {
		return 0
	}
	for _, span := range t.Spans {
		if span.Name == name {
			return span.Duration
		}
	}
	return 0
}

// Counter returns the value recorded for name, or zero if absent.
func (t *TurnTrace) Counter(name string) int64 {
	if t == nil {
		return 0
	}
	for _, c := range t.Counters {
		if c.Name == name {
			return c.Value
		}
	}
	return 0
}

// RequiredEventKeys is the set of span/counter event keys a traced run is
// expected to emit. Tests assert via agenteval.MissingTraceEvents that a run
// produces all of these. Generation spans are only produced when the run
// reaches a model request, so callers may trim this list for short circuits.
func RequiredEventKeys() []string {
	return []string{
		"span:" + SpanPromptBuild,
		"span:" + SpanGeneration,
		"span:" + SpanToolExecution,
		"span:" + SpanPermissionWait,
		"span:" + SpanCompaction,
		"span:" + SpanPersistence,
		"span:" + SpanProviderConnect,
		"counter:" + CounterModelRequests,
		"counter:" + CounterToolCalls,
		"counter:" + CounterInputTokens,
		"counter:" + CounterOutputTokens,
		"trace:run",
	}
}
