package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestCodeSearchFocusedQueryAndLimit(t *testing.T) {
	backend := &fakeSearchBackend{results: []searchResult{
		{Title: "strings.Contains", URL: "https://pkg.go.dev/strings", Snippet: "Contains reports whether substr is within s."},
		{Title: "Go strings", URL: "https://go.dev/ref/spec", Snippet: "String package docs."},
	}}
	tool := newCodeSearchToolWithBackend(backend)

	res := tool.Run(context.Background(), map[string]any{"query": "strings contains"})
	if res.Status != StatusOK {
		t.Fatalf("code_search failed: %s", res.Output)
	}
	if !strings.Contains(backend.gotQuery, "[code]") {
		t.Fatalf("expected [code] intent flag in query, got %q", backend.gotQuery)
	}
	if !strings.Contains(res.Output, "pkg.go.dev/strings") {
		t.Fatalf("expected result URL in output: %s", res.Output)
	}
	if backend.gotLimit < 1 {
		t.Fatalf("expected a positive limit, got %d", backend.gotLimit)
	}
}

func TestCodeSearchTokenBudgetControlsLimit(t *testing.T) {
	backend := &fakeSearchBackend{}
	tool := newCodeSearchToolWithBackend(backend)

	tool.Run(context.Background(), map[string]any{"query": "x", "tokensNum": 1000})
	low := backend.gotLimit
	tool.Run(context.Background(), map[string]any{"query": "x", "tokensNum": 50000})
	high := backend.gotLimit
	if high < low {
		t.Fatalf("expected more tokens -> higher limit; got low=%d high=%d", low, high)
	}
	if high > 10 {
		t.Fatalf("expected limit capped at 10, got %d", high)
	}
	// Out-of-range tokensNum clamps rather than erroring.
	res := tool.Run(context.Background(), map[string]any{"query": "x", "tokensNum": 999999})
	if res.Status != StatusOK {
		t.Fatalf("expected clamp, got %s", res.Output)
	}
}

func TestCodeSearchBehindNoResultsAndCleanError(t *testing.T) {
	noRes := newCodeSearchToolWithBackend(&fakeSearchBackend{})
	res := noRes.Run(context.Background(), map[string]any{"query": "nothing"})
	if res.Status != StatusOK || !strings.Contains(res.Output, "No results") {
		t.Fatalf("expected no-results message, got %s: %s", res.Status, res.Output)
	}

	// Backend error should surface and be redacted-ish (kept terse).
	secret := "[REDACTED:test_secret]"
	erring := newCodeSearchToolWithBackend(&fakeSearchBackend{err: fmt.Errorf("backend rejected key %s", secret)})
	res = erring.Run(context.Background(), map[string]any{"query": "boom"})
	if res.Status != StatusError {
		t.Fatalf("expected error status, got %s", res.Status)
	}
	if strings.Contains(res.Output, "secret-token") {
		t.Fatalf("backend error leaked secret: %s", res.Output)
	}
}

func TestCodeSearchNilBackendGuard(t *testing.T) {
	tool := newCodeSearchToolWithBackend(nil)
	res := tool.Run(context.Background(), map[string]any{"query": "q"})
	if res.Status != StatusError {
		t.Fatalf("expected error for nil backend, got %s", res.Status)
	}
	if !strings.Contains(res.Output, "backend") {
		t.Fatalf("expected backend message, got: %s", res.Output)
	}
}

func TestCodeSearchRequiresQuery(t *testing.T) {
	tool := newCodeSearchToolWithBackend(&fakeSearchBackend{})
	res := tool.Run(context.Background(), map[string]any{})
	if res.Status != StatusError || !strings.Contains(res.Output, "query") {
		t.Fatalf("expected query required error, got %s", res.Output)
	}
}
