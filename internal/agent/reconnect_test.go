package agent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/tools"
)

// flakyProvider fails the first failBefore connect attempts with failErr, then
// succeeds with a single-text stream.
type flakyProvider struct {
	calls      int32
	failBefore int32
	failErr    error
}

type transientStreamErrorProvider struct {
	calls      int32
	failBefore int32
	failError  string
	partial    string
}

func (p *transientStreamErrorProvider) StreamCompletion(_ context.Context, _ kajicoderuntime.CompletionRequest) (<-chan kajicoderuntime.StreamEvent, error) {
	n := atomic.AddInt32(&p.calls, 1)
	ch := make(chan kajicoderuntime.StreamEvent, 2)
	if n <= p.failBefore {
		if p.partial != "" {
			ch <- kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventText, Content: p.partial}
		}
		ch <- kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventError, Error: p.failError}
		close(ch)
		return ch, nil
	}
	ch <- kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventText, Content: "recovered"}
	ch <- kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventDone}
	close(ch)
	return ch, nil
}

func TestRunDoesNotRetryTransientStreamErrorAfterText(t *testing.T) {
	defer func(orig time.Duration) { streamReconnectBase = orig }(streamReconnectBase)
	streamReconnectBase = time.Nanosecond

	p := &transientStreamErrorProvider{
		failBefore: 1,
		failError:  "provider stream error: 503 Service Unavailable",
		partial:    "partial answer",
	}
	_, err := Run(context.Background(), "go", p, Options{Registry: tools.NewRegistry()})
	if err == nil {
		t.Fatal("transient stream error after answer text should surface instead of retrying")
	}
	if got := atomic.LoadInt32(&p.calls); got != 1 {
		t.Fatalf("must not retry after answer text, got %d calls", got)
	}
}

func (p *flakyProvider) StreamCompletion(_ context.Context, _ kajicoderuntime.CompletionRequest) (<-chan kajicoderuntime.StreamEvent, error) {
	n := atomic.AddInt32(&p.calls, 1)
	if n <= p.failBefore {
		return nil, p.failErr
	}
	ch := make(chan kajicoderuntime.StreamEvent, 1)
	ch <- kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventDone}
	close(ch)
	return ch, nil
}

func TestStreamWithReconnectRecoversFromTransientDisconnect(t *testing.T) {
	p := &flakyProvider{failBefore: 1, failErr: errors.New("unexpected EOF")}
	stream, err := streamWithReconnect(context.Background(), p, kajicoderuntime.CompletionRequest{}, nil)
	if err != nil {
		t.Fatalf("expected reconnect to recover, got %v", err)
	}
	if stream == nil {
		t.Fatal("expected a live stream after reconnect")
	}
	if got := atomic.LoadInt32(&p.calls); got != 2 {
		t.Fatalf("expected 2 connect attempts (1 fail + 1 success), got %d", got)
	}
}

func TestStreamWithReconnectGivesUpAfterMax(t *testing.T) {
	// Shrink the backoff so exhausting all retries stays fast.
	defer func(orig time.Duration) { streamReconnectBase = orig }(streamReconnectBase)
	streamReconnectBase = time.Nanosecond
	// Always fails with a disconnect error → exhausts retries and returns it.
	p := &flakyProvider{failBefore: 99, failErr: errors.New("connection reset by peer")}
	_, err := streamWithReconnect(context.Background(), p, kajicoderuntime.CompletionRequest{}, nil)
	if err == nil {
		t.Fatal("expected an error after exhausting reconnects")
	}
	// 1 initial + maxStreamReconnects retries.
	if got := atomic.LoadInt32(&p.calls); got != int32(1+maxStreamReconnects) {
		t.Fatalf("expected %d attempts, got %d", 1+maxStreamReconnects, got)
	}
}

func TestStreamWithReconnectDoesNotRetryNonDisconnect(t *testing.T) {
	// A context-limit error is the compactor's job, not the reconnect's — return
	// immediately without retrying.
	p := &flakyProvider{failBefore: 99, failErr: errors.New("context length exceeded")}
	_, err := streamWithReconnect(context.Background(), p, kajicoderuntime.CompletionRequest{}, nil)
	if err == nil {
		t.Fatal("expected the original error")
	}
	if got := atomic.LoadInt32(&p.calls); got != 1 {
		t.Fatalf("context-limit error must not be retried, got %d attempts", got)
	}
}

func TestRunRetriesPreContentTransientStreamErrors(t *testing.T) {
	defer func(orig time.Duration) { streamReconnectBase = orig }(streamReconnectBase)
	streamReconnectBase = time.Nanosecond

	p := &transientStreamErrorProvider{
		failBefore: 2,
		failError:  "provider stream error: 429 Too Many Requests",
	}
	result, err := Run(context.Background(), "go", p, Options{Registry: tools.NewRegistry()})
	if err != nil {
		t.Fatalf("expected transient stream error to recover, got %v", err)
	}
	if result.FinalAnswer != "recovered" {
		t.Fatalf("final answer = %q, want recovered", result.FinalAnswer)
	}
	if got := atomic.LoadInt32(&p.calls); got != 3 {
		t.Fatalf("want 3 calls (2 transient stream errors + success), got %d", got)
	}
}

func TestStreamWithReconnectStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &flakyProvider{failBefore: 99, failErr: errors.New("i/o timeout")}
	_, err := streamWithReconnect(ctx, p, kajicoderuntime.CompletionRequest{}, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	// Cancelled ctx → no retry beyond the first attempt.
	if got := atomic.LoadInt32(&p.calls); got != 1 {
		t.Fatalf("cancelled context must not retry, got %d attempts", got)
	}
}

func TestShouldReconnectClassification(t *testing.T) {
	ctx := context.Background()
	disconnects := []string{
		"unexpected EOF", "connection reset by peer", "broken pipe",
		"i/o timeout", "server closed the connection", "connection refused",
		"429 Too Many Requests", "503 Service Unavailable", "502 Bad Gateway",
		"504 Gateway Timeout", "500 Internal Server Error", "rate limit exceeded",
		"provider stream error: overloaded_error",
		"provider stream error: RESOURCE_EXHAUSTED",
		"provider request error: quota exceeded",
	}
	for _, m := range disconnects {
		if !shouldReconnect(ctx, errors.New(m)) {
			t.Errorf("expected reconnect for %q", m)
		}
	}
	notDisconnects := []string{
		"context length exceeded", "invalid api key", "model not found",
		"400 bad request: unsupported parameter",
		"401 unauthorized", "403 forbidden", "404 model not found",
	}
	for _, m := range notDisconnects {
		if shouldReconnect(ctx, errors.New(m)) {
			t.Errorf("did NOT expect reconnect for %q", m)
		}
	}
}
