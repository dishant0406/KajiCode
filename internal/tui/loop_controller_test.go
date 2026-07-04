package tui

import (
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func loopTestModel(t *testing.T, now time.Time) model {
	t.Helper()
	m := newModel(context.Background(), Options{})
	m.now = func() time.Time { return now }
	return m
}

func startFixedLoop(m model, prompt string, interval time.Duration) model {
	m, _ = m.startLoop(loopCommand{action: loopActionStart, mode: loopModeFixed, prompt: prompt, interval: interval})
	return m
}

func TestStartLoopRegistersAndSchedules(t *testing.T) {
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	m := loopTestModel(t, now)
	m = startFixedLoop(m, "check the build", 5*time.Minute)

	if len(m.loops) != 1 {
		t.Fatalf("want 1 loop, got %d", len(m.loops))
	}
	l := m.loops[0]
	if l.mode != loopModeFixed || l.interval != 5*time.Minute || l.prompt != "check the build" {
		t.Fatalf("unexpected loop %+v", l)
	}
	// First iteration is due immediately (nextRunAt == now).
	if !l.due(now) {
		t.Errorf("first iteration should be due at creation")
	}
	if !m.loopTicking {
		t.Errorf("starting a loop should start the poll ticker")
	}
}

func TestAdvanceLoopSchedulesNextWake(t *testing.T) {
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	m := loopTestModel(t, now)
	m = startFixedLoop(m, "poll", 5*time.Minute)
	id := m.loops[0].id

	m = m.advanceLoop(id, "did some work", nil)
	l := m.loops[0]
	if l.iteration != 1 {
		t.Fatalf("iteration = %d, want 1", l.iteration)
	}
	if !l.nextRunAt.Equal(now.Add(5 * time.Minute)) {
		t.Fatalf("nextRunAt = %v, want +5m", l.nextRunAt)
	}
}

func TestSelfPacedUsesModelDelay(t *testing.T) {
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	m := loopTestModel(t, now)
	m, _ = m.startLoop(loopCommand{action: loopActionStart, mode: loopModeSelfPaced, prompt: "keep tidying"})
	id := m.loops[0].id
	m.loopNextWake = 10 * time.Minute // as if set by the loop_control tool
	m = m.advanceLoop(id, "progress", nil)
	if !m.loops[0].nextRunAt.Equal(now.Add(10 * time.Minute)) {
		t.Fatalf("self-paced nextRunAt = %v, want +10m", m.loops[0].nextRunAt)
	}
}

func TestLoopDoneStops(t *testing.T) {
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	m := loopTestModel(t, now)
	m = startFixedLoop(m, "finish the task", time.Minute)
	id := m.loops[0].id
	m.loopDone = true
	m = m.advanceLoop(id, "all done", nil)
	if len(m.loops) != 0 {
		t.Fatalf("loop should stop when the task reports done, %d remain", len(m.loops))
	}
}

func TestLoopDoomGuardStops(t *testing.T) {
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	m := loopTestModel(t, now)
	m = startFixedLoop(m, "spin", time.Minute)
	id := m.loops[0].id
	for i := 0; i < loopDoomThreshold+1 && len(m.loops) > 0; i++ {
		m = m.advanceLoop(id, "identical answer", nil)
	}
	if len(m.loops) != 0 {
		t.Fatalf("doom guard should stop a loop repeating identical results")
	}
}

func TestLoopIterationCapStops(t *testing.T) {
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	m := loopTestModel(t, now)
	m = startFixedLoop(m, "bounded", time.Minute)
	m.loops[0].maxIter = 3
	id := m.loops[0].id
	for i := 0; i < 3 && len(m.loops) > 0; i++ {
		m = m.advanceLoop(id, "answer "+string(rune('a'+i)), nil) // distinct answers so doom doesn't fire
	}
	if len(m.loops) != 0 {
		t.Fatalf("loop should stop at its iteration cap")
	}
}

func TestStopLoopByID(t *testing.T) {
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	m := loopTestModel(t, now)
	m = startFixedLoop(m, "a", time.Minute)
	m = startFixedLoop(m, "b", time.Minute)
	id := m.loops[0].id
	m, _ = m.stopLoop(id)
	if len(m.loops) != 1 || m.findLoop(id) != nil {
		t.Fatalf("stopLoop(%s) should remove exactly that loop", id)
	}
	m, _ = m.stopAllLoops()
	if len(m.loops) != 0 {
		t.Fatalf("stopAllLoops should clear all loops")
	}
}

func TestFireDueLoopSkipsWhenBusy(t *testing.T) {
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	m := loopTestModel(t, now)
	m = startFixedLoop(m, "x", time.Minute)
	m.pending = true // a turn is in flight
	got, cmd := m.fireDueLoopIfIdle()
	if got.activeLoopID != "" || cmd != nil {
		t.Fatalf("a due loop must not fire while a turn is pending")
	}
}

func TestAdaptiveSelfPaceDelayWidensOverTime(t *testing.T) {
	if adaptiveSelfPaceDelay(0) >= adaptiveSelfPaceDelay(5) {
		t.Error("early iterations should check back sooner than later ones")
	}
	if adaptiveSelfPaceDelay(20) != 30*time.Minute {
		t.Errorf("a mature loop should settle to a 30m heartbeat, got %v", adaptiveSelfPaceDelay(20))
	}
}

func TestLoopFooterSummary(t *testing.T) {
	now := time.Date(2026, 7, 5, 15, 0, 0, 0, time.UTC)
	m := loopTestModel(t, now)
	if m.loopFooterSummary() != "" {
		t.Error("no loops -> empty footer summary")
	}
	m = startFixedLoop(m, "a", 5*time.Minute)
	m.loops[0].nextRunAt = now.Add(5 * time.Minute)
	got := m.loopFooterSummary()
	if got == "" || got[:6] != "1 loop" {
		t.Fatalf("footer summary = %q, want it to start with a loop count", got)
	}
}

func TestLoopCommandThroughSubmit(t *testing.T) {
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	m := loopTestModel(t, now)
	m.input.SetValue("/loop 5m tidy the imports")
	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if len(next.loops) != 1 {
		t.Fatalf("submitting /loop should create a loop, got %d", len(next.loops))
	}
	if next.loops[0].mode != loopModeFixed || next.loops[0].interval != 5*time.Minute {
		t.Fatalf("unexpected loop from submit: %+v", next.loops[0])
	}
	if !next.loopTicking {
		t.Fatalf("submitting /loop should start the poll ticker")
	}

	// /loop stop all through the same path clears it.
	next.input.SetValue("/loop stop all")
	updated2, _ := next.Update(testKey(tea.KeyEnter))
	final := updated2.(model)
	if len(final.loops) != 0 {
		t.Fatalf("/loop stop all should clear loops, got %d", len(final.loops))
	}
}

func TestStaleLoopTickIgnored(t *testing.T) {
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	m := loopTestModel(t, now)
	m = startFixedLoop(m, "x", time.Minute)
	m.loopSeq = 7
	// A tick from before the seq changed must be dropped and stop the ticker.
	updated, cmd := m.Update(loopTickMsg{seq: 3})
	next := updated.(model)
	if next.loopTicking {
		t.Error("a stale loop tick should stop the ticker")
	}
	if cmd != nil {
		t.Error("a stale loop tick should not reschedule")
	}
}

func TestValidateLoopTargetRejectsBuiltin(t *testing.T) {
	if _, ok := validateLoopTarget("/model list"); ok {
		t.Error("looping a built-in command should be rejected")
	}
	if _, ok := validateLoopTarget("keep improving docs"); !ok {
		t.Error("a plain prompt should be a valid loop target")
	}
	if _, ok := validateLoopTarget(""); ok {
		t.Error("an empty target should be rejected")
	}
}
