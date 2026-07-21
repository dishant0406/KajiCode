package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/sessions"
)

func TestStartNewSessionResetsState(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.activeSession = sessions.Metadata{SessionID: "sess-old"}
	m.sessionEvents = []sessions.Event{{Type: sessions.EventMessage}}
	m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendUser, text: "hello"})
	// Stage attachments + a queued message that /new must not leak into the new session.
	m.pendingImages = make([]kajicoderuntime.ImageBlock, 1)
	m.pendingImageLabels = []string{"pic.png"}
	m.pendingDocuments = []pendingDocument{{label: "doc.pdf"}}
	m.queuedMessage = "queued"
	// The /retry attachment snapshot is prior-session state too and must not survive.
	m.lastImages = make([]kajicoderuntime.ImageBlock, 1)
	m.lastImageLabels = []string{"pic.png"}
	m.lastDocuments = []pendingDocument{{label: "doc.pdf"}}

	next := m.startNewSession()

	if next.activeSession.SessionID != "" {
		t.Fatalf("expected active session id cleared, got %q", next.activeSession.SessionID)
	}
	if len(next.sessionEvents) != 0 {
		t.Fatalf("expected session events cleared, got %d", len(next.sessionEvents))
	}
	if len(next.transcript) != 1 || next.transcript[0].kind != rowWelcome {
		t.Fatalf("expected transcript reset to the fresh welcome row, got %#v", next.transcript)
	}
	// The transient home notice must keep the previous session recoverable without
	// turning the fresh home into transcript content.
	if !strings.Contains(next.homeNotice, "sess-old") || !strings.Contains(next.homeNotice, "/resume sess-old") {
		t.Fatalf("expected home notice to reference the previous session, got %q", next.homeNotice)
	}
	// Staged attachments and the queued message must not leak into the new session.
	if len(next.pendingImages) != 0 || len(next.pendingImageLabels) != 0 || len(next.pendingDocuments) != 0 || next.queuedMessage != "" {
		t.Fatalf("startNewSession must clear staged input, got images=%d labels=%d docs=%d queued=%q",
			len(next.pendingImages), len(next.pendingImageLabels), len(next.pendingDocuments), next.queuedMessage)
	}
	// The /retry snapshot must not leak the previous session's attachments.
	if len(next.lastImages) != 0 || len(next.lastImageLabels) != 0 || len(next.lastDocuments) != 0 {
		t.Fatalf("startNewSession must clear the retry snapshot, got images=%d labels=%d docs=%d",
			len(next.lastImages), len(next.lastImageLabels), len(next.lastDocuments))
	}
}

func TestNewCommandStartsFreshSession(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.activeSession = sessions.Metadata{SessionID: "sess-old"}
	m.input.SetValue("/new")

	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if next.activeSession.SessionID != "" {
		t.Fatalf("expected /new to clear the active session, got %q", next.activeSession.SessionID)
	}
}

func TestNewCommandRestoresHomeAndFirstPromptLeavesIt(t *testing.T) {
	m := newModel(context.Background(), Options{
		Cwd:          "/workspace/kajicode",
		Version:      "0.0.5",
		ProviderName: "openai",
		ModelName:    "gpt-5.6-sol",
	})
	m.width, m.height = 100, 30
	m.gitBranch = "main"
	m.activeSession = sessions.Metadata{SessionID: "sess-old"}
	m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendUser, text: "old conversation"})
	m.input.SetValue("/new")

	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	next.width, next.height = m.width, m.height
	view := plainRender(t, next.View())

	assertContains(t, view, "KajiCode")
	assertContains(t, view, composerPlaceholder)
	assertContains(t, view, "Shift+Tab mode")
	assertContains(t, view, "/workspace/kajicode:main")
	assertContains(t, view, "v0.0.5")
	assertContains(t, view, "/resume sess-old")
	assertNotContains(t, view, "old conversation")
	if count := strings.Count(view, composerPlaceholder); count != 1 {
		t.Fatalf("fresh home should render one composer, got %d in %q", count, view)
	}
	if strings.Contains(view, "openai/gpt-5.6-sol") {
		t.Fatalf("fresh home should suppress the normal title bar, got %q", view)
	}

	next.input.SetValue("inspect the repo")
	updated, _ = next.Update(testKey(tea.KeyEnter))
	chat := updated.(model)
	chat.width, chat.height = m.width, m.height
	chatView := plainRender(t, chat.View())
	if !transcriptContains(chat.transcript, "inspect the repo") {
		t.Fatalf("first home prompt should enter the transcript, got %#v", chat.transcript)
	}
	assertContains(t, chatView, "openai/gpt-5.6-sol")
	assertNotContains(t, chatView, "/resume sess-old")
	assertNotContains(t, chatView, "Ctrl+X ? commands")
}

func TestNewCommandDoesNotResetDuringRun(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.activeSession = sessions.Metadata{SessionID: "sess-old"}
	m.pending = true
	m.input.SetValue("/new")

	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	// The safety invariant: /new must never strand an in-flight session.
	if next.activeSession.SessionID != "sess-old" {
		t.Fatalf("/new must not reset an in-flight session, got %q", next.activeSession.SessionID)
	}
}
