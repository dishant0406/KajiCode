package agents

import (
	"encoding/json"
	"strings"

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
	// text buffers streamed assistant deltas. The provider calls onText once per
	// token/fragment, but a child session must persist ONE assistant message per
	// contiguous prose segment — exactly as the parent session records a single
	// message per turn segment. Without buffering, each delta became its own
	// persisted message and the subchat drill-in rendered one word per line.
	text strings.Builder
}

func (recorder *childRecorder) append(eventType sessions.EventType, payload map[string]any) {
	if recorder == nil || recorder.store == nil || recorder.sessionID == "" {
		return
	}
	_, _ = recorder.store.AppendEvent(recorder.sessionID, sessions.AppendEventInput{Type: eventType, Payload: payload})
}

func (recorder *childRecorder) onText(text string) {
	if recorder == nil || text == "" {
		return
	}
	recorder.text.WriteString(text)
	recorder.emitEvent(streamjson.Event{Type: streamjson.EventText, Delta: text})
}

// flushText persists the accumulated assistant text as a single EventMessage and
// resets the buffer. It is called at each prose segment boundary — before a tool
// call, and once at run end — so a segment matches the parent session's one
// message per turn segment. A no-op when nothing is buffered.
func (recorder *childRecorder) flushText() {
	if recorder == nil || recorder.text.Len() == 0 {
		return
	}
	content := recorder.text.String()
	recorder.text.Reset()
	recorder.append(sessions.EventMessage, map[string]any{"role": "assistant", "content": content})
}

// flushFinalText flushes the buffered prose, falling back to fallback when the
// provider never streamed text (a non-streaming response surfaces only in the
// run's FinalAnswer). Without the fallback the child session would record no
// assistant message at all for such providers.
func (recorder *childRecorder) flushFinalText(fallback string) {
	if recorder == nil {
		return
	}
	if recorder.text.Len() == 0 && strings.TrimSpace(fallback) != "" {
		recorder.text.WriteString(fallback)
	}
	recorder.flushText()
}

func (recorder *childRecorder) onToolCall(call agent.ToolCall) {
	recorder.flushText()
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
