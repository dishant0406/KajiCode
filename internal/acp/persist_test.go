package acp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dishant0406/KajiCode/internal/agent"
	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/sessions"
)

// runPrompt starts a session, sends one prompt, and returns the session id and
// the prompt's error.
func runPrompt(t *testing.T, deps Deps) (string, error) {
	t.Helper()
	h := newHarness(t, deps)
	t.Cleanup(h.stop)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var newRes NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir(), McpServers: []McpServer{}}, &newRes); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	err := h.client.Call(ctx, MethodSessionPrompt, PromptParams{SessionID: newRes.SessionID, Prompt: []ContentBlock{TextBlock("hi")}}, &PromptResult{})
	return newRes.SessionID, err
}

func payloadOf(t *testing.T, event sessions.Event) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func storedEvents(t *testing.T, deps Deps, sessionID string) []sessions.Event {
	t.Helper()
	events, err := deps.Store.ReadEvents(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func TestACPKeepsStreamedProseWhenARunFails(t *testing.T) {
	for name, runErr := range map[string]error{
		"cancelled":      context.Canceled,
		"provider error": errors.New("provider stream error: connection reset"),
	} {
		t.Run(name, func(t *testing.T) {
			deps := testDeps(t)
			deps.RunAgent = func(_ context.Context, _ string, _ kajicoderuntime.Provider, opts agent.Options) (agent.Result, error) {
				opts.OnText("partial ")
				opts.OnText("answer")
				return agent.Result{}, runErr
			}
			sessionID, _ := runPrompt(t, deps)

			events := storedEvents(t, deps, sessionID)
			if len(events) != 2 {
				t.Fatalf("expected the user and assistant messages, got %d events", len(events))
			}
			user, assistant := payloadOf(t, events[0]), payloadOf(t, events[1])
			if user["content"] != "hi" || user["model"] != "fake-model" || user["provider"] != "fake" {
				t.Errorf("unexpected user message: %v", user)
			}
			if assistant["role"] != "assistant" || assistant["content"] != "partial answer" {
				t.Errorf("expected the streamed prose to be saved, got %v", assistant)
			}
		})
	}
}

func TestACPRecordsARepeatingTurnSample(t *testing.T) {
	deps := testDeps(t)
	deps.RunAgent = func(_ context.Context, _ string, _ kajicoderuntime.Provider, opts agent.Options) (agent.Result, error) {
		opts.OnDegenerateTurn(strings.Repeat("Go.\nWriting.\n", 500))
		return agent.Result{FinalAnswer: "stopped"}, nil
	}
	sessionID, err := runPrompt(t, deps)
	if err != nil {
		t.Fatal(err)
	}

	var message string
	for _, event := range storedEvents(t, deps, sessionID) {
		if event.Type == sessions.EventError {
			message, _ = payloadOf(t, event)["message"].(string)
		}
	}
	if !strings.Contains(message, "repeating itself") || !strings.Contains(message, "Writing.") {
		t.Fatalf("expected a repeating-output event, got %q", message)
	}
	if len(message) > maxDegenerateSampleBytes+200 {
		t.Fatalf("sample must be bounded, got %d bytes", len(message))
	}
}
