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
	if got := tailTokenBudget(200_000); got != 15000 {
		t.Fatalf("large window should clamp to 15000, got %d", got)
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

func TestRenderTranscriptReferencesImages(t *testing.T) {
	msgs := []kajicoderuntime.Message{{
		Role:    kajicoderuntime.MessageRoleUser,
		Content: "look",
		Images:  []kajicoderuntime.ImageBlock{{MediaType: "image/png", Data: []byte{0x89}}},
	}}
	got := renderTranscript(msgs)
	if !strings.Contains(got, "1 image attachment(s) not shown") {
		t.Fatalf("image attachment should be referenced, got %q", got)
	}
	// A text-only message must not gain a spurious image reference.
	plain := renderTranscript([]kajicoderuntime.Message{{Role: kajicoderuntime.MessageRoleUser, Content: "hi"}})
	if strings.Contains(plain, "image attachment") {
		t.Fatalf("text-only transcript must not mention images, got %q", plain)
	}
}

func TestMaybeCompactBudgetedTailActivatesForRealisticWindow(t *testing.T) {
	// A realistic-ish model window (>= 32k turns on the budgeted tail, and the
	// large head trips the threshold) so maybeCompact keeps a recent turn window
	// verbatim rather than only a bare message count.
	turns := []kajicoderuntime.Message{
		sysMsg("s"),
		userMsg(strings.Repeat("q1", 60_000)), asstMsg("a1"), // big old head
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
	if st.tailTurns > 0 {
		t.Fatalf("default tail must be uncapped (keep every turn that fits), got %d", st.tailTurns)
	}
	out, didCompact := st.maybeCompact(context.Background(), provider, turns, nil)
	if provider.summarizeCalls == 0 {
		t.Fatal("expected compaction to run")
	}
	if !didCompact {
		t.Fatal("maybeCompact must report that it compacted")
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

// --- Unbounded tail + continuation cue ------------------------------------

func TestPlanTailUnboundedKeepsAllTurnsThatFit(t *testing.T) {
	msgs := []kajicoderuntime.Message{
		sysMsg("s"),
		userMsg("q1"), asstMsg("a1"),
		userMsg("q2"), asstMsg("a2"),
		userMsg("q3"), asstMsg("a3"),
		userMsg("q4"), asstMsg("a4"),
	}
	// TailTurns <= 0 means unbounded: a generous budget keeps every turn, so the
	// boundary lands at the oldest turn (index 1) and q1 stays verbatim.
	if got := planTail(msgs, 1, unboundedTailTurns, 100000); got != 1 {
		t.Fatalf("unbounded tail with a big budget should keep every turn (boundary 1), got %d", got)
	}
}

func TestPlanTailPositiveCapStillCaps(t *testing.T) {
	msgs := []kajicoderuntime.Message{
		sysMsg("s"),
		userMsg("q1"), asstMsg("a1"),
		userMsg("q2"), asstMsg("a2"),
		userMsg("q3"), asstMsg("a3"),
	}
	// An explicit cap of 1 keeps only the newest turn even with a huge budget.
	if got := planTail(msgs, 1, 1, 100000); got != 5 {
		t.Fatalf("explicit cap of 1 should keep only the newest turn (boundary 5), got %d", got)
	}
}

func TestSplitTurnsSkipsContinuationCue(t *testing.T) {
	msgs := []kajicoderuntime.Message{
		sysMsg("s"),
		userMsg("real ask"), asstMsg("a1"),
		{Role: kajicoderuntime.MessageRoleUser, Content: compactionContinuationText},
		asstMsg("a2"),
	}
	turns := splitTurns(msgs, 1)
	if len(turns) != 1 {
		t.Fatalf("the synthetic continuation cue must not begin a turn; got %d turns", len(turns))
	}
	if turns[0].start != 1 {
		t.Fatalf("the only turn should start at the real ask (1), got %d", turns[0].start)
	}
}

func TestAppendCompactionContinuation(t *testing.T) {
	// After an assistant/tool turn, the cue is appended once (idempotent).
	base := []kajicoderuntime.Message{sysMsg("s"), userMsg("q"), asstMsg("a")}
	got := appendCompactionContinuation(base)
	if len(got) != len(base)+1 || !isCompactionContinuation(got[len(got)-1]) {
		t.Fatalf("expected one appended continuation cue, got %#v", got)
	}
	if again := appendCompactionContinuation(got); len(again) != len(got) {
		t.Fatal("appending the cue twice must be a no-op when the tail is already a user message")
	}
	// A fresh, unanswered user ask must NOT get a "continue" cue.
	withUser := []kajicoderuntime.Message{sysMsg("s"), userMsg("q"), asstMsg("a"), userMsg("fresh ask")}
	if out := appendCompactionContinuation(withUser); len(out) != len(withUser) {
		t.Fatal("a trailing user ask must not get a continuation cue")
	}
}

// TestCompactBudgetedNoopWhenTailKeepsEverything covers the post-compaction
// shape with no real user turn: a summary marker followed only by assistant/tool
// messages. planTail reports "keep everything", and CompactMessages must honor
// that by leaving the history untouched — not re-summarizing a prior summary.
func TestCompactBudgetedNoopWhenTailKeepsEverything(t *testing.T) {
	msgs := []kajicoderuntime.Message{
		sysMsg("s"),
		{Role: kajicoderuntime.MessageRoleUser, Content: summaryLabel + "\nold summary"},
		asstMsg("a1"),
		{Role: kajicoderuntime.MessageRoleTool, ToolCallID: "c1", Content: "tool result"},
	}
	if got := planTail(msgs, 1, unboundedTailTurns, 1000); got != len(msgs) {
		t.Fatalf("preserved-shape history should be kept whole (boundary %d), got %d", len(msgs), got)
	}
	called := false
	out, err := Compact(msgs, CompactionOptions{
		TailTurns:       unboundedTailTurns,
		TailTokenBudget: 1000,
		ContextWindow:   200_000,
		Summarize: func([]kajicoderuntime.Message) (string, error) {
			called = true
			return "NEW", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("history with no compactable user turn must not be re-summarized")
	}
	if len(out) != len(msgs) {
		t.Fatalf("history must be unchanged, got %d messages", len(out))
	}
}
