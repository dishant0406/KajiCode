package tui

import (
	"testing"

	"github.com/dishant0406/KajiCode/internal/agent"
)

// TestPhaseActivityLabelHeartbeats pins the status-line rendering of the new
// liveness phases: heartbeat ticks show their live detail text, and the
// permission-wait phase gets a distinct label so "waiting for you" never
// reads as "waiting for the model".
func TestPhaseActivityLabelHeartbeats(t *testing.T) {
	cases := []struct {
		phase agent.PhaseEvent
		want  string
	}{
		{agent.PhaseEvent{Kind: agent.PhaseProviderRequest, Detail: ""}, "waiting for model"},
		{agent.PhaseEvent{Kind: agent.PhaseProviderRequest, Detail: "waiting for model… 45s"}, "waiting for model… 45s"},
		{agent.PhaseEvent{Kind: agent.PhaseStreaming, Detail: "streaming model response… 1m30s"}, "streaming model response… 1m30s"},
		{agent.PhaseEvent{Kind: agent.PhasePermissionWaiting, Detail: ""}, "waiting for approval"},
		{agent.PhaseEvent{Kind: agent.PhasePermissionWaiting, Detail: "waiting for approval… 30s"}, "waiting for approval… 30s"},
	}
	for _, tc := range cases {
		if got := phaseActivityLabel(tc.phase); got != tc.want {
			t.Fatalf("phaseActivityLabel(%+v) = %q, want %q", tc.phase, got, tc.want)
		}
	}
}
