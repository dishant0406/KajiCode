package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dishant0406/KajiCode/internal/providers/providerio"
)

// TestWaitingHeartbeatTicksUntilStopped pins the liveness contract: with an
// OnPhase sink, the heartbeat re-emits the wait phase with growing elapsed
// time until Stop, then goes quiet. Without this a silent stream is
// indistinguishable from a dead session.
func TestWaitingHeartbeatTicksUntilStopped(t *testing.T) {
	setWaitingTickIntervalForTest(20 * time.Millisecond)
	defer setWaitingTickIntervalForTest(defaultWaitingTickInterval)

	events := make(chan PhaseEvent, 64)
	options := Options{OnPhase: func(event PhaseEvent) { events <- event }}

	hb := startWaitingHeartbeat(context.Background(), options, PhaseProviderRequest, "waiting for model")
	time.Sleep(120 * time.Millisecond) // ~6 ticks
	hb.Stop()

	var got []PhaseEvent
	for {
		select {
		case event := <-events:
			got = append(got, event)
			continue
		case <-time.After(50 * time.Millisecond):
		}
		break
	}
	if len(got) < 2 {
		t.Fatalf("expected multiple heartbeat ticks, got %d", len(got))
	}
	for _, event := range got {
		if event.Kind != PhaseProviderRequest {
			t.Fatalf("tick kind = %q, want %q", event.Kind, PhaseProviderRequest)
		}
		if !strings.HasPrefix(event.Detail, "waiting for model") {
			t.Fatalf("tick detail %q missing base text", event.Detail)
		}
		if !strings.Contains(event.Detail, "s") {
			t.Fatalf("tick detail %q missing elapsed time", event.Detail)
		}
	}

	select {
	case event := <-events:
		t.Fatalf("unexpected tick after Stop: %+v", event)
	case <-time.After(80 * time.Millisecond):
	}
}

// TestWaitingHeartbeatBumpSwitchesPhase verifies bump() retargets ticks
// (provider_request -> streaming) without resetting the elapsed clock.
func TestWaitingHeartbeatBumpSwitchesPhase(t *testing.T) {
	setWaitingTickIntervalForTest(20 * time.Millisecond)
	defer setWaitingTickIntervalForTest(defaultWaitingTickInterval)

	events := make(chan PhaseEvent, 64)
	options := Options{OnPhase: func(event PhaseEvent) { events <- event }}

	hb := startWaitingHeartbeat(context.Background(), options, PhaseProviderRequest, "waiting for model")
	hb.bump(PhaseStreaming, "streaming model response")
	time.Sleep(100 * time.Millisecond)
	hb.Stop()

	sawStreaming := false
	for {
		select {
		case event := <-events:
			if event.Kind == PhaseStreaming && strings.HasPrefix(event.Detail, "streaming model response") {
				sawStreaming = true
			}
			continue
		case <-time.After(50 * time.Millisecond):
		}
		break
	}
	if !sawStreaming {
		t.Fatal("expected at least one tick on the bumped streaming phase")
	}
}

// TestWaitingHeartbeatNoSinkIsNoop keeps nil-OnPhase callers byte-identical:
// starting and stopping must be safe and produce nothing.
func TestWaitingHeartbeatNoSinkIsNoop(t *testing.T) {
	hb := startWaitingHeartbeat(context.Background(), Options{}, PhaseProviderRequest, "waiting for model")
	hb.bump(PhaseStreaming, "streaming") // must not panic
	hb.Stop()                            // must not hang
	hb.Stop()                            // double stop is fine
}

// TestTurnSilentBudgetRemaining pins the per-generation silent budget gate:
// fresh anchor => allowed; exhausted budget or cancelled ctx => blocked.
func TestTurnSilentBudgetRemaining(t *testing.T) {
	ctx := context.Background()

	if !turnSilentBudgetRemaining(ctx, time.Time{}) {
		t.Fatal("zero anchor should be allowed")
	}
	if !turnSilentBudgetRemaining(ctx, time.Now().Add(-time.Second)) {
		t.Fatal("1s-old anchor should be inside the budget")
	}

	idle := providerio.ResolveStreamIdleTimeout(0)
	budget := idle * time.Duration(turnSilentBudgetMultiplier)
	farPast := time.Now().Add(-budget - time.Second)
	if turnSilentBudgetRemaining(ctx, farPast) {
		t.Fatalf("anchor past budget (%v) should block further attempts", budget)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if turnSilentBudgetRemaining(cancelled, time.Now()) {
		t.Fatal("cancelled ctx must block")
	}
}

// TestPermissionWaitEmitsPhase verifies requestPermission surfaces the
// approval-wait phase before invoking the handler.
func TestPermissionWaitEmitsPhase(t *testing.T) {
	var kinds []PhaseKind
	options := Options{
		OnPhase: func(event PhaseEvent) { kinds = append(kinds, event.Kind) },
		OnPermissionRequest: func(_ context.Context, _ PermissionRequest) (PermissionDecision, error) {
			return PermissionDecision{Action: PermissionDecisionAllow}, nil
		},
	}
	decision, err := requestPermission(context.Background(), PermissionRequest{}, options)
	if err != nil {
		t.Fatalf("requestPermission: %v", err)
	}
	if decision.Action != PermissionDecisionAllow {
		t.Fatalf("decision = %v, want allow", decision.Action)
	}
	if len(kinds) == 0 || kinds[0] != PhasePermissionWaiting {
		t.Fatalf("expected first phase %q, got %v", PhasePermissionWaiting, kinds)
	}
}

// TestPermissionWaitNilHandlerDenies preserves the no-surface contract.
func TestPermissionWaitNilHandlerDenies(t *testing.T) {
	decision, err := requestPermission(context.Background(), PermissionRequest{Reason: "no surface"}, Options{})
	if err != nil {
		t.Fatalf("requestPermission: %v", err)
	}
	if decision.Action != PermissionDecisionDeny {
		t.Fatalf("nil handler must deny, got %v", decision.Action)
	}
}
