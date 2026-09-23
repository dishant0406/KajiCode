package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/dishant0406/KajiCode/internal/sessions"
)

// The /thread command browses the conversation's user and assistant messages. It
// is a two-step picker: entering a message opens the actions that apply to it.
// An assistant message can be copied; a user message can be copied, edited
// (recalled into the composer), or reverted to (rewind the session and workspace
// files back to just before that message).

// threadMessage is one browsable message: a transcript row reference plus the
// text to show. Only rowUser and rowAssistant rows become messages — tool calls,
// permission prompts, and system notes are skipped so the list stays readable.
type threadMessage struct {
	kind rowKind
	row  int // absolute index into m.transcript
	text string
}

// threadMessages flattens the transcript into the browsable message list, in
// order. The first line of each message is the label; the full text is kept for
// copy/edit/revert.
func (m model) threadMessages() []threadMessage {
	messages := make([]threadMessage, 0, len(m.transcript))
	for i, row := range m.transcript {
		switch row.kind {
		case rowUser, rowAssistant:
			text := strings.TrimSpace(row.text)
			if text == "" {
				continue
			}
			messages = append(messages, threadMessage{kind: row.kind, row: i, text: text})
		}
	}
	return messages
}

// threadMessageLabel is the one-line row shown in the picker: a role tag plus the
// first line of the message, truncated so rows stay scannable.
func threadMessageLabel(message threadMessage) string {
	role := "you"
	if message.kind == rowAssistant {
		role = "agent"
	}
	first := message.text
	if newline := strings.IndexByte(first, '\n'); newline >= 0 {
		first = first[:newline]
	}
	first = strings.TrimSpace(first)
	label := role + ": " + first
	return truncateRunes(label, 80)
}

// newThreadPicker builds the message-list picker. Returns nil when the
// conversation has no user/assistant messages yet.
func (m model) newThreadPicker() *commandPicker {
	messages := m.threadMessages()
	if len(messages) == 0 {
		return nil
	}
	items := make([]pickerItem, 0, len(messages))
	for _, message := range messages {
		items = append(items, pickerItem{
			Label: threadMessageLabel(message),
			Value: fmt.Sprintf("%d:%d", message.kind, message.row),
		})
	}
	return &commandPicker{
		kind:     pickerThread,
		title:    "conversation · ⏎ shows actions",
		items:    items,
		allItems: append([]pickerItem{}, items...),
	}
}

// newThreadActionPicker builds the action list for a chosen message. Assistant
// messages can be copied; user messages can also be edited or reverted to.
func newThreadActionPicker(kind rowKind, row int) *commandPicker {
	items := []pickerItem{
		{Label: "Copy", Value: "copy"},
	}
	if kind == rowUser {
		items = append(items,
			pickerItem{Label: "Edit in composer", Value: "edit"},
			pickerItem{Label: "Revert to here", Value: "revert"},
		)
	}
	return &commandPicker{
		kind:       pickerThreadAction,
		title:      "message actions",
		items:      items,
		allItems:   append([]pickerItem{}, items...),
		threadKind: kind,
		threadRow:  row,
	}
}

// openThreadPicker opens /thread's message list, or reports why it can't.
func (m model) openThreadPicker() (model, tea.Cmd) {
	if m.pending {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{
			kind: actionAppendSystem,
			text: pickerBusyText("/thread"),
		})
		return m, nil
	}
	picker := m.newThreadPicker()
	if picker == nil {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{
			kind: actionAppendSystem,
			text: "Thread\nno messages yet.",
		})
		return m, nil
	}
	m.picker = picker
	return m, nil
}

// applyThreadSelection handles Enter on the message list: opening the action
// picker for the chosen message. It is called from choosePicker before the
// generic picker cases.
func (m model) applyThreadSelection() (tea.Model, tea.Cmd) {
	item, ok := m.picker.current()
	if !ok {
		m.picker = nil
		return m, nil
	}
	kind, row, ok := parseThreadItemValue(item.Value)
	if !ok {
		m.picker = nil
		return m, nil
	}
	m.picker = newThreadActionPicker(kind, row)
	return m, nil
}

// applyThreadAction handles Enter on the action list. It resolves the target
// transcript row from the picker's stored kind/row and runs the action.
func (m model) applyThreadAction() (tea.Model, tea.Cmd) {
	kind := m.picker.threadKind
	row := m.picker.threadRow
	item, ok := m.picker.current()
	if !ok {
		m.picker = nil
		return m, nil
	}
	action := item.Value
	m.picker = nil
	message, ok := m.threadMessageAt(kind, row)
	if !ok {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{
			kind: actionAppendSystem,
			text: "Thread\nthat message is no longer available.",
		})
		return m, nil
	}
	switch action {
	case "copy":
		return m, copyTranscriptSelectionCmd(message.text)
	case "edit":
		m.recallPromptIntoComposer(message.text, m.attachmentsForThreadEdit(message))
		return m, nil
	case "revert":
		next, text := m.revertToMessage(message)
		next.transcript = reduceTranscript(next.transcript, transcriptAction{kind: actionAppendSystem, text: text})
		return next, nil
	default:
		return m, nil
	}
}

// threadMessageAt re-resolves the target message from a stored kind/row pair
// against the CURRENT transcript, so a stale picker can never act on the wrong
// row (e.g. after a rewind shifted indices).
func (m model) threadMessageAt(kind rowKind, row int) (threadMessage, bool) {
	if row < 0 || row >= len(m.transcript) {
		return threadMessage{}, false
	}
	message := m.transcript[row]
	if message.kind != kind || strings.TrimSpace(message.text) == "" {
		return threadMessage{}, false
	}
	return threadMessage{kind: kind, row: row, text: strings.TrimSpace(message.text)}, true
}

// parseThreadItemValue decodes the "kind:row" token stored on a message row.
func parseThreadItemValue(value string) (rowKind, int, bool) {
	separator := strings.IndexByte(value, ':')
	if separator < 0 {
		return 0, 0, false
	}
	kindValue, err := strconv.Atoi(value[:separator])
	if err != nil {
		return 0, 0, false
	}
	kind := rowKind(kindValue)
	if kind != rowUser && kind != rowAssistant {
		return 0, 0, false
	}
	row, err := strconv.Atoi(value[separator+1:])
	if err != nil || row < 0 {
		return 0, 0, false
	}
	return kind, row, true
}

// recallPromptIntoComposer restores a message's text (and any attachments) into
// the composer for editing, mirroring the /thread edit action. attachments is the
// staged attachments for the message's turn (nil when unknown); it is re-staged so
// an edited resend keeps its image/document context instead of silently degrading
// to text-only.
func (m *model) recallPromptIntoComposer(text string, attachments []stagedAttachment) {
	m.pendingAttachments = attachments
	m.setComposerState(composerState{text: text, cursor: len([]rune(text))})
	m.rebuildAttachmentTokensFromText()
	m.homeNotice = "Recalled into the composer — edit and press Enter to resend."
}

// attachmentsForThreadEdit returns the staged attachments to re-stage when editing
// a thread message. Only the most recent submitted prompt's attachments are
// remembered (m.lastAttachments), so those are returned when the edited message is
// the latest USER message and nil for older ones — editing an older message
// resends it as text, which is the best we can reproduce.
func (m model) attachmentsForThreadEdit(message threadMessage) []stagedAttachment {
	if message.kind != rowUser {
		return nil
	}
	lastUserRow := -1
	for _, candidate := range m.threadMessages() {
		if candidate.kind == rowUser {
			lastUserRow = candidate.row
		}
	}
	if lastUserRow == message.row {
		return m.lastAttachments
	}
	return nil
}

// revertToMessage rewinds the session and workspace files to just before the
// chosen user message, then reloads the in-memory session state (mirroring the
// earlier rewind path). It returns the updated model and a status message. A message
// with no recorded session sequence cannot be reverted — the caller is told why.
func (m model) revertToMessage(message threadMessage) (model, string) {
	if m.sessionStore == nil || m.activeSession.SessionID == "" {
		return m, "Thread\nno active session to revert."
	}
	if m.pending {
		return m, "Thread\ncannot revert while a run is in progress."
	}
	// A cancelled run's late flush hasn't appended its checkpoint events yet:
	// ApplyRewind would prune those checkpoint blobs as unreferenced, then the
	// flush would re-append pre-revert events after the marker.
	if len(m.flushRunIDs) > 0 {
		return m, "Thread\ncannot revert while a cancelled run is still flushing — retry in a moment."
	}
	// The row carries the sequence of its own user event; rewinding to seq-1 keeps
	// everything BEFORE that message and drops it and all that followed.
	if message.row < 0 || message.row >= len(m.transcript) || m.transcript[message.row].kind != rowUser {
		return m, "Thread\nthat message is no longer available."
	}
	row := m.transcript[message.row]
	if row.seq <= 0 {
		return m, "Thread\nthat message has no recorded position to revert to. /resume the session and try again."
	}
	target := row.seq - 1
	report, err := m.sessionStore.ApplyRewind(m.activeSession.SessionID, m.cwd, target)
	if err != nil {
		return m, "Thread\n" + err.Error()
	}
	// ApplyRewind truncated the persisted log, restored files, and appended a
	// marker — reload the in-memory state so dropped events don't linger in the
	// transcript or reach the next prompt as context.
	if meta, getErr := m.sessionStore.Get(m.activeSession.SessionID); getErr == nil {
		m.activeSession = *meta
	}
	events, readErr := m.sessionStore.ReadEvents(m.activeSession.SessionID)
	if readErr != nil {
		m.sessionEvents = nil
		return m, fmt.Sprintf("Thread\nreverted, but reloading the session failed (in-memory context cleared): %s", readErr.Error())
	}
	m.sessionEvents = append([]sessions.Event{}, events...)
	rows := initialTranscript()
	rows = appendTranscriptRowsDedup(rows, transcriptRowsFromSessionEvents(events))
	m.transcript = rows
	m.resetFlushFrontier("· reverted ·")

	summary := fmt.Sprintf("Reverted to just before that message\n%d file(s) restored, %d deleted, %d skipped.",
		report.FilesRestored, report.FilesDeleted, len(report.Skipped))
	if len(report.Skipped) > 0 {
		summary += "\nskipped (not recoverable): " + strings.Join(report.Skipped, ", ")
	}
	return m, summary
}
