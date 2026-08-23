package agent

import "strings"

// PhaseKind names the current non-model-visible run phase. Surfaces use this
// for liveness/status only; it is never appended to the conversation.
type PhaseKind string

const (
	PhaseProviderRequest PhaseKind = "provider_request"
	PhaseStreaming       PhaseKind = "streaming"
	PhaseToolRunning     PhaseKind = "tool_running"
	PhaseToolDone        PhaseKind = "tool_done"
	PhaseDiagnostics     PhaseKind = "diagnostics"
	PhaseSelfCorrect     PhaseKind = "self_correct"
	PhaseRetrying        PhaseKind = "retrying"
	PhaseCompacting      PhaseKind = "compacting"
	PhaseFinalizing      PhaseKind = "finalizing"
	// PhasePermissionWaiting marks the loop blocked on a permission/approval
	// decision. Distinct from PhaseProviderRequest so surfaces can show
	// "waiting for you" instead of "waiting for the model" during approval.
	PhasePermissionWaiting PhaseKind = "permission_waiting"
)

// PhaseEvent reports a live agent-loop phase to the caller.
type PhaseEvent struct {
	Kind   PhaseKind
	Detail string
}

func emitPhase(options Options, kind PhaseKind, detail string) {
	if options.OnPhase == nil {
		return
	}
	options.OnPhase(PhaseEvent{Kind: kind, Detail: strings.TrimSpace(detail)})
}
