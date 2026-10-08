package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dishant0406/KajiCode/internal/agent"
	"github.com/dishant0406/KajiCode/internal/sessions"
)

// compactionEventPayload marshals a compaction payload for replay tests.
func compactionEventPayload(t *testing.T, value map[string]any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return data
}

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

// The in-thread compaction card must stay a fixed, small height no matter what
// the summarizer produced. A degenerate summary (a re-emitted transcript) can be
// hundreds of kilobytes; the card shows only a bounded preview so the TUI is
// never flooded with thousands of lines.
func TestAgentCompactionCardBoundsSummaryPreview(t *testing.T) {
	m := newModel(context.Background(), Options{ModelName: "gpt-4.1"})

	// A 5,000-line transcript echo, like the summaries observed in real sessions.
	var degenerate strings.Builder
	degenerate.WriteString("## Objective\n")
	for i := 0; i < 5000; i++ {
		degenerate.WriteString("5190:export type AppStoreVersionCreateRequest = {\n")
	}
	degenerate.WriteString("\n## Next Move\nact")

	text := m.agentCompactCompleteText(agent.CompactionEvent{
		Trigger:      "proactive",
		Summary:      degenerate.String(),
		RemovedCount: 120,
	})
	lines := strings.Split(text, "\n")
	if len(lines) > 10 {
		t.Fatalf("compaction card must stay small, got %d lines:\n%s", len(lines), text)
	}
	// Consecutive duplicates are collapsed, so the repeated transcript line can
	// appear at most once.
	if got := strings.Count(text, "5190:export type AppStoreVersionCreateRequest = {"); got > 1 {
		t.Fatalf("expected duplicate transcript lines to be collapsed, got %d:\n%s", got, text)
	}
}

// A normal templated summary must still be shown, bounded, so the card keeps its
// usefulness.
func TestAgentCompactionCardShowsTemplatedSummaryPreview(t *testing.T) {
	m := newModel(context.Background(), Options{ModelName: "gpt-4.1"})

	text := m.agentCompactCompleteText(agent.CompactionEvent{
		Trigger:      "proactive",
		Summary:      "## Objective\nFix the parser.\n\n## Next Move\nRun the tests.",
		RemovedCount: 12,
	})
	for _, want := range []string{"Compression complete", "12 messages", "## Objective", "Fix the parser."} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected the card to contain %q, got:\n%s", want, text)
		}
	}
}

// An empty summary must not add a dangling "Summary:" line.
func TestAgentCompactionCardOmitsEmptySummary(t *testing.T) {
	m := newModel(context.Background(), Options{ModelName: "gpt-4.1"})
	text := m.agentCompactCompleteText(agent.CompactionEvent{Trigger: "proactive", RemovedCount: 3})
	if strings.Contains(text, "Summary:") {
		t.Fatalf("expected no Summary line for an empty summary, got:\n%s", text)
	}
}

// The manual compact command card renders the same summary; it must be bounded
// too, so a degenerate summary cannot flood the command output.
func TestCompactResultLinesBoundsSummary(t *testing.T) {
	degenerate := "## Objective\n" + strings.Repeat("5190:export type AppStoreVersionCreateRequest = {\n", 5000)
	lines := compactResultLines(CompactResult{Compacted: true, Summary: degenerate})
	if len(lines) > 12 {
		t.Fatalf("compact result must stay small, got %d lines", len(lines))
	}
	joined := strings.Join(lines, "\n")
	if got := strings.Count(joined, "5190:export type AppStoreVersionCreateRequest = {"); got > 1 {
		t.Fatalf("expected duplicate summary lines to be collapsed, got %d", got)
	}
}

// Resuming a session must not replay an already-persisted degenerate summary at
// full size: sessions compacted before validation existed still hold them.
func TestSessionReplayBoundsPersistedCompactionSummary(t *testing.T) {
	degenerate := "## Objective\n" + strings.Repeat("5190:export type AppStoreVersionCreateRequest = {\n", 5000)
	events := []sessions.Event{{
		Type:    sessions.EventCompaction,
		Payload: compactionEventPayload(t, map[string]any{"summary": degenerate}),
	}}
	rows := transcriptRowsFromSessionEvents(events)
	if len(rows) != 1 {
		t.Fatalf("expected one replayed row, got %d", len(rows))
	}
	if got := strings.Count(rows[0].text, "5190:export type AppStoreVersionCreateRequest = {"); got > 1 {
		t.Fatalf("expected the replayed summary to be collapsed, got %d occurrences", got)
	}
	if len(rows[0].text) > 2000 {
		t.Fatalf("expected a bounded replayed summary, got %d bytes", len(rows[0].text))
	}
}
