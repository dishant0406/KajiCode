package agents

import (
	"encoding/json"

	"github.com/dishant0406/KajiCode/internal/agent"
	"github.com/dishant0406/KajiCode/internal/sessions"
	"github.com/dishant0406/KajiCode/internal/streamjson"
)

// childRecorder persists a child run's events to its child session and, when an
// event sink is wired, emits the synthesized stream-json events a surface uses
// for live progress. It is the in-process replacement for the old subprocess
// stream-json reader.
type childRecorder struct {
	sessionID string
	store     *sessions.Store
	emit      func(streamjson.Event)
}

func (recorder *childRecorder) append(eventType sessions.EventType, payload map[string]any) {
	if recorder == nil || recorder.store == nil || recorder.sessionID == "" {
		return
	}
	_, _ = recorder.store.AppendEvent(recorder.sessionID, sessions.AppendEventInput{Type: eventType, Payload: payload})
}

func (recorder *childRecorder) onText(text string) {
	if text == "" {
		return
	}
	recorder.append(sessions.EventMessage, map[string]any{"role": "assistant", "content": text})
	recorder.emitEvent(streamjson.Event{Type: streamjson.EventText, Delta: text})
}

func (recorder *childRecorder) onToolCall(call agent.ToolCall) {
	arguments := call.Arguments
	if arguments == "" {
		arguments = "{}"
	}
	recorder.append(sessions.EventToolCall, map[string]any{
		"id":        call.ID,
		"name":      call.Name,
		"arguments": arguments,
	})
	recorder.emitEvent(streamjson.Event{
		Type: streamjson.EventToolCall,
		ID:   call.ID,
		Name: call.Name,
		Args: decodeArgs(arguments),
	})
}

func (recorder *childRecorder) onToolResult(result agent.ToolResult) {
	payload := map[string]any{
		"toolCallId": result.ToolCallID,
		"name":       result.Name,
		"status":     string(result.Status),
		"output":     result.Output,
	}
	recorder.append(sessions.EventToolResult, payload)
	recorder.emitEvent(streamjson.Event{
		Type:   streamjson.EventToolResult,
		ID:     result.ToolCallID,
		Name:   result.Name,
		Action: string(result.Status),
		Output: result.Output,
	})
}

func (recorder *childRecorder) onUsage(usage agent.Usage) {
	recorder.append(sessions.EventUsage, map[string]any{
		"inputTokens":  usage.InputTokens,
		"outputTokens": usage.OutputTokens,
	})
	prompt := usage.InputTokens
	completion := usage.OutputTokens
	total := prompt + completion
	recorder.emitEvent(streamjson.Event{
		Type:             streamjson.EventUsage,
		PromptTokens:     &prompt,
		CompletionTokens: &completion,
		TotalTokens:      &total,
	})
}

func (recorder *childRecorder) emitEvent(event streamjson.Event) {
	if recorder == nil || recorder.emit == nil {
		return
	}
	event.SessionID = recorder.sessionID
	recorder.emit(event)
}

func decodeArgs(raw string) any {
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return raw
	}
	return args
}
