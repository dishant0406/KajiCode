package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/dishant0406/KajiCode/internal/agent"
)

// TestPlanCommandTogglesPlanMode covers the /plan forms: bare toggles, on/off set
// explicitly, status is read-only, and an unknown arg shows usage.
func TestPlanCommandTogglesPlanMode(t *testing.T) {
	m := newModel(context.Background(), Options{})

	next, _ := m.dispatchCommand(parseCommand("/plan"))
	m = next.(model)
	if !m.planMode {
		t.Fatal("bare /plan should turn plan mode on")
	}
	if !transcriptContains(m.transcript, "Plan mode ON") {
		t.Fatalf("expected an ON notice, got %#v", m.transcript)
	}

	next, _ = m.dispatchCommand(parseCommand("/plan"))
	m = next.(model)
	if m.planMode {
		t.Fatal("bare /plan should toggle plan mode off")
	}

	next, _ = m.dispatchCommand(parseCommand("/plan on"))
	m = next.(model)
	if !m.planMode {
		t.Fatal("/plan on should enable plan mode")
	}

	next, _ = m.dispatchCommand(parseCommand("/plan off"))
	m = next.(model)
	if m.planMode {
		t.Fatal("/plan off should disable plan mode")
	}

	// status is read-only: it reports without flipping the flag.
	next, _ = m.dispatchCommand(parseCommand("/plan status"))
	m = next.(model)
	if m.planMode {
		t.Fatal("/plan status must not change the flag")
	}

	next, _ = m.dispatchCommand(parseCommand("/plan bogus"))
	m = next.(model)
	if !transcriptContains(m.transcript, "usage: /plan") {
		t.Fatalf("expected usage guidance, got %#v", m.transcript)
	}
}

// TestPlanModeKeybindingToggles drives the real Ctrl+G chord through Update.
func TestPlanModeKeybindingToggles(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.width, m.height = 100, 40

	updated, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'g', Mod: tea.ModCtrl}))
	got := updated.(model)
	if !got.planMode {
		t.Fatal("Ctrl+G should enable plan mode")
	}

	updated, _ = got.Update(tea.KeyPressMsg(tea.Key{Code: 'g', Mod: tea.ModCtrl}))
	got = updated.(model)
	if got.planMode {
		t.Fatal("Ctrl+G should toggle plan mode back off")
	}
}

// TestPlanModeStatusLabel asserts the status chip reflects plan mode.
func TestPlanModeStatusLabel(t *testing.T) {
	m := newModel(context.Background(), Options{})
	if label, _ := m.modeLabel(); label == "plan" {
		t.Fatal("plan label must not show while plan mode is off")
	}
	m.planMode = true
	if label, _ := m.modeLabel(); label != "plan" {
		t.Fatalf("modeLabel = %q, want plan", label)
	}
}

// TestPlanApprovedClearsModeAndQueuesExecution covers the handoff: a run that
// ends with plan approval clears plan mode, posts a notice, and queues the
// execution prompt so the next run implements the plan.
func TestPlanApprovedClearsModeAndQueuesExecution(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.pending = true
	m.activeRunID = 7
	m.planMode = true

	updated, _ := m.Update(agentResponseMsg{runID: 7, planApproved: true})
	got := updated.(model)
	if got.planMode {
		t.Fatal("approving the plan must clear plan mode")
	}
	if !transcriptContains(got.transcript, "Plan approved") {
		t.Fatalf("expected an approval notice, got %#v", got.transcript)
	}
	// launchQueuedMessageIfReady fires the execution run in the same Update: the
	// queue is consumed and the execution prompt is echoed as the next user turn.
	if got.queuedMessage != "" {
		t.Fatalf("the execution prompt should be consumed by the immediate launch, still queued: %q", got.queuedMessage)
	}
	if !transcriptContains(got.transcript, agent.PlanExecutionPrompt) {
		t.Fatalf("expected the execution prompt to start the next turn, got %#v", got.transcript)
	}
}
