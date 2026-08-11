package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
)

// --- planTail / turn splitting -------------------------------------------

func userMsg(c string) kajicoderuntime.Message {
	return kajicoderuntime.Message{Role: kajicoderuntime.MessageRoleUser, Content: c}
}
func asstMsg(c string) kajicoderuntime.Message {
	return kajicoderuntime.Message{Role: kajicoderuntime.MessageRoleAssistant, Content: c}
}
func sysMsg(c string) kajicoderuntime.Message {
	return kajicoderuntime.Message{Role: kajicoderuntime.MessageRoleSystem, Content: c}
}

func TestSplitTurnsGroupsByUser(t *testing.T) {
	msgs := []kajicoderuntime.Message{
		sysMsg("s"),
		userMsg("u1"), asstMsg("a1"), asstMsg("a1b"),
		userMsg("u2"), asstMsg("a2"),
		userMsg("u3"),
	}
	turns := splitTurns(msgs, 0)
	if len(turns) != 3 {
		t.Fatalf("expected 3 turns, got %d", len(turns))
	}
	if turns[0].start != 1 || turns[0].end != 4 {
		t.Fatalf("turn0 = [%d:%d], want [1:4]", turns[0].start, turns[0].end)
	}
	if turns[1].start != 4 || turns[1].end != 6 {
		t.Fatalf("turn1 = [%d:%d], want [4:6]", turns[1].start, turns[1].end)
	}
	if turns[2].start != 6 || turns[2].end != 7 {
		t.Fatalf("turn2 = [%d:%d], want [6:7]", turns[2].start, turns[2].end)
	}
}

func TestSplitTurnsSkipsSummaryMarker(t *testing.T) {
	msgs := []kajicoderuntime.Message{
		sysMsg("s"),
		userMsg("[Summary of earlier conversation]\nthe summary"),
		userMsg("u1"), asstMsg("a1"),
		userMsg("u2"),
	}
	turns := splitTurns(msgs, 0)
	if len(turns) != 2 {
		t.Fatalf("summary marker must not begin a turn; got %d turns", len(turns))
	}
	if turns[0].start != 2 || turns[0].end != 4 {
		t.Fatalf("first real turn = [%d:%d], want [2:4]", turns[0].start, turns[0].end)
	}
}

func TestPlanTailKeepsNewestTurnAlways(t *testing.T) {
	// Small budget: only the mandatory newest turn survives (plus split nothing).
	msgs := []kajicoderuntime.Message{
		sysMsg("s"),
		userMsg("u1"), asstMsg("a1"),
		userMsg(strings.Repeat("u2", 2000)), asstMsg(strings.Repeat("a2", 2000)),
	}
	// headLimit=1 past the system message; tailTurns=0 forces nothing older kept.
	boundary := planTail(msgs, 1, 1, 100)
	// The newest user (index 3) must be at or after the boundary.
	if boundary > 3 {
		t.Fatalf("newest user must be kept verbatim; boundary=%d (newest user at 3)", boundary)
	}
}

func TestPlanTailBudgetsOlderTurns(t *testing.T) {
	msgs := []kajicoderuntime.Message{
		sysMsg("s"),
		userMsg("u1"), asstMsg("a1"),
		userMsg("u2"), asstMsg("a2"),
		userMsg("u3"), asstMsg("a3"),
	}
	// tailTurns=3, generous budget keeps all three turns → boundary at u1 (1).
	boundary := planTail(msgs, 1, 3, 100000)
	if boundary != 1 {
		t.Fatalf("expected boundary at u1 (index 1), got %d", boundary)
	}
	// tailTurns=1 and tiny budget keeps only the newest turn → boundary at u3 (5).
	boundary = planTail(msgs, 1, 1, 0)
	if boundary != 5 {
		t.Fatalf("expected boundary at u3 (index 5), got %d", boundary)
	}
}

func TestSplitTurnToBudgetKeepsNewestSuffix(t *testing.T) {
	msgs := []kajicoderuntime.Message{
		userMsg("u1"),
		asstMsg(strings.Repeat("big", 1000)), // ~1000 tokens
		asstMsg("small-suffix"),
	}
	turn := tailTurn{start: 0, end: 3}
	// remaining budget fits only the two newest messages (a "big" then suffix) or
	// partial; ensure the split retains at least the newest (index 2).
	split := splitTurnToBudget(msgs, turn, 300)
	if split < 2 {
		t.Fatalf("split must keep the newest message (index 2); got split=%d", split)
	}
}

func TestPlanTailKeepEverythingWhenNoTurns(t *testing.T) {
	msgs := []kajicoderuntime.Message{sysMsg("s")}
	if boundary := planTail(msgs, 1, 2, 1000); boundary != len(msgs) {
		t.Fatalf("expected keep-everything boundary %d, got %d", len(msgs), boundary)
	}
}

func TestTailTokenBudgetClamps(t *testing.T) {
	if got := tailTokenBudget(0); got != 0 {
		t.Fatalf("zero window should yield 0 budget, got %d", got)
	}
	if got := tailTokenBudget(200_000); got != 8000 {
		t.Fatalf("large window should clamp to 8000, got %d", got)
	}
	// 10000*0.25 = 2500.
	if got := tailTokenBudget(10_000); got != 2500 {
		t.Fatalf("mid window budget = %d, want 2500", got)
	}
	// 1000*0.25 = 250 < 2000 floor.
	if got := tailTokenBudget(1000); got != 2000 {
		t.Fatalf("small window budget should floor at 2000, got %d", got)
	}
}

// --- Budgeted Compact integration ----------------------------------------

func TestCompactBudgetedTailKeepsRecentTurns(t *testing.T) {
	msgs := []kajicoderuntime.Message{
		sysMsg("s"),
		userMsg("old q1"), asstMsg("old a1"),
		userMsg("q2"), asstMsg("a2"),
		userMsg("q3"), asstMsg("a3"),
	}
	var captured []kajicoderuntime.Message
	out, err := Compact(msgs, CompactionOptions{
		TailTurns:       2,
		TailTokenBudget: 100000,
		ContextWindow:   200_000,
		Summarize: func(toSummarize []kajicoderuntime.Message) (string, error) {
			captured = toSummarize
			return "SUMMARY", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Tail turns (q2..a3) kept verbatim, so their content survives.
	joined := strings.Join(func() []string {
		var s []string
		for _, m := range out {
			s = append(s, m.Content)
		}
		return s
	}(), "|")
	for _, want := range []string{"q3", "a3", "q2", "a2"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("budgeted tail must keep %q verbatim, got %q", want, joined)
		}
	}
	// The old head (q1) is what gets summarized; a1 starts the preserved suffix
	// so the suffix begins at an assistant (alternation-safe).
	if len(captured) != 1 || captured[0].Content != "old q1" {
		t.Fatalf("expected head [q1] summarized, got %#v", captured)
	}
	// No consecutive user messages (Alternation constraint).
	for i := 1; i < len(out); i++ {
		if out[i].Role == kajicoderuntime.MessageRoleUser && out[i-1].Role == kajicoderuntime.MessageRoleUser {
			t.Fatalf("budgeted tail produced consecutive user messages at %d: %+v", i, out)
		}
	}
}

func TestCompactBudgetedNoopWhenNothingToSummarize(t *testing.T) {
	msgs := []kajicoderuntime.Message{
		sysMsg("s"),
		userMsg("u1"), asstMsg("a1"),
	}
	called := false
	out, err := Compact(msgs, CompactionOptions{
		TailTurns:       2,
		TailTokenBudget: 100000,
		ContextWindow:   200_000,
		Summarize: func([]kajicoderuntime.Message) (string, error) {
			called = true
			return "x", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("nothing to summarize when budgeted tail covers the whole history")
	}
	if len(out) != len(msgs) {
		t.Fatalf("expected unchanged history, got %d messages", len(out))
	}
}

func TestCompactBudgetedSplitsOverBudgetTurn(t *testing.T) {
	msgs := []kajicoderuntime.Message{
		sysMsg("s"),
		userMsg("old"), asstMsg("old answer"),
		userMsg("recent"), asstMsg(strings.Repeat("R", 4000)), // big turn
	}
	// Budget fits only the newest ask plus a tiny part of the big answer.
	var out []kajicoderuntime.Message
	captured := 0
	var err error
	out, err = Compact(msgs, CompactionOptions{
		TailTurns:       2,
		TailTokenBudget: 200,
		ContextWindow:   200_000,
		Summarize: func([]kajicoderuntime.Message) (string, error) {
			captured++
			return "SUMMARY", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = out
	if captured != 1 {
		t.Fatalf("expected one summarization pass, got %d", captured)
	}
	// The newest ask (recent) must survive regardless, and the whole tail must not
	// contain the old user "old" (it folded into the summary head).
	for _, m := range out {
		if strings.Contains(m.Content, "old\n") {
			t.Fatalf("old head should be summarized away, got %+v", out)
		}
	}
}

// --- Anchored summary / extractPreviousSummary ---------------------------

func TestExtractPreviousSummaryFindsMarker(t *testing.T) {
	msgs := []kajicoderuntime.Message{
		sysMsg("s"),
		userMsg("[Summary of earlier conversation]\nthe running summary"),
		userMsg("u"), asstMsg("a"),
	}
	got := extractPreviousSummary(msgs)
	if !strings.Contains(got, "the running summary") {
		t.Fatalf("expected previous summary body, got %q", got)
	}
}

func TestExtractPreviousSummaryStripsPreservedState(t *testing.T) {
	msgs := []kajicoderuntime.Message{
		userMsg("[Summary of earlier conversation]\nprose\n\n## Preserved state (active plan + loaded skills; carried across compaction)\n{\"plan\":\"x\"}"),
	}
	got := extractPreviousSummary(msgs)
	if strings.Contains(got, preservedStateLabel) {
		t.Fatalf("preserved-state block should be stripped, got %q", got)
	}
	if !strings.Contains(got, "prose") {
		t.Fatalf("prose summary should be retained, got %q", got)
	}
}

func TestExtractPreviousSummaryEmptyWhenNone(t *testing.T) {
	msgs := []kajicoderuntime.Message{userMsg("no summary here"), asstMsg("a")}
	if got := extractPreviousSummary(msgs); got != "" {
		t.Fatalf("expected no previous summary, got %q", got)
	}
}

// --- Bounded tool output in summarizer transcript ------------------------

func TestRenderTranscriptBoundsToolOutput(t *testing.T) {
	big := strings.Repeat("D", 5000)
	msgs := []kajicoderuntime.Message{{Role: kajicoderuntime.MessageRoleTool, Content: big, ToolCallID: "1"}}
	got := renderTranscript(msgs)
	if len(got) > summarizeToolResultMaxBytes+256 {
		t.Fatalf("tool output should be bounded; got %d bytes", len(got))
	}
	if !strings.Contains(got, "output truncated") {
		t.Fatalf("expected truncation note, got %q", got)
	}
}

func TestRenderTranscriptMarksPrunedPlaceholder(t *testing.T) {
	placeholder := "[pruned read_file output (~200 tokens) to reclaim context — re-run the tool if you need it again]"
	msgs := []kajicoderuntime.Message{{Role: kajicoderuntime.MessageRoleTool, Content: placeholder, ToolCallID: "1"}}
	got := renderTranscript(msgs)
	if !strings.Contains(got, "Old tool result content cleared") {
		t.Fatalf("pruned placeholder should be described as cleared, got %q", got)
	}
}

func TestMaybeCompactBudgetedTailActivatesForRealisticWindow(t *testing.T) {
	// A realistic-ish model window (>= 32k turns on the budgeted tail, and the
	// large head trips the threshold) so maybeCompact keeps a recent turn window
	// verbatim rather than only a bare message count.
	turns := []kajicoderuntime.Message{
		sysMsg("s"),
		userMsg("q1"), asstMsg(strings.Repeat("a1", 60_000)), // big old head
		userMsg("q2"), asstMsg("a2"),
		userMsg("q3"), asstMsg("a3"),
	}
	provider := &summarizeRecordingProvider{
		turns: [][]kajicoderuntime.StreamEvent{{
			{Type: kajicoderuntime.StreamEventText, Content: "answer"},
			{Type: kajicoderuntime.StreamEventDone},
		}},
	}
	// Realistic window so budgeted tail activates and threshold trips.
	st := newCompactionState(Options{ContextWindow: 40_000}, nil)
	if st.tailTurns == 0 {
		t.Fatal("realistic window should enable the budgeted tail by default")
	}
	out := st.maybeCompact(context.Background(), provider, turns, nil)
	if provider.summarizeCalls == 0 {
		t.Fatal("expected compaction to run")
	}
	var contents []string
	for _, m := range out {
		contents = append(contents, m.Content)
	}
	joined := strings.Join(contents, "|")
	// The recent asks and answers must survive verbatim in the compacted output.
	for _, want := range []string{"q3", "a3", "q2", "a2"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("budgeted tail must keep %q verbatim, got %q", want, joined)
		}
	}
}

func TestSummaryInstructionsMandatesTemplate(t *testing.T) {
	if !strings.Contains(summaryInstructions, "## Objective") ||
		!strings.Contains(summaryInstructions, "## Work State") ||
		!strings.Contains(summaryInstructions, "## Relevant Files") {
		t.Fatal("summary instructions must mandate the strict Markdown template")
	}
}
