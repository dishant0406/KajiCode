package sessions

import (
	"encoding/json"
	"strings"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
)

func eventPayload(event Event) map[string]any {
	payload := map[string]any{}
	_ = json.Unmarshal(event.Payload, &payload)
	return payload
}

func firstPayloadString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := payloadString(payload, key); value != "" {
			return value
		}
	}
	return ""
}

func payloadString(payload map[string]any, key string) string {
	value, ok := payload[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return string(data)
	}
}

func payloadStrings(payload map[string]any, key string) []string {
	raw, ok := payload[key]
	if !ok || raw == nil {
		return nil
	}
	switch values := raw.(type) {
	case []string:
		return values
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		if text := payloadString(payload, key); text != "" {
			return []string{text}
		}
		return nil
	}
}

func cleanModelMessages(messages []kajicoderuntime.Message) []kajicoderuntime.Message {
	out := make([]kajicoderuntime.Message, 0, len(messages))
	for _, message := range messages {
		if message.Role == kajicoderuntime.MessageRoleSystem {
			continue
		}
		if message.Content == "" && len(message.ToolCalls) == 0 && message.ToolCallID == "" {
			continue
		}
		message.Images = nil
		out = append(out, message)
	}
	return out
}

// CompactableEventsFromMessages returns the events whose model messages a
// compaction elided, by walking the EXACT same event-to-message fold the replay
// layer uses (ModelMessagesFromEvents). The elided count is measured in model
// messages, so events that produce none (empty, system, usage, composer_input)
// never consume a slot; the mapping stops the moment the fold reaches the
// elided count, which leaves every event of the preserved tail untouched. Pass
// the log view the fold will be applied to (the rehydrated events for
// reconstructed sessions), so a prior compaction snapshot is already collapsed
// and the first elided messages genuinely come from the live tail.
func CompactableEventsFromMessages(elidedCount int, events []Event) []Event {
	if elidedCount <= 0 {
		return nil
	}
	compactable := make([]Event, 0, elidedCount)
	messages := []kajicoderuntime.Message{}
	for _, event := range events {
		if len(messages) >= elidedCount {
			break
		}
		before := len(messages)
		messages = foldModelMessage(messages, event)
		if len(messages) > before {
			compactable = append(compactable, event)
		}
	}
	return compactable
}
