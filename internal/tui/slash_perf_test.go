package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestSlashAutocompleteKeepsSidebarChatWidth(t *testing.T) {
	m := slashPerfModel(8)
	before := m.chatColumnWidth()

	updated, _ := m.Update(testKeyText("/"))
	m = updated.(model)

	if !m.suggestionsActive() {
		t.Fatal("slash key should open autocomplete")
	}
	if !m.sidebarActive() {
		t.Fatal("autocomplete should not collapse an active sidebar")
	}
	if got := m.chatColumnWidth(); got != before {
		t.Fatalf("chat width changed after slash: %d -> %d", before, got)
	}
}

func BenchmarkFirstSlashLongSidebarTranscript(b *testing.B) {
	for _, rows := range []int{30, 100} {
		b.Run(stringKey(rows), func(b *testing.B) {
			for range b.N {
				b.StopTimer()
				defaultRenderCache.clear()
				m := slashPerfModel(rows)
				_ = m.View()

				b.StartTimer()
				updated, _ := m.Update(testKeyText("/"))
				m = updated.(model)
				_ = m.View()
				b.StopTimer()
			}
		})
	}
}

func slashPerfModel(rows int) model {
	m := mouseTestModel()
	m.width = 160
	m.height = 44
	m.plan.steps = []planStep{{content: "keep sidebar active", status: "in_progress"}}
	m.transcript = initialTranscript()
	text := strings.Repeat("This is markdown with **bold**, [link](https://example.com), and a list item. ", 8)
	for index := range rows {
		m.transcript = appendTranscriptRow(m.transcript, transcriptRow{
			kind:  rowAssistant,
			id:    stringKey(index),
			text:  text,
			final: true,
		})
	}
	m.chatScrollOffset = 5
	m.input.SetValue("")
	m.input.Focus()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	return updated.(model)
}
