package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func BenchmarkScrollWheelLongThread(b *testing.B) {
	for _, tc := range []struct {
		name    string
		pending bool
	}{
		{name: "stable_sidebar", pending: false},
		{name: "pending_sidebar", pending: true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			m := longThreadScrollBenchmarkModel(tc.pending)
			msg := testMouseWheel(tea.MouseWheelUp, 2, 2)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				updated, cmd := m.Update(msg)
				if cmd != nil {
					b.Fatal("wheel scroll should not schedule commands")
				}
				m = updated.(model)
				_ = m.View()
			}
		})
	}
}

func BenchmarkScrollCommandLongThread(b *testing.B) {
	for _, tc := range []struct {
		name    string
		pending bool
	}{
		{name: "stable_sidebar", pending: false},
		{name: "pending_sidebar", pending: true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			m := longThreadScrollBenchmarkModel(tc.pending)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				var cmd tea.Cmd
				m, cmd = m.scrollChatWithCommand(chatWheelScrollLines)
				if cmd != nil {
					b.Fatal("scroll should not schedule commands")
				}
				_ = m.View()
			}
		})
	}
}

func BenchmarkScrollWheelCoalescedBurstLongThread(b *testing.B) {
	m := longThreadScrollBenchmarkModel(true)
	var teaModel tea.Model = m
	wheel := testMouseWheel(tea.MouseWheelUp, 2, 2)
	sent := make([]tea.Msg, 0, 1)
	c := newTranscriptWheelCoalescer(func(msg tea.Msg) {
		sent = append(sent, msg)
	})
	c.afterFunc = func(func()) coalesceTimer { return &manualTimer{} }

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		sent = sent[:0]
		for range 1000 {
			if got := c.filter(teaModel, wheel); got != nil {
				b.Fatal("transcript wheel should be buffered")
			}
		}
		c.flush()
		if len(sent) != 1 {
			b.Fatalf("coalesced messages = %d, want 1", len(sent))
		}
		updated, cmd := m.Update(sent[0])
		if cmd != nil {
			b.Fatal("coalesced scroll should not schedule commands")
		}
		m = updated.(model)
		teaModel = m
		_ = m.View()
	}
}

func longThreadScrollBenchmarkModel(pending bool) model {
	m := newModel(context.Background(), Options{AltScreen: true, ProviderName: "test-provider", ModelName: "test-model"})
	m.width = 160
	m.height = 44
	m.mouseCapture = true
	m.transcript = initialTranscript()
	m.plan.steps = []planStep{{content: "keep sidebar active", status: "in_progress"}}
	text := strings.Repeat("This is markdown with **bold**, [link](https://example.com), and a list item. ", 8)
	for index := 0; index < 1000; index++ {
		m.transcript = appendTranscriptRow(m.transcript, transcriptRow{
			kind:  rowAssistant,
			id:    stringKey(index),
			text:  text,
			final: true,
		})
	}
	if pending {
		m.pending = true
		m.activeRunID = 42
		// A real pending run sets runStartIndex in beginRun; mirror it so the
		// settled-prefix fast path is exercised, like production.
		m.runStartIndex = len(m.transcript)
		m.streamingText = []byte("still working")
		m.streamingTextHasContent = true
		m.streamingTextTail = "still working"
	}
	_ = m.View()
	return m
}

// BenchmarkStreamingTurnLongThread measures one View() per streamed frame during
// a live run whose tail carries an active (animating) tool card — the shape that
// previously re-folded the entire history every frame. It guards the settled-
// prefix fast path: the frozen history must be reused, not re-rendered, so the
// frame cost tracks the live tail rather than the whole thread.
func BenchmarkStreamingTurnLongThread(b *testing.B) {
	for _, rowCount := range []int{1000, 6000} {
		b.Run(stringKey(rowCount), func(b *testing.B) {
			m := longThreadScrollBenchmarkModel(true)
			m.transcript = appendTranscriptRow(m.transcript, transcriptRow{
				kind: rowToolCall, id: "live", tool: "bash", runID: m.activeRunID, detail: "go test ./...",
			})
			_ = m.View()
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				m.streamingText = append(m.streamingText, 'x')
				m.streamingTextTail = "still working"
				_ = m.View()
			}
		})
	}
}
