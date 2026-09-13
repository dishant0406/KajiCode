package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestProgressTimeoutResetsOnProgress(t *testing.T) {
	cancelled := make(chan struct{})
	progress := newProgressTimeout(40*time.Millisecond, func(error) { close(cancelled) })
	defer progress.stop()

	// Keep resetting faster than the timeout; it must not fire.
	for i := 0; i < 5; i++ {
		time.Sleep(20 * time.Millisecond)
		progress.reset()
	}
	select {
	case <-cancelled:
		t.Fatal("timeout fired despite progress resets")
	default:
	}

	// Stop resetting; it must now fire.
	select {
	case <-cancelled:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout did not fire after progress stopped")
	}
}

func TestProgressRegistryTouchResetsMatchingToken(t *testing.T) {
	var registry progressRegistry
	cancelled := make(chan struct{})
	progress := newProgressTimeout(40*time.Millisecond, func(error) { close(cancelled) })
	registry.addProgress("tok", progress)
	defer progress.stop()

	time.Sleep(20 * time.Millisecond)
	registry.touchProgress("tok")   // matching token resets
	registry.touchProgress("other") // unknown token is ignored
	select {
	case <-cancelled:
		t.Fatal("timer fired despite a matching progress touch")
	case <-time.After(30 * time.Millisecond):
	}
}

func TestProgressTokenFromParams(t *testing.T) {
	if got := progressTokenFromParams(json.RawMessage(`{"progressToken":"abc"}`)); got != "abc" {
		t.Fatalf("token = %q, want abc", got)
	}
	if got := progressTokenFromParams(nil); got != "" {
		t.Fatalf("nil params token = %q, want empty", got)
	}
	if got := progressTokenFromParams(json.RawMessage(`not json`)); got != "" {
		t.Fatalf("bad json token = %q, want empty", got)
	}
}

func TestClientListToolsPaginates(t *testing.T) {
	pages := map[string][]RemoteTool{
		"":   {{Name: "a"}},
		"p2": {{Name: "b"}},
		"p3": {{Name: "c"}},
	}
	calls := 0
	request := func(_ context.Context, _ string, params any, target any) error {
		calls++
		cursor := ""
		if decoded, ok := params.(map[string]any); ok {
			cursor, _ = decoded["cursor"].(string)
		}
		result := target.(*struct {
			Tools      []RemoteTool `json:"tools"`
			NextCursor string       `json:"nextCursor"`
		})
		result.Tools = pages[cursor]
		switch cursor {
		case "":
			result.NextCursor = "p2"
		case "p2":
			result.NextCursor = "p3"
		}
		return nil
	}

	tools, err := clientListTools(context.Background(), request)
	if err != nil {
		t.Fatalf("clientListTools: %v", err)
	}
	if len(tools) != 3 || calls != 3 {
		t.Fatalf("tools = %d, calls = %d, want 3/3", len(tools), calls)
	}
}

func TestClientListToolsTolerantOutputSchema(t *testing.T) {
	// A server that sends a non-object outputSchema (here a string) must not fail
	// the tools/list decode.
	raw := `{"tools":[{"name":"t","inputSchema":{"type":"object"},"outputSchema":"weird"}]}`
	request := func(_ context.Context, _ string, _ any, target any) error {
		return json.Unmarshal([]byte(raw), target)
	}
	tools, err := clientListTools(context.Background(), request)
	if err != nil {
		t.Fatalf("clientListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "t" {
		t.Fatalf("unexpected tools: %#v", tools)
	}
}

func TestClientListToolsGuardsRepeatedCursor(t *testing.T) {
	calls := 0
	request := func(_ context.Context, _ string, _ any, target any) error {
		calls++
		result := target.(*struct {
			Tools      []RemoteTool `json:"tools"`
			NextCursor string       `json:"nextCursor"`
		})
		result.Tools = []RemoteTool{{Name: "a"}}
		result.NextCursor = "same" // never advances
		return nil
	}
	if _, err := clientListTools(context.Background(), request); err != nil {
		t.Fatalf("clientListTools: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2 (one repeat detected then stop)", calls)
	}
}
