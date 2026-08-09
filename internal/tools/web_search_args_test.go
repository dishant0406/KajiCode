package tools

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"
)

// optsCaptureBackend records the SearchOptions it receives so tests can assert
// that Run propagates the new args (type/livecrawl/context_max_characters).
type optsCaptureBackend struct {
	opts    SearchOptions
	results []searchResult
	err     error
}

func (c *optsCaptureBackend) Search(_ context.Context, _ string, opts SearchOptions) ([]searchResult, error) {
	c.opts = opts
	return c.results, c.err
}

func TestWebSearchPropagatesNewArgsToOptions(t *testing.T) {
	backend := &optsCaptureBackend{results: []searchResult{{Title: "T", URL: "https://t.dev"}}}
	tool := newWebSearchToolWithBackend(backend)
	res := tool.Run(context.Background(), map[string]any{
		"query":                  "q",
		"type":                   "deep",
		"livecrawl":              "preferred",
		"context_max_characters": 2500,
	})
	if res.Status != StatusOK {
		t.Fatalf("status = %v: %s", res.Status, res.Output)
	}
	if backend.opts.Type != "deep" {
		t.Errorf("opts.Type = %q, want deep", backend.opts.Type)
	}
	if backend.opts.LiveCrawl != "preferred" {
		t.Errorf("opts.LiveCrawl = %q, want preferred", backend.opts.LiveCrawl)
	}
	if backend.opts.ContextMax != 2500 {
		t.Errorf("opts.ContextMax = %d, want 2500", backend.opts.ContextMax)
	}
	if backend.opts.CurrentYear != time.Now().Year() {
		t.Errorf("opts.CurrentYear = %d, want %d", backend.opts.CurrentYear, time.Now().Year())
	}
}

func TestWebSearchRejectsInvalidEnumArgs(t *testing.T) {
	tool := newWebSearchToolWithBackend(&optsCaptureBackend{results: []searchResult{{Title: "T", URL: "https://t.dev"}}})
	for _, bad := range []map[string]any{
		{"query": "q", "type": "bogus"},
		{"query": "q", "livecrawl": "sometimes"},
	} {
		res := tool.Run(context.Background(), bad)
		if res.Status != StatusError {
			t.Fatalf("args %#v: expected StatusError, got %v (%q)", bad, res.Status, res.Output)
		}
	}
}

func TestWebSearchRendersContent(t *testing.T) {
	backend := &optsCaptureBackend{results: []searchResult{
		{Title: "T", URL: "https://t.dev", Content: "full page body here"},
	}}
	res := newWebSearchToolWithBackend(backend).Run(context.Background(), map[string]any{"query": "q"})
	if res.Status != StatusOK {
		t.Fatalf("status = %v: %s", res.Status, res.Output)
	}
	if !strings.Contains(res.Output, "<content> full page body here </content>") {
		t.Errorf("full content not rendered:\n%s", res.Output)
	}
}

func TestWebSearchDescriptionEmbedsCurrentYear(t *testing.T) {
	desc := webSearchDescription()
	if !strings.Contains(desc, "The current year is") {
		t.Errorf("description should anchor the current year:\n%s", desc)
	}
	want := time.Now().Year()
	if !strings.Contains(desc, "AI news "+strconv.Itoa(want)) {
		t.Errorf("description should include %d example:\n%s", want, desc)
	}
}

func TestWebSearchSchemaIncludesNewArgs(t *testing.T) {
	tool := newWebSearchToolWithBackend(&optsCaptureBackend{})
	props := tool.Parameters().Properties
	for _, key := range []string{"type", "livecrawl", "context_max_characters"} {
		if _, ok := props[key]; !ok {
			t.Errorf("schema missing key %s", key)
		}
	}
	if e := props["type"].Enum; len(e) != 3 {
		t.Errorf("type enum = %v, want 3 options", e)
	}
}

func TestRankAndTrimWebSearchResults(t *testing.T) {
	in := []searchResult{
		{Title: "low", URL: "https://a", Score: 0.05},
		{Title: "high", URL: "https://b", Score: 0.9},
		{Title: "mid", URL: "https://c", Score: 0.4},
	}
	got := rankAndTrimWebSearchResults(in, 2)
	if len(got) != 2 {
		t.Fatalf("want 2 after threshold+limit, got %d: %#v", len(got), got)
	}
	if got[0].Title != "high" || got[1].Title != "mid" {
		t.Errorf("order = %q,%q want high,mid", got[0].Title, got[1].Title)
	}

	// No scores → order preserved, only limit applies.
	in = []searchResult{
		{Title: "first", URL: "https://a"},
		{Title: "second", URL: "https://b"},
		{Title: "third", URL: "https://c"},
	}
	got = rankAndTrimWebSearchResults(in, 2)
	if len(got) != 2 || got[0].Title != "first" || got[1].Title != "second" {
		t.Errorf("no-score order should be preserved, got %#v", got)
	}
}
