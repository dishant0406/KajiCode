package tui

import (
	"context"
	"testing"
)

func TestSubchatStartWithoutStoreReportsError(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.sessionStore = nil
	next, cmd, errMsg := m.startSubchat("s1", "worker · task", 5)
	if errMsg == "" {
		t.Error("startSubchat without a store should return an error message")
	}
	if cmd != nil || next.subchat.active {
		t.Fatal("failed subchat start must not activate or schedule loading")
	}
}

func TestSubchatIgnoresPreparedResultAfterExit(t *testing.T) {
	m := newModel(context.Background(), Options{SessionStore: testSessionStore(t)})
	m.subchat = subchatState{
		active:             true,
		loading:            true,
		loadSeq:            3,
		childSessionID:     "child-1",
		parentScrollOffset: 7,
	}
	m.chatScrollOffset = m.subchat.exit()

	updated, cmd := m.applySubchatPrepared(subchatPreparedMsg{
		seq:            3,
		childSessionID: "child-1",
		rows:           appendRow(nil, rowAssistant, "late result"),
	})
	if cmd != nil || updated.subchat.active || updated.subchat.loading {
		t.Fatal("a late load result must not reopen an exited subchat")
	}
	if updated.chatScrollOffset != 7 {
		t.Fatalf("parent scroll offset = %d, want 7", updated.chatScrollOffset)
	}
}

func TestSubchatLongTranscriptLoadsTailFirstInPages(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.altScreen = true
	m.subchat = subchatState{
		active:         true,
		loading:        true,
		loadSeq:        1,
		childSessionID: "child-1",
	}
	rows := make([]transcriptRow, 0, subchatTranscriptPageRows*10+5)
	for i := 0; i < cap(rows); i++ {
		rows = appendRow(rows, rowAssistant, stringKey(i))
	}

	m, cmd := m.applySubchatPrepared(subchatPreparedMsg{
		seq:            1,
		childSessionID: "child-1",
		rows:           rows,
	})
	if cmd == nil || !m.subchat.loading {
		t.Fatal("large subchat should schedule another bounded page")
	}
	if got := len(m.subchat.childRows); got != subchatTranscriptPageRows {
		t.Fatalf("initial visible rows = %d, want %d", got, subchatTranscriptPageRows)
	}
	if m.subchat.childRows[0].text != stringKey(len(rows)-subchatTranscriptPageRows) {
		t.Fatal("initial page must be the newest transcript tail")
	}

	continuations := 0
	for cmd != nil {
		updated, nextCmd := m.Update(execCmd(cmd))
		m = updated.(model)
		cmd = nextCmd
		continuations++
	}
	if continuations > 4 {
		t.Fatalf("subchat paging used %d continuations, want logarithmic growth", continuations)
	}
	if m.subchat.loading || len(m.subchat.pending) != 0 {
		t.Fatal("all subchat pages should finish materializing")
	}
	if len(m.subchat.childRows) != len(rows) || m.subchat.childRows[0].text != stringKey(0) {
		t.Fatal("paged subchat transcript must preserve complete chronological order")
	}
}

func TestSubchatExitRestoresScrollOffset(t *testing.T) {
	var s subchatState
	// Simulate entering with a saved offset
	s.active = true
	s.childSessionID = "s1"
	s.childSessionTitle = "test"
	s.parentScrollOffset = 42

	offset := s.exit()
	if offset != 42 {
		t.Errorf("exit should return saved offset 42, got %d", offset)
	}
	if s.active {
		t.Error("subchat should be inactive after exit")
	}
	if s.childSessionID != "" {
		t.Error("childSessionID should be cleared after exit")
	}
}

func TestRenderSubchatNavBar(t *testing.T) {
	got := renderSubchatNavBar("worker · fix tests", 80)
	if got == "" {
		t.Fatal("nav bar should not be empty")
	}

	// Empty title should still render
	got2 := renderSubchatNavBar("", 80)
	if got2 == "" {
		t.Fatal("nav bar should not be empty even with no title")
	}
}
