package sessions

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
)

const modelHistorySummaryHeader = "[Summary of earlier conversation]"

// foldModelMessage advances the model-history fold by ONE event, exactly as
// ModelMessagesFromEvents would. Returning it as a package-level helper lets
// CompactableEventsFromMessages walk the same event-to-message correspondence
// in lockstep without re-running the whole prefix per event.
func foldModelMessage(messages []kajicoderuntime.Message, event Event) []kajicoderuntime.Message {
	payload := eventPayload(event)
	switch event.Type {
	case EventCompaction:
		if compacted := modelMessagesFromCompaction(payload, len(messages) > 0); len(compacted) > 0 {
			messages = compacted
		}
	case EventMessage:
		messages = appendEventMessage(messages, payload)
	case EventToolCall:
		if call, ok := toolCallFromPayload(payload); ok {
			messages = append(messages, kajicoderuntime.Message{
				Role:      kajicoderuntime.MessageRoleAssistant,
				ToolCalls: []kajicoderuntime.ToolCall{call},
			})
		}
	case EventToolResult:
		if message, ok := toolResultMessage(payload); ok {
			messages = append(messages, message)
		}
	}
	return messages
}

func ModelMessagesFromEvents(events []Event) []kajicoderuntime.Message {
	messages := []kajicoderuntime.Message{}
	for _, event := range events {
		messages = foldModelMessage(messages, event)
	}
	return cleanModelMessages(messages)
}

func ModelCompactionPayload(summary string, messages []kajicoderuntime.Message, compactedEvents []Event, trigger string) CompactionPayload {
	payload := CompactionPayload{
		Summary:           strings.TrimSpace(summary),
		CompactableCount:  len(compactedEvents),
		PreservedCount:    len(cleanModelMessages(messages)),
		CompactableEvents: eventRefs(compactedEvents),
		ModelMessages:     cleanModelMessages(messages),
		Trigger:           strings.TrimSpace(trigger),
	}
	if len(compactedEvents) > 0 {
		last := compactedEvents[len(compactedEvents)-1]
		payload.CompactedThroughEventID = last.ID
		payload.CompactedThroughSequence = last.Sequence
	}
	return payload
}

func appendEventMessage(messages []kajicoderuntime.Message, payload map[string]any) []kajicoderuntime.Message {
	role := payloadString(payload, "role")
	content := payloadString(payload, "content")
	switch role {
	case "user":
		return appendContentMessage(messages, kajicoderuntime.MessageRoleUser, content)
	case "assistant":
		return appendContentMessage(messages, kajicoderuntime.MessageRoleAssistant, content)
	case "ask_user":
		return appendContentMessage(messages, kajicoderuntime.MessageRoleAssistant, askUserContent(payload))
	case "ask_user_answers":
		return appendContentMessage(messages, kajicoderuntime.MessageRoleUser, askUserAnswersContent(payload))
	default:
		return appendContentMessage(messages, kajicoderuntime.MessageRoleUser, content)
	}
}

func appendContentMessage(messages []kajicoderuntime.Message, role kajicoderuntime.MessageRole, content string) []kajicoderuntime.Message {
	content = strings.TrimSpace(content)
	if content == "" {
		return messages
	}
	return append(messages, kajicoderuntime.Message{Role: role, Content: content})
}

func modelMessagesFromCompaction(payload map[string]any, useSnapshot bool) []kajicoderuntime.Message {
	if raw, ok := payload["modelMessages"]; ok && useSnapshot {
		data, err := json.Marshal(raw)
		if err == nil {
			var messages []kajicoderuntime.Message
			if json.Unmarshal(data, &messages) == nil && len(messages) > 0 {
				return cleanModelMessages(messages)
			}
		}
	}
	summary := strings.TrimSpace(payloadString(payload, "summary"))
	if summary == "" {
		return nil
	}
	return []kajicoderuntime.Message{{
		Role:    kajicoderuntime.MessageRoleUser,
		Content: modelHistorySummaryHeader + "\n" + summary,
	}}
}

func toolCallFromPayload(payload map[string]any) (kajicoderuntime.ToolCall, bool) {
	call := kajicoderuntime.ToolCall{
		ID:        firstPayloadString(payload, "id", "toolCallId"),
		Name:      firstPayloadString(payload, "name", "toolName"),
		Arguments: payloadString(payload, "arguments"),
	}
	return call, call.ID != "" || call.Name != ""
}

func toolResultMessage(payload map[string]any) (kajicoderuntime.Message, bool) {
	id := payloadString(payload, "toolCallId")
	output := payloadString(payload, "output")
	if output == "" {
		output = strings.TrimSpace(fmt.Sprintf("%s %s", payloadString(payload, "name"), payloadString(payload, "status")))
	}
	if id == "" && output == "" {
		return kajicoderuntime.Message{}, false
	}
	return kajicoderuntime.Message{Role: kajicoderuntime.MessageRoleTool, ToolCallID: id, Content: output}, true
}

func askUserContent(payload map[string]any) string {
	questions := payloadStrings(payload, "questions")
	if len(questions) == 0 {
		if question := payloadString(payload, "question"); question != "" {
			questions = append(questions, question)
		}
	}
	if len(questions) == 0 {
		return ""
	}
	return "Asked the user:\n- " + strings.Join(questions, "\n- ")
}

func askUserAnswersContent(payload map[string]any) string {
	answers := payloadStrings(payload, "answers")
	if len(answers) == 0 {
		return ""
	}
	return "User answered ask_user:\n- " + strings.Join(answers, "\n- ")
}

// CompactedModelMessages builds the model-history snapshot a compaction payload
// should persist: the summary as a leading user message followed by the model
// messages of the preserved tail events. It mirrors what walking the rehydrated
// event log produces, but is usable directly at compaction time so the stored
// snapshot is self-contained (a raw-event consumer does not need the preserved
// events afterwards).
func CompactedModelMessages(summary string, preservedEvents []Event) []kajicoderuntime.Message {
	snapshot := []kajicoderuntime.Message{}
	if summary = strings.TrimSpace(summary); summary != "" {
		snapshot = append(snapshot, kajicoderuntime.Message{
			Role:    kajicoderuntime.MessageRoleUser,
			Content: modelHistorySummaryHeader + "\n" + summary,
		})
	}
	snapshot = append(snapshot, ModelMessagesFromEvents(preservedEvents)...)
	return cleanModelMessages(snapshot)
}
