package mcp

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

// hungServerClient builds a Client that behaves like a server that received
// the request but never replies: a drainer consumes stdin bytes (so the write
// completes), while no reader goroutine ever dispatches a response. The call
// then waits on the response channel until its deadline fires.
func hungServerClient() *Client {
	pipeReader, pipeWriter := net.Pipe()
	go func() { // drain stdin like a server process that never answers
		_, _ = io.Copy(io.Discard, pipeReader)
	}()
	client := &Client{
		server:  Server{Name: "hung"},
		pending: make(map[int]chan dispatchResult),
		stdin:   nopWriteCloser{pipeWriter},
		reader:  newMessageReader(bufio.NewReader(pipeReader)),
		writer:  newMessageWriter(bufio.NewWriter(pipeWriter)),
	}
	client.readerOnce.Do(func() {}) // pre-mark started: no readLoop on this synthetic pipe
	return client
}

type nopWriteCloser struct{ io.WriteCloser }

func (nopWriteCloser) Close() error { return nil }

// TestCallToolDeadlineFires pins the hung-server fix: when the caller's ctx has
// no deadline, CallTool must fail after defaultToolCallTimeout instead of
// blocking the agent turn forever.
func TestCallToolDeadlineFires(t *testing.T) {
	old := defaultToolCallTimeout
	defaultToolCallTimeout = 50 * time.Millisecond
	defer func() { defaultToolCallTimeout = old }()

	// A client whose server never answers: writes go into a pipe nobody
	// drains, so only the deadline can unblock the call.
	client := hungServerClient()
	started := time.Now()
	_, err := client.CallTool(context.Background(), "tool", map[string]any{})
	elapsed := time.Since(started)

	if err == nil {
		t.Fatal("expected deadline error from hung server")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("deadline took %v; watchdog did not bound the call", elapsed)
	}
}

// TestCallToolCallerDeadlineWins verifies an existing ctx deadline is never
// shortened or extended — CallTool defers to it.
func TestCallToolCallerDeadlineWins(t *testing.T) {
	old := defaultToolCallTimeout
	defaultToolCallTimeout = time.Hour
	defer func() { defaultToolCallTimeout = old }()

	client := hungServerClient()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()

	started := time.Now()
	_, err := client.CallTool(ctx, "tool", map[string]any{})
	elapsed := time.Since(started)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed >= defaultToolCallTimeout {
		t.Fatalf("caller's 40ms deadline was ignored (took %v)", elapsed)
	}
}

// TestCallToolCancelledContextFailsFast keeps cancellation semantics intact.
func TestCallToolCancelledContextFailsFast(t *testing.T) {
	client := hungServerClient()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.CallTool(ctx, "tool", map[string]any{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}
