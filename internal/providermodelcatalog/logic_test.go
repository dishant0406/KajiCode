package providermodelcatalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dishant0406/KajiCode/internal/modelregistry"
	"github.com/dishant0406/KajiCode/internal/providercatalog"
)

func TestLooksLikeCodingModelID(t *testing.T) {
	coding := []string{"gpt-4o", "claude-sonnet-4.5", "o1", "o3-mini", "o4-mini", "qwen2.5-coder", "deepseek-chat", "kimi-k2", "codestral-latest", "grok-code", "tencent/hy3"}
	for _, id := range coding {
		if !LooksLikeCodingModelID(id) {
			t.Errorf("LooksLikeCodingModelID(%q) = false, want true", id)
		}
	}
	nonCoding := []string{"", "   ", "whisper-1", "dall-e-3", "tts-1", "text-embedding-3-large", "omni-moderation-latest", "sora", "random-widget-7"}
	for _, id := range nonCoding {
		if LooksLikeCodingModelID(id) {
			t.Errorf("LooksLikeCodingModelID(%q) = true, want false", id)
		}
	}
}

func TestModelMatchesProvider(t *testing.T) {
	cases := []struct {
		transport providercatalog.Transport
		provider  modelregistry.ProviderKind
		want      bool
	}{
		{providercatalog.TransportOpenAI, modelregistry.ProviderOpenAI, true},
		{providercatalog.TransportOpenAI, modelregistry.ProviderAnthropic, false},
		{providercatalog.TransportAnthropic, modelregistry.ProviderAnthropic, true},
		{providercatalog.TransportAnthropicCompatible, modelregistry.ProviderAnthropic, true},
		{providercatalog.TransportGoogle, modelregistry.ProviderGoogle, true},
		{providercatalog.TransportGoogle, modelregistry.ProviderOpenAI, false},
		{providercatalog.TransportOpenAICompatible, modelregistry.ProviderOpenAI, false}, // default branch: no match
	}
	for _, tc := range cases {
		got := modelMatchesProvider(modelregistry.ModelEntry{Provider: tc.provider}, providercatalog.Descriptor{Transport: tc.transport})
		if got != tc.want {
			t.Errorf("modelMatchesProvider(provider=%q, transport=%q) = %v, want %v", tc.provider, tc.transport, got, tc.want)
		}
	}
}

func TestDefaultedOpenGatewayURL(t *testing.T) {
	if got := defaultedOpenGatewayURL(providercatalog.Descriptor{}, " https://x/models.json "); got != "https://x/models.json" {
		t.Fatalf("explicit override = %q, want trimmed override", got)
	}
	if got := defaultedOpenGatewayURL(providercatalog.Descriptor{DefaultBaseURL: "https://gw.example.com/v1"}, ""); got != "https://gw.example.com/kajicode/models.json" {
		t.Fatalf("derived = %q", got)
	}
	if got := defaultedOpenGatewayURL(providercatalog.Descriptor{DefaultBaseURL: "::not a url"}, ""); got != "https://opengateway.gitlawb.com/kajicode/models.json" {
		t.Fatalf("fallback = %q", got)
	}
}

func TestModelSortLabelAndSortModels(t *testing.T) {
	if got := modelSortLabel(Model{Description: "  GPT-4o  "}); got != "gpt-4o" {
		t.Fatalf("label from description = %q", got)
	}
	if got := modelSortLabel(Model{ID: "Llama-3"}); got != "llama-3" {
		t.Fatalf("label from id = %q", got)
	}
	models := []Model{{ID: "b", Description: "Zeta"}, {ID: "a", Description: "Alpha"}}
	sortModels(models)
	if models[0].Description != "Alpha" {
		t.Fatalf("sortModels did not order by label: %#v", models)
	}
}

func TestFetchJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("Accept header = %q, want application/json", r.Header.Get("Accept"))
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	body, err := fetchJSON(context.Background(), srv.URL, srv.Client())
	if err != nil || string(body) != `{"ok":true}` {
		t.Fatalf("fetchJSON = %q, %v", string(body), err)
	}
	if _, err := fetchJSON(context.Background(), "   ", srv.Client()); err == nil {
		t.Fatal("expected an error for an empty endpoint")
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer bad.Close()
	if _, err := fetchJSON(context.Background(), bad.URL, bad.Client()); err == nil {
		t.Fatal("expected an error for a 500 response")
	}
}

func TestFetchJSONReadsCatalogLargerThanFourMegabytes(t *testing.T) {
	padding := strings.Repeat("x", 5<<20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"openai":{"models":{"gpt-4o":{"id":"gpt-4o"}}},"pad":{"models":{"x":{"id":"` + padding + `"}}}}`))
	}))
	defer srv.Close()

	models, err := FetchModelsDev(context.Background(), "openai", FetchOptions{HTTPClient: srv.Client(), ModelsDevURL: srv.URL})
	if err != nil || !containsModelID(models, "gpt-4o") {
		t.Fatalf("large catalog models = %#v, %v", models, err)
	}
}

func TestFetchModelsDevAndOpenGatewayOverHTTP(t *testing.T) {
	modelsDev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"openai":{"models":{"gpt-4o":{"id":"gpt-4o"}}}}`))
	}))
	defer modelsDev.Close()
	models, err := FetchModelsDev(context.Background(), "openai", FetchOptions{HTTPClient: modelsDev.Client(), ModelsDevURL: modelsDev.URL})
	if err != nil {
		t.Fatalf("FetchModelsDev error: %v", err)
	}
	if !containsModelID(models, "gpt-4o") {
		t.Fatalf("FetchModelsDev models = %#v, want gpt-4o", models)
	}

	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"models":[{"id":"claude-coder"}]}`))
	}))
	defer gateway.Close()
	models, err = FetchOpenGateway(context.Background(), gateway.URL, FetchOptions{HTTPClient: gateway.Client()})
	if err != nil {
		t.Fatalf("FetchOpenGateway error: %v", err)
	}
	if !containsModelID(models, "claude-coder") {
		t.Fatalf("FetchOpenGateway models = %#v, want claude-coder", models)
	}

	// FetchRemote routes the opengateway provider to FetchOpenGateway via the override URL.
	routed, err := FetchRemote(context.Background(), providercatalog.Descriptor{ID: "gitlawb-opengateway"}, FetchOptions{HTTPClient: gateway.Client(), OpenGatewayURL: gateway.URL})
	if err != nil {
		t.Fatalf("FetchRemote error: %v", err)
	}
	if !containsModelID(routed, "claude-coder") {
		t.Fatalf("FetchRemote models = %#v, want claude-coder", routed)
	}
}

func containsModelID(models []Model, id string) bool {
	for _, model := range models {
		if model.ID == id {
			return true
		}
	}
	return false
}

func TestOpenCodeCatalogSkipsGeminiModels(t *testing.T) {
	body := []byte(`{"opencode":{"models":{
		"gemini-3.5-flash":{"id":"gemini-3.5-flash","tool_call":true,"provider":{"npm":"@ai-sdk/google"}},
		"kimi-k3":{"id":"kimi-k3","tool_call":true}}}}`)
	models, err := ParseModelsDevProvider(body, "opencode")
	if err != nil {
		t.Fatal(err)
	}
	if containsModelID(models, "gemini-3.5-flash") || !containsModelID(models, "kimi-k3") {
		t.Fatalf("models = %#v, want kimi-k3 only", models)
	}
}
