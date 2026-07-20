package azureopenai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
)

func TestChatCompletionsEndpointNormalizesAzureResourceURL(t *testing.T) {
	root, endpoint, err := ChatCompletionsEndpoint("https://example.openai.azure.com")
	if err != nil {
		t.Fatalf("ChatCompletionsEndpoint() error = %v", err)
	}
	if root != "https://example.openai.azure.com/openai/v1" {
		t.Fatalf("root = %q, want Azure v1 root", root)
	}
	if endpoint != "https://example.openai.azure.com/openai/v1/chat/completions" {
		t.Fatalf("endpoint = %q, want Azure chat completions endpoint", endpoint)
	}
}

func TestChatCompletionsEndpointPreservesDeploymentQuery(t *testing.T) {
	root, endpoint, err := ChatCompletionsEndpoint("https://example.openai.azure.com/openai/deployments/kajicode?api-version=2025-04-01-preview")
	if err != nil {
		t.Fatalf("ChatCompletionsEndpoint() error = %v", err)
	}
	if root != "https://example.openai.azure.com/openai/deployments/kajicode" {
		t.Fatalf("root = %q, want deployment root without query", root)
	}
	want := "https://example.openai.azure.com/openai/deployments/kajicode/chat/completions?api-version=2025-04-01-preview"
	if endpoint != want {
		t.Fatalf("endpoint = %q, want %q", endpoint, want)
	}
}

func TestProviderUsesAzureAPIKeyHeader(t *testing.T) {
	requests := make(chan *http.Request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Clone(r.Context())
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if payload["model"] != "kajicode-deployment" {
			t.Fatalf("model = %#v, want deployment name", payload["model"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	provider, err := New(Options{
		APIKey:  "az-secret",
		BaseURL: server.URL,
		Model:   "kajicode-deployment",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	events, err := provider.StreamCompletion(context.Background(), kajicoderuntime.CompletionRequest{
		Messages: []kajicoderuntime.Message{{Role: kajicoderuntime.MessageRoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StreamCompletion() error = %v", err)
	}
	for range events {
	}

	got := <-requests
	if got.URL.Path != "/openai/v1/chat/completions" {
		t.Fatalf("path = %q, want Azure chat completions path", got.URL.Path)
	}
	if got.Header.Get("api-key") != "az-secret" {
		t.Fatalf("api-key header = %q, want secret", got.Header.Get("api-key"))
	}
	if got.Header.Get("Authorization") != "" {
		t.Fatalf("Authorization header = %q, want empty", got.Header.Get("Authorization"))
	}
}
