package tui

import (
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
)

// streamCoalesceInterval is roughly one 60fps frame. Assistant stream deltas that
// arrive within this window are merged into a single typed message, so the render
// rate decouples from the token rate: a fast local model (100+ tok/s) no longer
// forces 100+ full Update→View cycles (each re-parsing the growing markdown) per
// second. Rendering stays smooth regardless of provider speed.
const streamCoalesceInterval = 16 * time.Millisecond

// textCoalescer batches stream deltas before forwarding them to the Bubble Tea
// program. Any OTHER message flushes the pending stream chunk first, so ordering
// between streamed prose/reasoning and tool-call / row / usage messages is
// preserved. The turn's final agentResponseMsg does not pass through here (it is
// a tea.Cmd return, not a sink message), but the model drops deltas whose runID
// is no longer active, so a flush that races just past end-of-turn is harmless.
//
// Sink messages originate from the single agent goroutine and so arrive
// serially; the only concurrent caller is the flush timer. The mutex guards the
// buffer/timer AND is held across the downstream forward, so a timer-fired stream
// flush can never overtake a concurrent non-stream message: whoever holds the lock
// drains and forwards atomically, and the other caller blocks until it is done.
type textCoalescer struct {
	forward func(tea.Msg) // downstream sink (external sink + program.Send)
	// afterFunc schedules fn to run after one frame interval and returns a
	// stoppable timer. Defaults to a real time.AfterFunc(streamCoalesceInterval, …);
	// tests swap in a controllable timer so flush timing is deterministic instead of
	// racing the 16ms wall clock.
	afterFunc func(fn func()) coalesceTimer

	mu    sync.Mutex
	buf   []byte
	runID int
	kind  streamCoalesceKind
	timer coalesceTimer
}

type streamCoalesceKind int

const (
	streamCoalesceNone streamCoalesceKind = iota
	streamCoalesceText
	streamCoalesceReasoning
)

// coalesceTimer is the subset of *time.Timer the coalescer needs. Abstracted
// behind afterFunc so a test can substitute a timer it controls.
type coalesceTimer interface {
	Stop() bool
}

func newTextCoalescer(forward func(tea.Msg)) *textCoalescer {
	return &textCoalescer{
		forward: forward,
		afterFunc: func(fn func()) coalesceTimer {
			return time.AfterFunc(streamCoalesceInterval, fn)
		},
	}
}

// send is the coalescing entry point installed as the RuntimeMessageSink.
func (c *textCoalescer) send(msg tea.Msg) {
	c.mu.Lock()
	defer c.mu.Unlock()

	kind, runID, delta, ok := coalescableStreamMessage(msg)
	if !ok {
		// Non-stream message: flush buffered stream first (preserving order), then
		// forward it — both under the lock so nothing can interleave between them.
		c.drainAndForwardLocked()
		c.forward(msg)
		return
	}

	// A delta for a different run/kind than the one buffered flushes the old chunk
	// before buffering the new one. In practice runs are sequential (the prior run's
	// end already flushed via a non-stream message), so this is belt-and-braces.
	if len(c.buf) > 0 && (runID != c.runID || kind != c.kind) {
		c.drainAndForwardLocked()
	}
	c.runID = runID
	c.kind = kind
	c.buf = append(c.buf, delta...)
	if c.timer == nil {
		c.timer = c.afterFunc(c.flush)
	}
}

// flush forwards any buffered stream chunk as one typed message. Runs on the timer
// goroutine; the lock it takes serializes it against send so its output can't be
// reordered around a concurrent non-stream message.
func (c *textCoalescer) flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.drainAndForwardLocked()
}

// drainAndForwardLocked forwards any buffered stream chunk and stops
// the timer, all while the caller holds c.mu — so a stream flush and any non-stream
// forward are strictly ordered and never interleave. A no-op when nothing is
// buffered. string(c.buf) copies, so reusing the backing array via [:0] is safe.
func (c *textCoalescer) drainAndForwardLocked() {
	if len(c.buf) == 0 {
		return
	}
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	msg := coalescedStreamMessage(c.kind, c.runID, string(c.buf))
	c.buf = c.buf[:0]
	c.kind = streamCoalesceNone
	c.forward(msg)
}

func coalescableStreamMessage(msg tea.Msg) (streamCoalesceKind, int, string, bool) {
	switch typed := msg.(type) {
	case agentTextMsg:
		return streamCoalesceText, typed.runID, typed.delta, true
	case agentReasoningMsg:
		return streamCoalesceReasoning, typed.runID, typed.delta, true
	default:
		return streamCoalesceNone, 0, "", false
	}
}

func coalescedStreamMessage(kind streamCoalesceKind, runID int, delta string) tea.Msg {
	switch kind {
	case streamCoalesceReasoning:
		return agentReasoningMsg{runID: runID, delta: delta}
	default:
		return agentTextMsg{runID: runID, delta: delta}
	}
}
