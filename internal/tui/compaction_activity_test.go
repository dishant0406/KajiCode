package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/dishant0406/KajiCode/internal/agent"
)

// TestAgentCompactionUpsertsInThreadRowAndCounts verifies that the phase-driven
// "compressing…" transient row is replaced by the completion row and that the
// per-session counter increments exactly once per agentCompactionMsg.
func TestAgentCompactionUpsertsInThreadRowAndCounts(t *testing.T) {
	m := newModel(context.Background(), Options{ModelName: "gpt-4.1"})
	m.activeRunID = 7

	// Phase pings while running must not stack transient rows.
	for i := 0; i < 3; i++ {
		updated, _ := m.Update(agentPhaseMsg{runID: 7, phase: agent.PhaseEvent{Kind: agent.PhaseCompacting}})
		m = updated.(model)
	}
	rows := 0
	for _, r := range m.transcript {
		if r.id == agentCompactionRowID {
			rows++
			if !strings.HasPrefix(strings.TrimSpace(r.text), "Compressing session") {
				t.Fatalf("expected running compact row, got text:\n%s", r.text)
			}
		}
	}
	if rows != 1 {
		t.Fatalf("expected exactly one in-thread compaction row while running, got %d", rows)
	}

	// Completion replaces the transient row and bumps the counter once.
	updated, _ := m.Update(agentCompactionMsg{
		runID: 7,
		event: agent.CompactionEvent{Trigger: "high-water", Summary: "s3", RemovedCount: 42},
	})
	m = updated.(model)
	if m.compactions != 1 {
		t.Fatalf("expected compactions=1 after one completion, got %d", m.compactions)
	}
	rows = 0
	for _, r := range m.transcript {
		if r.id == agentCompactionRowID {
			rows++
			if !strings.HasPrefix(strings.TrimSpace(r.text), "Compression complete") {
				t.Fatalf("expected completed compact row, got text:\n%s", r.text)
			}
			if !strings.Contains(r.text, "42 messages") {
				t.Fatalf("expected completion row to mention removed count, got:\n%s", r.text)
			}
		}
	}
	if rows != 1 {
		t.Fatalf("expected the transient row to be replaced (not stacked), got %d completion rows", rows)
	}
}

// TestAgentCompactionIgnoresStaleRunID guards against replaying a finished run's
// compaction into the wrong conversation view.
func TestAgentCompactionIgnoresStaleRunID(t *testing.T) {
	m := newModel(context.Background(), Options{ModelName: "gpt-4.1"})
	m.activeRunID = 3
	updated, _ := m.Update(agentCompactionMsg{runID: 99, event: agent.CompactionEvent{RemovedCount: 5}})
	next := updated.(model)
	if next.compactions != 0 {
		t.Fatalf("stale-run compaction must not bump the counter, got %d", next.compactions)
	}
	for _, r := range next.transcript {
		if r.id == agentCompactionRowID {
			t.Fatalf("stale-run compaction must not render a row, got text:\n%s", r.text)
		}
	}
}

// TestSidebarAndFooterExposeCompactionCount ensures the ACTIVITY feed and footer
// both surface the per-session "♻ compacted N×" counter.
func TestSidebarAndFooterExposeCompactionCount(t *testing.T) {
	m := newModel(context.Background(), Options{ModelName: "gpt-4.1"})
	m.activeRunID = 5
	m.compactions = 3

	activity := plainRender(t, strings.Join(m.sidebarActivityLines(40, 10), "\n"))
	if !strings.Contains(activity, "compacted 3x") {
		t.Fatalf("expected sidebar ACTIVITY to show 'compacted 3x', got:\n%s", activity)
	}

	footer := plainRender(t, m.statusLine(80))
	if !strings.Contains(footer, "compacted 3x") {
		t.Fatalf("expected footer to show 'compacted 3x', got:\n%s", footer)
	}
}

// TestNewSessionResetsCompactionCount ensures a fresh session starts from zero.
func TestNewSessionResetsCompactionCount(t *testing.T) {
	m := newModel(context.Background(), Options{ModelName: "gpt-4.1"})
	m.compactions = 3
	m = m.startNewSession()
	if m.compactions != 0 {
		t.Fatalf("new session must reset compaction count, got %d", m.compactions)
	}
}
