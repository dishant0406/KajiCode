package agent

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/sessions"
)

var markerPattern = regexp.MustCompile(`msg-\d+`)

// validSummary builds a summary shaped like the real summarizer output (the
// strict template from summaryTemplate) carrying the given marker text. Tests use
// it so their stub summarizers satisfy validCompactionSummary the way a real
// summarizer does, instead of returning prose that production would reject.
func validSummary(marker string) string {
	return "## Objective\n" + marker + "\n\n## Important Details\n- none\n\n## Work State\n### Completed\n- none\n### Active\n- none\n### Blocked\n- none\n\n## Next Move\ncontinue\n\n## Relevant Files\n- none"
}

// sizeLimitedSummarizer returns a context-limit error when the rendered
// transcript carries more than maxMarkers messages, and otherwise "summarizes"
// by echoing the message markers it saw — so a successful summary records exactly
// which messages it covered.
type sizeLimitedSummarizer struct {
	maxMarkers int
	calls      int32
}

func (p *sizeLimitedSummarizer) StreamCompletion(_ context.Context, request kajicoderuntime.CompletionRequest) (<-chan kajicoderuntime.StreamEvent, error) {
	atomic.AddInt32(&p.calls, 1)
	text := request.Messages[len(request.Messages)-1].Content
	markers := markerPattern.FindAllString(text, -1)
	ch := make(chan kajicoderuntime.StreamEvent, 2)
	if len(markers) > p.maxMarkers {
		ch <- kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventError, Error: "context length exceeded"}
		close(ch)
		return ch, nil
	}
	ch <- kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventText, Content: strings.Join(markers, " ")}
	ch <- kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventDone}
	close(ch)
	return ch, nil
}

type errorSummarizer struct {
	message string
	calls   int32
}

func (p *errorSummarizer) StreamCompletion(_ context.Context, _ kajicoderuntime.CompletionRequest) (<-chan kajicoderuntime.StreamEvent, error) {
	atomic.AddInt32(&p.calls, 1)
	ch := make(chan kajicoderuntime.StreamEvent, 1)
	ch <- kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventError, Error: p.message}
	close(ch)
	return ch, nil
}

// compressingSummarizer fails on more than maxMarkers messages but returns a
// SHORT marker-free summary, so two partial summaries combine into something that
// fits — modelling real summarization that shrinks its input.
type compressingSummarizer struct {
	maxMarkers int
	calls      int32
}

func (p *compressingSummarizer) StreamCompletion(_ context.Context, request kajicoderuntime.CompletionRequest) (<-chan kajicoderuntime.StreamEvent, error) {
	atomic.AddInt32(&p.calls, 1)
	text := request.Messages[len(request.Messages)-1].Content
	ch := make(chan kajicoderuntime.StreamEvent, 2)
	if len(markerPattern.FindAllString(text, -1)) > p.maxMarkers {
		ch <- kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventError, Error: "context length exceeded"}
		close(ch)
		return ch, nil
	}
	ch <- kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventText, Content: "S"}
	ch <- kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventDone}
	close(ch)
	return ch, nil
}

func TestSummarizeWithFallbackReSummarizesPartialsIntoOne(t *testing.T) {
	messages := make([]kajicoderuntime.Message, 4)
	for i := range messages {
		messages[i] = kajicoderuntime.Message{Role: kajicoderuntime.MessageRoleUser, Content: fmt.Sprintf("msg-%d body", i)}
	}
	provider := &compressingSummarizer{maxMarkers: 2}

	summary, err := summarizeWithFallback(context.Background(), provider, messages, nil)
	if err != nil {
		t.Fatalf("summarizeWithFallback failed: %v", err)
	}
	// The two chunk summaries ("S" / "S") are re-summarized into ONE unit, not
	// returned as the joined "S\n\nS" blob — so a later compaction can shrink it.
	if strings.Contains(summary, "\n\n") {
		t.Fatalf("expected a single re-summarized result, got a joined blob: %q", summary)
	}
	if summary != "S" {
		t.Fatalf("summary = %q, want the reduced %q", summary, "S")
	}
}

func TestSummarizeWithFallbackChunksOnContextLimit(t *testing.T) {
	const n = 8
	messages := make([]kajicoderuntime.Message, n)
	for i := range messages {
		messages[i] = kajicoderuntime.Message{Role: kajicoderuntime.MessageRoleUser, Content: fmt.Sprintf("msg-%d some content", i)}
	}
	// The summarizer can only handle 2 messages per call, so the 8-message slice
	// must be split recursively until each chunk fits.
	provider := &sizeLimitedSummarizer{maxMarkers: 2}

	summary, err := summarizeWithFallback(context.Background(), provider, messages, nil)
	if err != nil {
		t.Fatalf("summarizeWithFallback failed: %v", err)
	}
	for i := 0; i < n; i++ {
		if !strings.Contains(summary, fmt.Sprintf("msg-%d", i)) {
			t.Fatalf("combined summary missing msg-%d: %q", i, summary)
		}
	}
	if got := atomic.LoadInt32(&provider.calls); got < 2 {
		t.Fatalf("expected multiple calls from splitting, got %d", got)
	}
}

func TestSummarizeWithFallbackPropagatesNonContextErrors(t *testing.T) {
	provider := &errorSummarizer{message: "auth error: invalid key"}
	_, err := summarizeWithFallback(context.Background(), provider, []kajicoderuntime.Message{
		{Role: kajicoderuntime.MessageRoleUser, Content: "msg-0"},
		{Role: kajicoderuntime.MessageRoleUser, Content: "msg-1"},
	}, nil)
	if err == nil {
		t.Fatal("expected a non-context-limit error to propagate")
	}
	if got := atomic.LoadInt32(&provider.calls); got != 1 {
		t.Fatalf("a non-context error must not trigger splitting/retry, calls=%d", got)
	}
}

func TestSummarizeWithFallbackSingleMessageContextLimitSurfaces(t *testing.T) {
	// A single message that still won't fit can't be split further → error surfaces.
	provider := &sizeLimitedSummarizer{maxMarkers: 0}
	_, err := summarizeWithFallback(context.Background(), provider, []kajicoderuntime.Message{
		{Role: kajicoderuntime.MessageRoleUser, Content: "msg-0 too big"},
	}, nil)
	if err == nil {
		t.Fatal("expected the context-limit error to surface for an unsplittable single message")
	}
}

// usageReportingSummarizer emits a usage event so a test can assert the
// summarizer's token cost is forwarded to OnUsage.
type usageReportingSummarizer struct{}

func (usageReportingSummarizer) StreamCompletion(_ context.Context, _ kajicoderuntime.CompletionRequest) (<-chan kajicoderuntime.StreamEvent, error) {
	ch := make(chan kajicoderuntime.StreamEvent, 3)
	ch <- kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventText, Content: "summary"}
	ch <- kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventUsage, Usage: kajicoderuntime.Usage{PromptTokens: 100, CompletionTokens: 20}}
	ch <- kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventDone}
	return ch, nil
}

func TestSummarizeForwardsUsageButNotText(t *testing.T) {
	// Compaction must stay invisible to the user (no OnText), but its token cost
	// MUST be counted, so OnUsage has to fire for the summarizer call.
	var got kajicoderuntime.Usage
	var calls int
	summary, err := summarizeWithFallback(context.Background(), usageReportingSummarizer{}, []kajicoderuntime.Message{
		{Role: kajicoderuntime.MessageRoleUser, Content: "hello"},
	}, func(u kajicoderuntime.Usage) { calls++; got = u })
	if err != nil {
		t.Fatalf("summarize failed: %v", err)
	}
	if summary != "summary" {
		t.Fatalf("unexpected summary: %q", summary)
	}
	if calls != 1 {
		t.Fatalf("expected OnUsage to fire once, got %d", calls)
	}
	if got.PromptTokens != 100 || got.CompletionTokens != 20 {
		t.Fatalf("unexpected forwarded usage: %#v", got)
	}
}

// --- Summary validation ---------------------------------------------------

func TestValidCompactionSummaryAcceptsRealSummaries(t *testing.T) {
	valid := []string{
		validSummary("objective text"),
		// The shortest real summary observed in the local store.
		"## Objective\nSimplify the store sync code.\n\n## Next Move\nContinue the fix loop.",
		// Leading whitespace and a stray tag before the outline are tolerated.
		"<summary>\n\n## Objective\nGoal\n\n## Next Move\nAct.",
	}
	for _, summary := range valid {
		if !validCompactionSummary(summary) {
			t.Fatalf("expected a valid summary to be accepted: %q", summary)
		}
	}
}

func TestValidCompactionSummaryRejectsDegenerateOutput(t *testing.T) {
	cases := map[string]string{
		"empty":                     "",
		"whitespace only":           "   \n\t  ",
		"lazy one-liner":            "Now the registry:",
		"deliberation, no headings": "Let me write. Let me run. Let me go. OK.",
		"prose preamble then outline": strings.Repeat("Let me read the file. ", 40) +
			"\n\n## Objective\nGoal\n\n## Next Move\nAct.",
		"outline without Next Move": "## Objective\nGoal\n\n## Relevant Files\n- a.go",
		"missing Objective heading": "## Important Details\n- x\n\n## Next Move\nAct.",
		"transcript echo":           strings.Repeat("tool result: 5190:export type Foo = {\n", 3000),
	}
	for name, summary := range cases {
		if validCompactionSummary(summary) {
			t.Fatalf("expected %s to be rejected", name)
		}
	}
}

func TestValidCompactionSummaryRejectsOversizedAndRepeated(t *testing.T) {
	// A templated summary that is far too long is a transcript echo, not a summary.
	huge := "## Objective\n" + strings.Repeat("word ", 8000) + "\n\n## Next Move\nact"
	if len(huge) <= sessions.MaxCompactionSummaryBytes {
		t.Fatalf("fixture too small to exercise the cap: %d bytes", len(huge))
	}
	if validCompactionSummary(huge) {
		t.Fatal("expected an oversized summary to be rejected")
	}

	// A long run of byte-identical lines is a re-emitted transcript.
	repeated := "## Objective\n" + strings.Repeat("5190:export type Foo = {\n", sessions.MaxCompactionSummaryRepeatRun+5) + "\n## Next Move\nact"
	if validCompactionSummary(repeated) {
		t.Fatal("expected a repeated-line transcript echo to be rejected")
	}

	// Just under the repetition bound is still fine.
	ok := "## Objective\n" + strings.Repeat("same line\n", sessions.MaxCompactionSummaryRepeatRun-1) + "\n## Next Move\nact"
	if !validCompactionSummary(ok) {
		t.Fatal("expected a summary under the repetition bound to be accepted")
	}
}

// TestLongestIdenticalLineRun covers the shared structural helper in
// internal/sessions, which both compaction paths rely on.
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
		{"a\n\n\na", 2},        // interleaved blank lines are skipped
	}
	for _, c := range cases {
		if got := sessions.LongestIdenticalLineRun(c.text); got != c.want {
			t.Fatalf("LongestIdenticalLineRun(%q) = %d, want %d", c.text, got, c.want)
		}
	}
}

// summarizerTextProvider returns a fixed summary text, so a test can drive
// CompactMessages with exactly the output a misbehaving model would produce.
type summarizerTextProvider struct{ text string }

func (p summarizerTextProvider) StreamCompletion(_ context.Context, _ kajicoderuntime.CompletionRequest) (<-chan kajicoderuntime.StreamEvent, error) {
	ch := make(chan kajicoderuntime.StreamEvent, 2)
	ch <- kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventText, Content: p.text}
	ch <- kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventDone}
	close(ch)
	return ch, nil
}

// End to end: a summarizer that echoes the transcript must NOT have its output
// accepted as the summary. CompactMessages fails, so the caller takes its
// deterministic fallback instead of injecting garbage.
func TestCompactMessagesRejectsTranscriptEchoFromSummarizer(t *testing.T) {
	messages := []kajicoderuntime.Message{
		{Role: kajicoderuntime.MessageRoleSystem, Content: "system"},
		{Role: kajicoderuntime.MessageRoleUser, Content: strings.Repeat("question ", 300)},
		{Role: kajicoderuntime.MessageRoleAssistant, Content: strings.Repeat("answer ", 300)},
		{Role: kajicoderuntime.MessageRoleUser, Content: "latest"},
	}
	// The summarizer "summarizes" by re-emitting the transcript it was handed.
	provider := summarizerTextProvider{text: renderTranscript(messages)}
	_, err := CompactMessages(messages, CompactionOptions{
		PreserveLast: 1,
		Summarize:    summarizeClosure(context.Background(), provider, nil),
	})
	if err == nil {
		t.Fatal("expected a transcript-echo summary to be rejected")
	}
	if !strings.Contains(err.Error(), "unusable summary") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// End to end: a legitimate templated summary is still accepted verbatim.
func TestCompactMessagesAcceptsTemplatedSummaryFromSummarizer(t *testing.T) {
	messages := []kajicoderuntime.Message{
		{Role: kajicoderuntime.MessageRoleSystem, Content: "system"},
		{Role: kajicoderuntime.MessageRoleUser, Content: strings.Repeat("question ", 300)},
		{Role: kajicoderuntime.MessageRoleAssistant, Content: strings.Repeat("answer ", 300)},
		{Role: kajicoderuntime.MessageRoleUser, Content: "latest"},
	}
	provider := summarizerTextProvider{text: validSummary("real work happened")}
	result, err := CompactMessages(messages, CompactionOptions{
		PreserveLast: 1,
		Summarize:    summarizeClosure(context.Background(), provider, nil),
	})
	if err != nil {
		t.Fatalf("a templated summary must be accepted: %v", err)
	}
	if !result.Compacted {
		t.Fatal("expected compaction to be reported")
	}
	if !strings.Contains(result.SummaryText, "real work happened") {
		t.Fatalf("unexpected SummaryText: %q", result.SummaryText)
	}
}
