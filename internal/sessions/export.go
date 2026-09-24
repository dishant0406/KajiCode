package sessions

import "strings"

// Transcript rendering shared by every surface that exports a conversation.
// It reads stored events, so the same output is produced from the TUI, the
// headless CLI, and ACP.

// Transcript role prefixes, shared so the TUI's row-based renderer and the
// event-based renderer below cannot drift.
const (
	TranscriptUserPrefix      = "you: "
	TranscriptAssistantPrefix = "kajicode: "
	TranscriptSystemPrefix    = "· "
)

// TranscriptFromEvents renders a session's message events as a plain,
// role-prefixed text document: user prompts and assistant answers, with
// system-role messages as notes. Tool-call and permission events are skipped.
// skipMessage, when non-nil, drops message content that is not real progress
// (the guardrail no-output stop) so a failed run never pollutes the export.
func TranscriptFromEvents(events []Event, skipMessage func(content string) bool) string {
	var b strings.Builder
	for _, event := range events {
		if event.Type != EventMessage {
			continue
		}
		payload := eventPayload(event)
		content := strings.TrimRight(payloadString(payload, "content"), "\n")
		if strings.TrimSpace(content) == "" {
			continue
		}
		if skipMessage != nil && skipMessage(content) {
			continue
		}
		var prefix string
		switch strings.ToLower(strings.TrimSpace(payloadString(payload, "role"))) {
		case "user":
			prefix = TranscriptUserPrefix
		case "assistant":
			prefix = TranscriptAssistantPrefix
		case "system":
			prefix = TranscriptSystemPrefix
		default:
			continue
		}
		b.WriteString(prefix)
		b.WriteString(content)
		b.WriteString("\n\n")
	}
	return b.String()
}
