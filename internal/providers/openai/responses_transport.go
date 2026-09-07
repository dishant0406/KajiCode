package openai

// Generalized OpenAI Responses-API transport.
//
// The ChatGPT Codex backend and OpenCode Go both serve the OpenAI Responses API
// at {baseURL}/responses (not chat-completions). Both need the same request
// builder, typed SSE dispatcher, and tool-call accumulator. This file hosts that
// transport so CodexProvider (Codex headers) and the per-model responses override
// (x-opencode-session for OpenCode Go) can share one implementation.

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
)

// responsesRequestExtra is the per-request header hook for a responses
// transport. It runs after the inner openai provider's built-in headers, on both
// the original request and the 401-refresh retry, so any injected value must be
// re-derivable from the request.
type responsesRequestExtra func(req *http.Request)

// responsesTransport is a provider that streams the OpenAI Responses API over
// the shared openai transport plumbing (endpoint, auth, retry, idle timeout).
// Unlike CodexProvider it is header-agnostic: callers supply their specialized
// request headers via requestExtra.
type responsesTransport struct {
	inner        *Provider
	label        string
	requestExtra responsesRequestExtra
}

// NewResponsesProvider builds a generic Responses-API provider (no Codex
// headers) that POSTs to {baseURL}/responses. sessionID, when non-empty, is sent
// as `x-opencode-session` — required by OpenCode Go for request routing, and a
// harmless no-op header for OpenAI's own Responses API. When sessionID is empty,
// a random session id is generated so the header is always present (OpenCode Go
// returns 400 MissingSessionID otherwise). It returns the shared
// responsesTransport, which satisfies kajicoderuntime.Provider.
func NewResponsesProvider(options Options, sessionID string) (*responsesTransport, error) {
	openaiOpts := options
	if baseURL := strings.TrimRight(strings.TrimSpace(openaiOpts.BaseURL), "/"); baseURL != "" {
		openaiOpts.Endpoint = baseURL + "/responses"
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		var buf [16]byte
		if _, err := rand.Read(buf[:]); err == nil {
			sessionID = fmt.Sprintf("kajicode-%x", buf[:])
		} else {
			sessionID = "kajicode-session"
		}
	}
	transport := &responsesTransport{label: "responses"}
	transport.requestExtra = func(req *http.Request) {
		req.Header.Set("x-opencode-session", sessionID)
	}
	inner, err := New(openaiOpts)
	if err != nil {
		return nil, fmt.Errorf("openai responses provider: %w", err)
	}
	transport.inner = inner
	return transport, nil
}

// StreamCompletion builds a Responses-API request and dispatches it through the
// shared Responses SSE stream path.
func (p *responsesTransport) StreamCompletion(ctx context.Context, request kajicoderuntime.CompletionRequest) (<-chan kajicoderuntime.StreamEvent, error) {
	responsesReq, err := p.buildResponsesRequest(request)
	if err != nil {
		return nil, fmt.Errorf("encode %s request: %w", p.label, err)
	}
	body, err := json.Marshal(responsesReq)
	if err != nil {
		return nil, fmt.Errorf("encode %s request: %w", p.label, err)
	}
	events := make(chan kajicoderuntime.StreamEvent, 16)
	go func() {
		defer close(events)
		p.streamResponses(ctx, body, events)
	}()
	return events, nil
}
