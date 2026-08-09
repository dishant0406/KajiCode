package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- Exa provider ---

func TestExaProviderParsesResultsWithContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != "exa-key" {
			t.Errorf("x-api-key header = %q, want %q", got, "exa-key")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[
			{"title":"Alpha","url":"https://a.dev","text":"alpha page body text","score":0.9},
			{"title":"NoURL","url":"","text":"skip me","score":0.6},
			{"title":"Beta","url":"https://b.dev","text":"","score":0.2}
		]}`))
	}))
	defer server.Close()

	p := &exaProvider{client: server.Client(), apiKey: "exa-key", baseURL: server.URL}
	results, err := p.Search(context.Background(), "q", SearchOptions{Limit: 5, ContextMax: 10000})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 (empty-URL dropped), got %d: %#v", len(results), results)
	}
	if results[0].Content != "alpha page body text" {
		t.Errorf("content = %q", results[0].Content)
	}
	if results[0].Score != 0.9 {
		t.Errorf("score = %v, want 0.9", results[0].Score)
	}
	// Snippet derives from content when present.
	if results[1].Snippet != "" && results[0].Snippet != "alpha page body text" {
		t.Errorf("snippet should derive from content/title")
	}
}

func TestExaProviderSendsLiveCrawlAndType(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = decodeJSONBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer server.Close()

	p := &exaProvider{client: server.Client(), apiKey: "k", baseURL: server.URL}
	if _, err := p.Search(context.Background(), "q", SearchOptions{Limit: 3, Type: "deep", LiveCrawl: "preferred", ContextMax: 500}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got["numResults"].(float64) != 3 {
		t.Errorf("numResults = %v, want 3", got["numResults"])
	}
	if got["type"] != "deep" {
		t.Errorf("type = %v, want deep", got["type"])
	}
	contents := got["contents"].(map[string]any)
	if contents["includeText"] != true {
		t.Errorf("includeText = %v, want true", contents["includeText"])
	}
	if contents["maxCharacters"].(float64) != 500 {
		t.Errorf("maxCharacters = %v, want 500", contents["maxCharacters"])
	}
}

// --- Tavily provider ---

func TestTavilyProviderParsesRichContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tavily-key" {
			t.Errorf("Authorization = %q, want Bearer", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[
			{"title":"A","url":"https://a.dev","content":"short content","raw_content":"# rich\n\nfull markdown body","score":0.8},
			{"title":"NoURL","url":"","content":"x","score":0.5}
		],"answer":"An answer"}`))
	}))
	defer server.Close()

	p := &tavilyProvider{client: server.Client(), apiKey: "tavily-key", baseURL: server.URL}
	results, err := p.Search(context.Background(), "q", SearchOptions{Limit: 5, ContextMax: 10000})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 (empty-URL dropped), got %d", len(results))
	}
	// Prefers raw markdown over content.
	if !strings.Contains(results[0].Content, "full markdown body") {
		t.Errorf("content should prefer raw_content, got %q", results[0].Content)
	}
	if results[0].Score != 0.8 {
		t.Errorf("score = %v, want 0.8", results[0].Score)
	}
}

func TestTavilyContentBudgetIsEnforced(t *testing.T) {
	body := `{"results":[{"title":"A","url":"https://a.dev","content":"long body text here","score":0.9}]}`
	results, err := parseTavilyResults([]byte(body), 10)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !strings.HasPrefix(results[0].Content, "long body ") {
		t.Errorf("content should keep first 10 chars, got %q", results[0].Content)
	}
	if !strings.Contains(results[0].Content, "[truncated]") {
		t.Errorf("content should mark truncation, got %q", results[0].Content)
	}
}

// --- Selection & failover ---

func TestBuildSearchBackendAutoPrecedence(t *testing.T) {
	// Single content provider → that provider, not a chain.
	env := searchEnv{exaKey: "exa"}
	if backend := buildSearchBackend(env); backend != nil {
		if _, ok := backend.(*exaProvider); !ok {
			t.Fatalf("single exa key → want exaProvider, got %T", backend)
		}
	}

	env = searchEnv{tavilyKey: "tavily"}
	if backend := buildSearchBackend(env); backend != nil {
		if _, ok := backend.(*tavilyProvider); !ok {
			t.Fatalf("single tavily key → want tavilyProvider, got %T", backend)
		}
	}

	// Only a base URL → snippet backend, not a chain.
	env = searchEnv{baseURL: "http://sx"}
	if backend := buildSearchBackend(env); backend != nil {
		if _, ok := backend.(*httpSearchBackend); !ok {
			t.Fatalf("only baseURL → want snippet backend, got %T", backend)
		}
	}
}

func TestBuildSearchBackendMultipleContentKeysChain(t *testing.T) {
	env := searchEnv{exaKey: "exa", tavilyKey: "tavily"}
	if backend := buildSearchBackend(env); backend != nil {
		if _, ok := backend.(*searchBackendChain); !ok {
			t.Fatalf("two content keys → want failover chain, got %T", backend)
		}
	}
}

func TestBuildSearchBackendAutoChainAndExplicit(t *testing.T) {
	// Explicit provider wins and is a single backend, not a chain (regardless of
	// other keys being set).
	env := searchEnv{exaKey: "exa", tavilyKey: "tavily", provider: providerTavily}
	if backend := buildSearchBackend(env); backend != nil {
		if _, ok := backend.(*tavilyProvider); !ok {
			t.Fatalf("explicit tavily → want tavilyProvider, got %T", backend)
		}
	}

	env = searchEnv{provider: providerExa}
	if backend := buildSearchBackend(env); backend != nil {
		t.Fatalf("explicit exa without key → nil, got %T", backend)
	}

	env = searchEnv{}
	if backend := buildSearchBackend(env); backend != nil {
		t.Fatalf("nothing configured → nil, got %T", backend)
	}
}

func TestSearchBackendFailover(t *testing.T) {
	failing := &stubBackend{err: context.DeadlineExceeded}
	empty := &stubBackend{results: nil}
	good := &stubBackend{results: []searchResult{{Title: "Good", URL: "https://g.dev"}}}

	chain := &searchBackendChain{backends: []searchBackend{failing, empty, good}}
	results, err := chain.Search(context.Background(), "q", SearchOptions{Limit: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].Title != "Good" {
		t.Fatalf("failover did not reach good backend: %#v", results)
	}

	// All fail → last error surfaces.
	chain = &searchBackendChain{backends: []searchBackend{&stubBackend{err: context.Canceled}}}
	if _, err := chain.Search(context.Background(), "q", SearchOptions{}); err == nil {
		t.Fatal("expected error when all backends fail")
	}
}

func TestTinyFishProviderParsesSnippetResults(t *testing.T) {
	body := []byte(`{"results":[
		{"position":1,"site_name":"go.dev","title":"Go","snippet":"The Go language","url":"https://go.dev"},
		{"position":2,"site_name":"example.com","title":"Example","snippet":"","url":""}
	]}`)
	got, err := parseTinyFishResults(body)
	if err != nil {
		t.Fatalf("parseTinyFishResults: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 result (empty url dropped), got %d", len(got))
	}
	r := got[0]
	if r.Title != "Go" || r.URL != "https://go.dev" {
		t.Errorf("tinyfish parse = %+v", r)
	}
	// Snippet-only backend: snippet populated, no full content.
	if r.Snippet != "The Go language" {
		t.Errorf("snippet = %q, want %q", r.Snippet, "The Go language")
	}
	if r.Content != "The Go language" {
		t.Errorf("content = %q, want snippet text", r.Content)
	}
}

func TestTinyFishProviderSendsKeyHeaderAndQuery(t *testing.T) {
	var gotURL string
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		gotKey = r.Header.Get("X-API-Key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"Go","url":"https://go.dev","snippet":"The Go language"}]}`))
	}))
	defer srv.Close()

	p := &tinyFishProvider{client: srv.Client(), apiKey: "tfkey", baseURL: srv.URL}
	results, err := p.Search(context.Background(), "golang", SearchOptions{Limit: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotKey != "tfkey" {
		t.Errorf("X-API-Key header = %q", gotKey)
	}
	if !strings.Contains(gotURL, "query=golang") || !strings.Contains(gotURL, "page=0") {
		t.Errorf("request url = %q", gotURL)
	}
	if len(results) != 1 || results[0].URL != "https://go.dev" {
		t.Errorf("results = %+v", results)
	}
}

func TestTinyFishProviderReturnsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad key", http.StatusUnauthorized)
	}))
	defer srv.Close()
	p := &tinyFishProvider{client: srv.Client(), apiKey: "bad", baseURL: srv.URL}
	if _, err := p.Search(context.Background(), "q", SearchOptions{}); err == nil {
		t.Fatal("expected error on non-2xx")
	}
}

func TestBuildSearchBackendTinyFish(t *testing.T) {
	// Single tinyfish key → tinyfishProvider.
	env := searchEnv{tinyFishKey: "tf"}
	if backend := buildSearchBackend(env); backend != nil {
		if _, ok := backend.(*tinyFishProvider); !ok {
			t.Fatalf("single tinyfish key → want tinyfishProvider, got %T", backend)
		}
	}

	// Multiple keys incl. tinyfish → failover chain.
	env = searchEnv{exaKey: "e", tavilyKey: "t", tinyFishKey: "tf"}
	if backend := buildSearchBackend(env); backend != nil {
		if _, ok := backend.(*searchBackendChain); !ok {
			t.Fatalf("multiple keys → want chain, got %T", backend)
		}
	}

	// Explicit tinyfish selection.
	env = searchEnv{tinyFishKey: "tf", provider: providerTinyFish}
	if backend := buildSearchBackend(env); backend != nil {
		if _, ok := backend.(*tinyFishProvider); !ok {
			t.Fatalf("explicit tinyfish → want tinyfishProvider, got %T", backend)
		}
	}

	// Explicit tinyfish without key → nil.
	env = searchEnv{provider: providerTinyFish}
	if backend := buildSearchBackend(env); backend != nil {
		t.Fatalf("explicit tinyfish without key → nil, got %T", backend)
	}
}

func TestWebSearchProvidersIncludesTinyFish(t *testing.T) {
	found := false
	for _, p := range WebSearchProviders() {
		if p.ID == providerTinyFish {
			found = true
			if p.EnvVar != envTinyFishAPIKey || !p.RequiresKey {
				t.Errorf("tinyfish metadata = %+v", p)
			}
			if p.DefaultBaseURL != tinyFishDefaultURL {
				t.Errorf("tinyfish default base url = %q, want %q", p.DefaultBaseURL, tinyFishDefaultURL)
			}
		}
	}
	if !found {
		t.Fatal("TinyFish not present in WebSearchProviders")
	}
}

// --- helpers ---

type stubBackend struct {
	results []searchResult
	err     error
}

func (s *stubBackend) Search(context.Context, string, SearchOptions) ([]searchResult, error) {
	return s.results, s.err
}

func decodeJSONBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		t.Fatalf("decode incoming body: %v", err)
	}
	return m
}
