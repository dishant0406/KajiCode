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
		m.streamingText = []byte("still working")
		m.streamingTextHasContent = true
		m.streamingTextTail = "still working"
	}
	_ = m.View()
	return m
}
