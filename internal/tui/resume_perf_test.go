package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/dishant0406/KajiCode/internal/sessions"
)

func BenchmarkResumeLongTranscriptFirstPaint(b *testing.B) {
	for _, rows := range []int{300, 1000} {
		b.Run(stringKey(rows), func(b *testing.B) {
			transcriptRows := resumePerfRows(rows)
			b.ReportAllocs()
			for range b.N {
				b.StopTimer()
				defaultRenderCache.clear()
				m := resumePerfModel()

				b.StartTimer()
				m, cmd := m.applyResumePrepared(resumePreparedMsg{
					seq:     1,
					session: &sessions.Metadata{SessionID: "session-1", Title: "Long thread"},
					rows:    transcriptRows,
				})
				if cmd != nil || m.resumeInFlight {
					b.Fatal("large resume should paint the tail without scheduling hidden work")
				}
				_ = m.View()
				b.StopTimer()
			}
		})
	}
}

func BenchmarkResumeScrollSchedulesHistoryGap(b *testing.B) {
	transcriptRows := resumePerfRows(1000)
	b.ReportAllocs()
	for range b.N {
		b.StopTimer()
		defaultRenderCache.clear()
		m := resumePerfModel()
		m, cmd := m.applyResumePrepared(resumePreparedMsg{
			seq:     1,
			session: &sessions.Metadata{SessionID: "session-1", Title: "Long thread"},
			rows:    transcriptRows,
		})
		if cmd != nil || m.resumeInFlight {
			b.Fatal("large resume should not schedule hidden work")
		}
		_ = m.View()
		m.chatScrollOffset = m.chatMaxScrollOffset()

		b.StartTimer()
		m, cmd = m.scrollChatWithCommand(m.chatPageScrollLines())
		if cmd == nil || !m.resumeHistoryLoading {
			b.Fatal("scroll should schedule async history preparation")
		}
		_ = m.View()
		b.StopTimer()
	}
}

func BenchmarkResumeHistoryPreparedApply(b *testing.B) {
	transcriptRows := resumePerfRows(1000)
	b.ReportAllocs()
	for range b.N {
		b.StopTimer()
		defaultRenderCache.clear()
		m := resumePerfModel()
		m, _ = m.applyResumePrepared(resumePreparedMsg{
			seq:     1,
			session: &sessions.Metadata{SessionID: "session-1", Title: "Long thread"},
			rows:    transcriptRows,
		})
		_ = m.View()
		m.chatScrollOffset = m.chatMaxScrollOffset()
		m, cmd := m.scrollChatWithCommand(m.chatPageScrollLines())
		msg := execCmd(cmd)

		b.StartTimer()
		updated, nextCmd := m.Update(msg)
		m = updated.(model)
		if nextCmd != nil || m.resumeHistoryLoading {
			b.Fatal("prepared page should apply without hidden work")
		}
		_ = m.View()
		b.StopTimer()
	}
}

func resumePerfModel() model {
	m := newModel(context.Background(), Options{})
	m.altScreen = true
	m.width = 160
	m.height = 44
	m.resumeSeq = 1
	m.resumeInFlight = true
	updated, _ := m.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	return updated.(model)
}

func resumePerfRows(rows int) []transcriptRow {
	out := make([]transcriptRow, 0, rows)
	text := strings.Repeat("This is markdown with **bold**, [link](https://example.com), and a list item. ", 8)
	for index := range rows {
		out = appendTranscriptRow(out, transcriptRow{
			kind:  rowAssistant,
			id:    stringKey(index),
			text:  text,
			final: true,
		})
	}
	return out
}
