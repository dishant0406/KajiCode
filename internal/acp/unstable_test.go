package acp

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestACPDocumentSyncAndNesSuggest(t *testing.T) {
	deps := testDeps(t)
	var got NesSuggestInput
	deps.SuggestNes = func(_ context.Context, in NesSuggestInput) (*NesEditSuggestion, error) {
		got = in
		return &NesEditSuggestion{
			ID:    "s1",
			URI:   in.URI,
			Edits: []NesTextEdit{{Range: Range{Start: Position{Line: 1, Character: 0}, End: Position{Line: 1, Character: 3}}, NewText: "print(1)"}},
		}, nil
	}
	h := newHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var newRes NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir(), McpServers: []McpServer{}}, &newRes); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	// didOpen establishes the buffer.
	if err := h.client.Notify(MethodDocumentDidOpen, DidOpenDocumentParams{
		SessionID: newRes.SessionID, URI: "file:///a.go", LanguageID: "go", Version: 1, Text: "package a\n\n",
	}); err != nil {
		t.Fatalf("didOpen: %v", err)
	}
	// didChange (full sync: whole text) updates it.
	if err := h.client.Notify(MethodDocumentDidChange, DidChangeDocumentParams{
		SessionID: newRes.SessionID, URI: "file:///a.go", Version: 2,
		ContentChanges: []TextDocumentContentChangeEvent{{Text: "package a\nfoo\n"}},
	}); err != nil {
		t.Fatalf("didChange: %v", err)
	}
	waitFor(t, func() bool {
		sess := acpTestSession(h, newRes.SessionID)
		return sess != nil && docText(sess, "file:///a.go") == "package a\nfoo\n"
	})

	var suggest SuggestNesResult
	if err := h.client.Call(ctx, MethodNesSuggest, SuggestNesParams{
		SessionID: newRes.SessionID, URI: "file:///a.go", Version: 2,
		Position: Position{Line: 1, Character: 0}, TriggerKind: "manual",
	}, &suggest); err != nil {
		t.Fatalf("nes/suggest: %v", err)
	}
	if len(suggest.Suggestions) != 1 || suggest.Suggestions[0].Edits[0].NewText != "print(1)" {
		t.Fatalf("suggestions = %+v", suggest.Suggestions)
	}
	if got.Text != "package a\nfoo\n" || got.LanguageID != "go" {
		t.Fatalf("SuggestNes input = %+v", got)
	}
	// didClose drops the buffer.
	if err := h.client.Notify(MethodDocumentDidClose, DidCloseDocumentParams{SessionID: newRes.SessionID, URI: "file:///a.go"}); err != nil {
		t.Fatalf("didClose: %v", err)
	}
	waitFor(t, func() bool {
		sess := acpTestSession(h, newRes.SessionID)
		return sess != nil && !docExists(sess, "file:///a.go")
	})
	// No buffered doc -> empty suggestions, no error.
	var empty SuggestNesResult
	if err := h.client.Call(ctx, MethodNesSuggest, SuggestNesParams{SessionID: newRes.SessionID, URI: "file:///a.go", Version: 3, Position: Position{}, TriggerKind: "manual"}, &empty); err != nil {
		t.Fatalf("nes/suggest after close: %v", err)
	}
	if len(empty.Suggestions) != 0 {
		t.Fatalf("expected no suggestions, got %+v", empty.Suggestions)
	}
}

func TestACPRangeEditAppliesIncrementalChange(t *testing.T) {
	text := "abc\ndef\n"
	got := applyRangeEdit(text, Range{Start: Position{Line: 1, Character: 0}, End: Position{Line: 1, Character: 3}}, "XYZ")
	if got != "abc\nXYZ\n" {
		t.Fatalf("applyRangeEdit = %q", got)
	}
	if byteOffset("abc\ndef", Position{Line: 1, Character: 2}) != 6 {
		t.Fatalf("byteOffset = %d", byteOffset("abc\ndef", Position{Line: 1, Character: 2}))
	}
	// A stale range falls back to a whole-text replace.
	if got := applyRangeEdit("x", Range{Start: Position{Line: 9, Character: 0}, End: Position{Line: 9, Character: 0}}, "y"); got != "y" {
		t.Fatalf("stale range = %q", got)
	}
}

func TestACPInitializeAdvertisesUnstableCaps(t *testing.T) {
	h := newHarness(t, testDeps(t))
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var res InitializeResult
	if err := h.client.Call(ctx, MethodInitialize, InitializeParams{ProtocolVersion: ProtocolVersion}, &res); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	caps := res.AgentCapabilities
	if caps.Providers == nil {
		t.Error("providers capability not advertised")
	}
	if caps.PositionEncoding != "utf-8" {
		t.Errorf("positionEncoding = %q", caps.PositionEncoding)
	}
	if caps.Nes == nil || caps.Nes.Events == nil || caps.Nes.Events.Document == nil {
		t.Fatalf("nes events not advertised: %+v", caps.Nes)
	}
	if caps.Nes.Events.Document.DidChange == nil || caps.Nes.Events.Document.DidChange.SyncKind != "full" {
		t.Fatalf("didChange syncKind not full: %+v", caps.Nes.Events.Document)
	}
}

func TestACPProvidersListSetDisable(t *testing.T) {
	deps := testDeps(t)
	deps.ListProviders = func() ([]ProviderInfo, error) {
		return []ProviderInfo{{ProviderID: "p1", Supported: []string{"openai"}, Required: true,
			Current: &ProviderCurrentInfo{APIType: "openai", BaseURL: "http://x/v1"}}}, nil
	}
	var setID, disabledID string
	deps.SetProvider = func(id, apiType, baseURL string, headers map[string]string) error { setID = id; return nil }
	deps.DisableProvider = func(id string) error { disabledID = id; return nil }
	h := newHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var list ListProvidersResult
	if err := h.client.Call(ctx, MethodProvidersList, json.RawMessage("{}"), &list); err != nil {
		t.Fatalf("providers/list: %v", err)
	}
	if len(list.Providers) != 1 || list.Providers[0].Current.BaseURL != "http://x/v1" {
		t.Fatalf("providers = %+v", list.Providers)
	}
	if err := h.client.Call(ctx, MethodProvidersSet, SetProviderParams{ProviderID: "p1", APIType: "openai", BaseURL: "http://y/v1"}, &SetProviderResult{}); err != nil {
		t.Fatalf("providers/set: %v", err)
	}
	if setID != "p1" {
		t.Fatalf("SetProvider got %q", setID)
	}
	if err := h.client.Call(ctx, MethodProvidersDisable, DisableProviderParams{ProviderID: "p1"}, &DisableProviderResult{}); err != nil {
		t.Fatalf("providers/disable: %v", err)
	}
	if disabledID != "p1" {
		t.Fatalf("DisableProvider got %q", disabledID)
	}
	// Missing providerId is rejected.
	if err := h.client.Call(ctx, MethodProvidersSet, SetProviderParams{BaseURL: "x"}, &SetProviderResult{}); err == nil {
		t.Fatal("empty providerId must be rejected")
	}
}

// helpers to observe session state from tests in the same package.

func acpTestSession(h *clientHarness, id string) *acpSession { return h.agent.session(id) }

func docText(s *acpSession, uri string) string {
	doc, ok := s.document(uri)
	if !ok {
		return ""
	}
	return doc.text
}

func docExists(s *acpSession, uri string) bool {
	_, ok := s.document(uri)
	return ok
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}

func TestACPByteOffsetBounds(t *testing.T) {
	text := "abc\ndef"
	// character within the line is accepted.
	if byteOffset(text, Position{Line: 0, Character: 2}) != 2 {
		t.Fatalf("in-line offset wrong")
	}
	// a character past the end of its line is rejected (would cross the newline).
	if byteOffset(text, Position{Line: 0, Character: 5}) != -1 {
		t.Fatalf("past-line-end character must be rejected")
	}
	// character exactly at the line end (the newline byte) is allowed.
	if byteOffset(text, Position{Line: 0, Character: 3}) != 3 {
		t.Fatalf("line-end character rejected")
	}
	if byteOffset(text, Position{Line: 0, Character: -1}) != -1 {
		t.Fatalf("negative character must be rejected")
	}
}

func TestACPProvidersDisableRejectsEmptyAndOnly(t *testing.T) {
	deps := testDeps(t)
	deps.DisableProvider = func(string) error { return nil }
	h := newHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.client.Call(ctx, MethodProvidersDisable, DisableProviderParams{}, &DisableProviderResult{}); err == nil {
		t.Fatal("empty providerId must be rejected")
	}
}
