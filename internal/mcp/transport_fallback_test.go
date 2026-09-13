package mcp

import (
	"context"

	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestHTTPTransportFallsBackToSSE verifies a server configured as "http" that
// only speaks the legacy HTTP+SSE transport still connects: Streamable HTTP's
// initialize POST is rejected, so Connect retries over SSE.
func TestHTTPTransportFallsBackToSSE(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	events := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		// Streamable HTTP would POST JSON-RPC to the server root; this server only
		// speaks SSE, so reject it and force the fallback.
		if request.URL.Path == "/" && request.Method == http.MethodPost {
			http.Error(response, "streamable HTTP not supported", http.StatusNotFound)
			return
		}
		if request.Method == http.MethodGet && request.URL.Path == "/sse" {
			flusher, ok := response.(http.Flusher)
			if !ok {
				http.Error(response, "no flush", http.StatusInternalServerError)
				return
			}
			response.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(response, "event: endpoint\ndata: /messages\n\n")
			flusher.Flush()
			for {
				select {
				case event := <-events:
					_, _ = fmt.Fprint(response, event)
					flusher.Flush()
				case <-request.Context().Done():
					return
				}
			}
		}
		if request.Method == http.MethodPost && request.URL.Path == "/messages" {
			message := readHTTPRPCMessage(t, request)
			switch message.Method {
			case "initialize":
				events <- formatSSERPCResponse(t, message.ID, map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]any{"tools": map[string]any{}},
				})
				response.WriteHeader(http.StatusAccepted)
			case "notifications/initialized":
				response.WriteHeader(http.StatusNoContent)
			default:
				events <- formatSSERPCError(t, message.ID, "unexpected")
				response.WriteHeader(http.StatusAccepted)
			}
			return
		}
		http.Error(response, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	// Point "http" at the SSE endpoint URL so the GET fallback hits /sse.
	client, err := Connect(ctx, Server{Name: "legacy", Type: ServerTypeHTTP, URL: server.URL + "/sse"})
	if err != nil {
		t.Fatalf("Connect() error = %v, want SSE fallback success", err)
	}
	if _, ok := client.(*remoteSSEClient); !ok {
		t.Fatalf("expected remoteSSEClient after fallback, got %T", client)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

// TestHTTPTransportDoesNotFallBackOnAuthFailure pins that a 401 is surfaced as an
// auth error rather than masked by a pointless SSE retry that would repeat the
// same rejected request.
func TestHTTPTransportDoesNotFallBackOnAuthFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("WWW-Authenticate", `Bearer resource_metadata="https://mcp.test/.well-known/oauth-protected-resource"`)
		http.Error(response, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	_, err := Connect(ctx, Server{Name: "secure", Type: ServerTypeHTTP, URL: server.URL})
	if err == nil {
		t.Fatal("expected an auth error")
	}
	if !isTransportAuthError(err) && !needsAuth(err) {
		t.Fatalf("expected auth-classified error, got %v", err)
	}
}
