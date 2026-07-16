package trace

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/agenteval"
)

func TestRecorderSpanAccumulates(t *testing.T) {
	r := NewRecorder("s1", "r1", "")
	r.Start()
	s := r.Span(SpanGeneration)
	time.Sleep(2 * time.Millisecond)
	s.End()
	s2 := r.Span(SpanGeneration)
	time.Sleep(1 * time.Millisecond)
	s2.End()

	tr := r.Finish()
	got := tr.Span(SpanGeneration)
	if got < 3*time.Millisecond {
		t.Fatalf("generation span did not accumulate across stamps; got %v", got)
	}
	if len(tr.Spans) != 1 {
		t.Fatalf("expected one generation span entry, got %d", len(tr.Spans))
	}
}

func TestRecorderCounters(t *testing.T) {
	r := NewRecorder("s1", "r1", "")
	r.Counter(CounterToolCalls, 1)
	r.Counter(CounterToolCalls, 2)
	r.Counter(CounterModelRequests, 3)

	tr := r.Finish()
	if got := tr.Counter(CounterToolCalls); got != 3 {
		t.Fatalf("tool_calls = %d, want 3", got)
	}
	if got := tr.Counter(CounterModelRequests); got != 3 {
		t.Fatalf("model_requests = %d, want 3", got)
	}
}

func TestRecorderFirstTokenOnce(t *testing.T) {
	r := NewRecorder("s", "r", "")
	r.Start()
	r.StampFirstToken()
	first := r.Finish().FirstTokenAt
	r.StampFirstToken() // no-op after Finish; should not panic
	if r.Finish().FirstTokenAt != first {
		t.Fatal("StampFirstToken should not move the timestamp after the first stamp")
	}
}

func TestFinishSnapshotIsCopy(t *testing.T) {
	r := NewRecorder("s", "r", "")
	r.Counter(CounterToolCalls, 5)
	tr := r.Finish()
	tr.Counters[0].Value = 999
	if got := r.Finish().Counter(CounterToolCalls); got != 5 {
		t.Fatalf("Finish snapshot must be a copy; mutating it changed recorder state to %d", got)
	}
}

func TestNilRecorderIsNoOp(t *testing.T) {
	var r *Recorder
	r.Start()
	r.Counter(CounterToolCalls, 1)
	r.StampFirstToken()
	r.StampFirstVisibleEvent()
	r.StampFirstUsefulAction()
	r.RecordSpan(SpanGeneration, time.Millisecond)
	s := r.Span(SpanGeneration)
	s.End()
	if tr := r.Finish(); tr != nil {
		t.Fatalf("nil recorder Finish should return nil, got %+v", tr)
	}
}

func TestRecorderConcurrent(t *testing.T) {
	r := NewRecorder("s", "r", "")
	r.Start()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := r.Span(SpanToolExecution)
			time.Sleep(time.Millisecond)
			s.End()
			r.Counter(CounterToolCalls, 1)
			r.StampFirstToken()
		}()
	}
	wg.Wait()
	tr := r.Finish()
	if got := tr.Counter(CounterToolCalls); got != 50 {
		t.Fatalf("tool_calls = %d, want 50", got)
	}
	if got := tr.Span(SpanToolExecution); got <= 0 {
		t.Fatalf("tool_execution span empty after concurrent stamps; got %v", got)
	}
}

func TestContextRoundTrip(t *testing.T) {
	r := NewRecorder("s", "r", "")
	ctx := WithContext(context.Background(), r)
	if got := FromContext(ctx); got != r {
		t.Fatal("FromContext did not return the injected recorder")
	}
	if got := FromContext(context.Background()); got != nil {
		t.Fatalf("FromContext on a bare context should return nil, got %v", got)
	}
}

func TestContextNilRecorder(t *testing.T) {
	ctx := WithContext(context.Background(), nil)
	if got := FromContext(ctx); got != nil {
		t.Fatalf("FromContext should return nil for an injected nil recorder, got %v", got)
	}
}

func TestWriteNDJSONMatchesAgentevalContract(t *testing.T) {
	r := NewRecorder("s1", "r1", "cold")
	r.Start()
	r.RecordSpan(SpanPromptBuild, 10*time.Millisecond)
	r.RecordSpan(SpanGeneration, 50*time.Millisecond)
	r.RecordSpan(SpanToolExecution, 5*time.Millisecond)
	r.RecordSpan(SpanPermissionWait, 1*time.Millisecond)
	r.RecordSpan(SpanCompaction, 2*time.Millisecond)
	r.RecordSpan(SpanPersistence, 1*time.Millisecond)
	r.RecordSpan(SpanProviderConnect, 8*time.Millisecond)
	r.Counter(CounterModelRequests, 2)
	r.Counter(CounterToolCalls, 3)
	r.Counter(CounterInputTokens, 100)
	r.Counter(CounterOutputTokens, 40)
	tr := r.Finish()

	var buf bytes.Buffer
	if err := WriteNDJSON(&buf, tr); err != nil {
		t.Fatalf("WriteNDJSON: %v", err)
	}
	stdout := buf.String()

	missing := agenteval.MissingTraceEvents(RequiredEventKeys(), stdout)
	if len(missing) > 0 {
		t.Fatalf("NDJSON missing required event keys: %v\noutput:\n%s", missing, stdout)
	}

	keys := agenteval.ParseTraceEventKeys(stdout)
	want := map[string]bool{
		"trace:run":                       true,
		"span:" + SpanPromptBuild:         true,
		"span:" + SpanGeneration:          true,
		"span:" + SpanToolExecution:       true,
		"span:" + SpanProviderConnect:     true,
		"counter:" + CounterModelRequests: true,
		"counter:" + CounterToolCalls:     true,
		"counter:" + CounterInputTokens:   true,
		"counter:" + CounterOutputTokens:  true,
	}
	for k := range want {
		if !contains(keys, k) {
			t.Fatalf("expected key %q in parsed keys %v", k, keys)
		}
	}

	// Each line must be valid JSON.
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("non-JSON NDJSON line: %q (%v)", line, err)
		}
	}
}

func TestWriteTextIsReadable(t *testing.T) {
	r := NewRecorder("s", "r", "")
	r.Start()
	r.RecordSpan(SpanGeneration, 42*time.Millisecond)
	r.Counter(CounterToolCalls, 7)
	tr := r.Finish()
	var buf bytes.Buffer
	if err := WriteText(&buf, tr); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"trace run=", "spans:", "generation", "counters:", "tool_calls"} {
		if !strings.Contains(out, want) {
			t.Fatalf("text trace missing %q:\n%s", want, out)
		}
	}
}

func TestAttributionRatio(t *testing.T) {
	r := NewRecorder("s", "r", "")
	r.Start()
	// Attribute ~all wall time so the ratio is near 1.0.
	r.RecordSpan(SpanGeneration, 10*time.Millisecond)
	r.RecordSpan(SpanToolExecution, 10*time.Millisecond)
	tr := r.Finish()
	// Wall is at least 20ms and attributed is 20ms, so ratio should be <= 1
	// and the run is "well attributed" (>= 0.95 only if wall is close to 20ms;
	// we assert the lower bound instead since wall includes scheduling jitter).
	if ratio := tr.AttributionRatio(); ratio > 1.0 {
		t.Fatalf("attribution ratio %v exceeds 1.0", ratio)
	}
	if tr.AttributedDuration() != 20*time.Millisecond {
		t.Fatalf("attributed = %v, want 20ms", tr.AttributedDuration())
	}
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func TestReadNDJSONRoundTrip(t *testing.T) {
	r := NewRecorder("s1", "r1", "cold")
	r.Start()
	r.RecordSpan(SpanPromptBuild, 10*time.Millisecond)
	r.RecordSpan(SpanGeneration, 50*time.Millisecond)
	r.RecordSpan(SpanGeneration, 5*time.Millisecond) // accumulates to 55ms
	r.Counter(CounterModelRequests, 3)
	r.Counter(CounterToolCalls, 7)
	r.StampFirstToken()
	original := r.Finish()

	var buf bytes.Buffer
	if err := WriteNDJSON(&buf, original); err != nil {
		t.Fatalf("WriteNDJSON: %v", err)
	}
	parsed, err := ReadNDJSON(&buf)
	if err != nil {
		t.Fatalf("ReadNDJSON: %v", err)
	}
	if parsed == nil {
		t.Fatal("ReadNDJSON returned nil")
	}
	if parsed.RunID != original.RunID || parsed.SessionID != original.SessionID || parsed.Profile != original.Profile {
		t.Fatalf("identity mismatch: got %+v want %+v", parsed, original)
	}
	if got := parsed.Span(SpanGeneration); got != 55*time.Millisecond {
		t.Fatalf("generation span after round-trip = %v, want 55ms", got)
	}
	if got := parsed.Counter(CounterModelRequests); got != 3 {
		t.Fatalf("model_requests after round-trip = %d, want 3", got)
	}
	if got := parsed.Counter(CounterToolCalls); got != 7 {
		t.Fatalf("tool_calls after round-trip = %d, want 7", got)
	}
	if parsed.FirstTokenAt.IsZero() {
		t.Fatal("first_token_at lost in round-trip")
	}
}
