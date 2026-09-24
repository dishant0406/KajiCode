package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dishant0406/KajiCode/internal/agent"
)

// ask_user routing. When the client advertises form elicitation, the model's
// ask_user tool is presented as an ACP form; otherwise the handler is nil and the
// agent falls back to its headless, non-blocking answer.

// askUserHandler returns the OnAskUser callback for one session, or nil when the
// client cannot render a form (so the agent uses its fallback).
func (a *Agent) askUserHandler(sessionID string) func(context.Context, agent.AskUserRequest) (agent.AskUserResponse, error) {
	if !a.supportsElicitation() {
		return nil
	}
	return func(ctx context.Context, req agent.AskUserRequest) (agent.AskUserResponse, error) {
		return a.elicit(ctx, sessionID, req)
	}
}

func (a *Agent) supportsElicitation() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.clientCaps.Elicitation != nil && a.clientCaps.Elicitation.Form != nil
}

// elicit sends one elicitation form and maps the client's response back to the
// answers the agent loop expects. A client error, a decline, or a cancel yields
// empty answers (the same shape as a user skipping the prompt); a cancelled
// context aborts the run.
func (a *Agent) elicit(ctx context.Context, sessionID string, req agent.AskUserRequest) (agent.AskUserResponse, error) {
	schema, _ := json.Marshal(askUserSchema(req.Questions))
	message := strings.TrimSpace(req.Header)
	if message == "" {
		message = "KajiCode needs your input."
	}
	params := CreateElicitationParams{
		Mode:            "form",
		Message:         message,
		SessionID:       sessionID,
		ToolCallID:      req.ToolCallID,
		RequestedSchema: schema,
	}

	var result CreateElicitationResult
	if err := a.conn.Call(ctx, MethodElicitationCreate, params, &result); err != nil {
		return agent.AskUserResponse{}, err
	}
	if result.Action != "accept" {
		return agent.AskUserResponse{Answers: make([]string, len(req.Questions))}, nil
	}
	answers := make([]string, len(req.Questions))
	for i := range req.Questions {
		if raw, ok := result.Content[questionKey(i)]; ok {
			answers[i] = stringifyAnswer(raw)
		}
	}
	return agent.AskUserResponse{Answers: answers}, nil
}

// askUserSchema builds a flat JSON-schema object with one property per question.
// AskUserQuestion carries guidance (recommended option, multi-select) but not a
// value set, so each property is a plain string — enough for the client to render
// one labelled field per question, which is all the answers require.
func askUserSchema(questions []agent.AskUserQuestion) map[string]any {
	properties := map[string]any{}
	required := make([]string, 0, len(questions))
	for i, q := range questions {
		field := map[string]any{"type": "string"}
		if title := strings.TrimSpace(q.Question); title != "" {
			field["title"] = title
		}
		properties[questionKey(i)] = field
		required = append(required, questionKey(i))
	}
	return map[string]any{"type": "object", "properties": properties, "required": required}
}

// questionKey is the stable property name for the i'th ask_user question. The
// answers array is positional, so the key only needs to survive the round trip.
func questionKey(i int) string { return fmt.Sprintf("answer_%d", i) }

// stringifyAnswer renders a raw JSON answer as the string the loop expects,
// stripping the surrounding quotes of a JSON string.
func stringifyAnswer(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return strings.TrimSpace(string(raw))
}

// elicitForm sends one form elicitation and returns the submitted content as
// strings. ok=false means the client does not support form elicitation (so the
// caller should fall back to a non-elicitation path). A decline/cancel returns
// ok=true with an empty map.
func (a *Agent) elicitForm(ctx context.Context, sessionID, message string, schema map[string]any) (map[string]string, bool, error) {
	if !a.supportsElicitation() {
		return nil, false, nil
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, true, err
	}
	params := CreateElicitationParams{Mode: "form", Message: message, SessionID: sessionID, RequestedSchema: raw}
	var result CreateElicitationResult
	if err := a.conn.Call(ctx, MethodElicitationCreate, params, &result); err != nil {
		return nil, true, err
	}
	out := map[string]string{}
	if result.Action == "accept" {
		for key, value := range result.Content {
			var s string
			if json.Unmarshal(value, &s) == nil {
				out[key] = s
			} else {
				out[key] = strings.TrimSpace(string(value))
			}
		}
	}
	return out, true, nil
}
