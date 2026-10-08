package sessions

import (
	"context"
	"strings"
	"testing"
)

func TestValidCompactionSummaryShapeAcceptsRealSummaries(t *testing.T) {
	valid := []string{
		"## Objective\nFix the parser.\n\n## Next Move\nRun the tests.",
		// The manual path asks for a free-form digest, not a Markdown outline, so a
		// plain prose summary must still be accepted.
		"Compacted earlier session context: 120 event(s) summarized, 8 recent event(s) preserved.\n" +
			"Compacted range: #1 message through #120 tool_result.",
		"one two three four five six seven eight nine ten eleven twelve thirteen fourteen",
	}
	for _, summary := range valid {
		if !ValidCompactionSummaryShape(summary) {
			t.Fatalf("expected a real summary to be accepted: %q", summary)
		}
	}
}

func TestValidCompactionSummaryShapeRejectsDegenerateOutput(t *testing.T) {
	cases := map[string]string{
		"empty":                    "",
		"whitespace only":          "   \n\t  ",
		"transcript echo":          strings.Repeat("tool result: 5190:export type Foo = {\n", 3000),
		"echo with blank lines":    strings.Repeat("5190:export type Foo = {\n\n", 3000),
		"oversized templated echo": "## Objective\n" + strings.Repeat("word ", 8000) + "\n\n## Next Move\nact",
	}
	for name, summary := range cases {
		if ValidCompactionSummaryShape(summary) {
			t.Fatalf("expected %s to be rejected", name)
		}
	}
}

// A lazy one-liner is degenerate for the AGENT path (it demands a template) but
// is a legitimate free-form manual summary, so the shared shape check must accept
// it — the agent path layers its own heading checks on top.
func TestValidCompactionSummaryShapeAcceptsShortFreeFormSummary(t *testing.T) {
	if !ValidCompactionSummaryShape("Now the registry:") {
		t.Fatal("the shared shape check must not police free-form wording")
	}
}

func TestLongestIdenticalLineRun(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		{"", 0},
		{"a\nb\nc", 1},
		{"a\na\na", 3},
		{"a\n\nb\nb", 2},
		{"a\na\n\nb\nb\nb", 3}, // blank lines do not break a run
		{"  a  \na", 2},        // surrounding whitespace is ignored
	}
	for _, c := range cases {
		if got := LongestIdenticalLineRun(c.text); got != c.want {
			t.Fatalf("LongestIdenticalLineRun(%q) = %d, want %d", c.text, got, c.want)
		}
	}
}

// SummarizePlan must reject a summarizer that answers with a transcript echo, so
// the manual "compact now" path cannot record garbage into the session store.
func TestSummarizePlanRejectsTranscriptEcho(t *testing.T) {
	plan := CompactionPlan{SummaryPrompt: "Summarize these events."}
	provider := fakeTitleProvider{text: strings.Repeat("5190:export type Foo = {\n", 3000)}
	if _, err := SummarizePlan(context.Background(), provider, plan); err == nil {
		t.Fatal("expected a transcript-echo summary to be rejected")
	}
}

func TestSummarizePlanRejectsEmptySummary(t *testing.T) {
	plan := CompactionPlan{SummaryPrompt: "Summarize these events."}
	if _, err := SummarizePlan(context.Background(), fakeTitleProvider{text: "  \n "}, plan); err == nil {
		t.Fatal("expected an empty summary to be rejected")
	}
}

func TestSummarizePlanAcceptsRealSummary(t *testing.T) {
	plan := CompactionPlan{SummaryPrompt: "Summarize these events."}
	got, err := SummarizePlan(context.Background(), fakeTitleProvider{text: "Fixed the parser; tests pass next."}, plan)
	if err != nil {
		t.Fatalf("a real summary must be accepted: %v", err)
	}
	if got != "Fixed the parser; tests pass next." {
		t.Fatalf("unexpected summary: %q", got)
	}
}
