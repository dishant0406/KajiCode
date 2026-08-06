package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestTranscriptWheelCoalescerBatchesTranscriptWheel(t *testing.T) {
	rec := &recorder{}
	c := newTranscriptWheelCoalescer(rec.forward)
	c.afterFunc = func(func()) coalesceTimer { return &manualTimer{} }
	m := wheelCoalescerTestModel()

	if got := c.filter(m, testMouseWheel(tea.MouseWheelUp, 2, 2)); got != nil {
		t.Fatalf("transcript wheel should be buffered, got %#v", got)
	}
	if got := c.filter(m, testMouseWheel(tea.MouseWheelUp, 2, 2)); got != nil {
		t.Fatalf("transcript wheel should be buffered, got %#v", got)
	}
	if got := rec.snapshot(); len(got) != 0 {
		t.Fatalf("wheel should not flush before frame timer, got %#v", got)
	}

	c.flush()

	msg := singleChatWheelScrollMsg(t, rec.snapshot())
	if msg.delta != chatWheelScrollLines*2 || msg.x != 2 || msg.y != 2 {
		t.Fatalf("coalesced wheel = %#v, want two upward wheel steps at 2,2", msg)
	}
}

func TestTranscriptWheelCoalescerCapsBurstPerFrame(t *testing.T) {
	rec := &recorder{}
	c := newTranscriptWheelCoalescer(rec.forward)
	c.afterFunc = func(func()) coalesceTimer { return &manualTimer{} }
	m := wheelCoalescerTestModel()

	for range 1000 {
		if got := c.filter(m, testMouseWheel(tea.MouseWheelUp, 2, 2)); got != nil {
			t.Fatalf("transcript wheel should be buffered, got %#v", got)
		}
	}
	c.flush()

	msg := singleChatWheelScrollMsg(t, rec.snapshot())
	if msg.delta != transcriptWheelMaxLinesPerFrame {
		t.Fatalf("coalesced burst delta = %d, want cap %d", msg.delta, transcriptWheelMaxLinesPerFrame)
	}
}

func TestTranscriptWheelCoalescerReversalReplacesPendingDirection(t *testing.T) {
	rec := &recorder{}
	c := newTranscriptWheelCoalescer(rec.forward)
	c.afterFunc = func(func()) coalesceTimer { return &manualTimer{} }
	m := wheelCoalescerTestModel()

	_ = c.filter(m, testMouseWheel(tea.MouseWheelUp, 2, 2))
	_ = c.filter(m, testMouseWheel(tea.MouseWheelUp, 2, 2))
	_ = c.filter(m, testMouseWheel(tea.MouseWheelDown, 2, 2))
	c.flush()

	msg := singleChatWheelScrollMsg(t, rec.snapshot())
	if msg.delta != -chatWheelScrollLines {
		t.Fatalf("coalesced reversal delta = %d, want latest downward step %d", msg.delta, -chatWheelScrollLines)
	}
}

func TestTranscriptWheelCoalescerCancelsPendingOnKeyboard(t *testing.T) {
	rec := &recorder{}
	c := newTranscriptWheelCoalescer(rec.forward)
	c.afterFunc = func(func()) coalesceTimer { return &manualTimer{} }
	m := wheelCoalescerTestModel()

	_ = c.filter(m, testMouseWheel(tea.MouseWheelUp, 2, 2))
	key := testKeyText("x")
	if got := c.filter(m, key); got != key {
		t.Fatalf("keyboard message = %#v, want original %#v", got, key)
	}
	c.flush()

	if got := rec.snapshot(); len(got) != 0 {
		t.Fatalf("keyboard should cancel pending wheel burst, got %#v", got)
	}
}

func TestTranscriptWheelCoalescerPassesComposerWheelThrough(t *testing.T) {
	rec := &recorder{}
	c := newTranscriptWheelCoalescer(rec.forward)
	c.afterFunc = func(func()) coalesceTimer { return &manualTimer{} }
	m := wheelCoalescerTestModel()
	m.width = 44
	m.height = 20
	m.input.SetValue("Create a book library dashboard page with cards, filters, charts, and responsive behavior.")
	m.input.CursorEnd()
	x, y := composerMousePoint(t, m, 0)
	wheel := testMouseWheel(tea.MouseWheelUp, x, y)

	if got := c.filter(m, wheel); got != wheel {
		t.Fatalf("composer wheel should pass through, got %#v", got)
	}
	c.flush()
	if got := rec.snapshot(); len(got) != 0 {
		t.Fatalf("pass-through composer wheel should not enqueue chat scroll, got %#v", got)
	}
}

func TestChatWheelScrollMsgScrollsTranscript(t *testing.T) {
	m := wheelCoalescerTestModel()

	updated, cmd := m.Update(chatWheelScrollMsg{delta: chatWheelScrollLines * 3, x: 2, y: 2})
	m = updated.(model)

	if cmd != nil {
		t.Fatal("coalesced chat wheel should not schedule commands for loaded history")
	}
	if want := chatWheelScrollLines * 3; m.chatScrollOffset != want {
		t.Fatalf("chatScrollOffset = %d, want %d", m.chatScrollOffset, want)
	}
}

func wheelCoalescerTestModel() model {
	m := newModel(context.Background(), Options{AltScreen: true})
	m.width = 90
	m.height = 14
	m.mouseCapture = true
	for index := 0; index < 40; index++ {
		m.transcript = appendRow(m.transcript, rowAssistant, "message "+string(rune('A'+index%26)))
	}
	return m
}

func singleChatWheelScrollMsg(t *testing.T, msgs []tea.Msg) chatWheelScrollMsg {
	t.Helper()
	if len(msgs) != 1 {
		t.Fatalf("messages = %#v, want one chatWheelScrollMsg", msgs)
	}
	msg, ok := msgs[0].(chatWheelScrollMsg)
	if !ok {
		t.Fatalf("message = %#v, want chatWheelScrollMsg", msgs[0])
	}
	return msg
}
