package acp

import (
	"context"
	"encoding/json"
	"strings"
)

// v1-unstable surface: editor->agent document sync, next-edit suggestions, and
// provider configuration. These are spec-optional and gated by capabilities
// KajiCode advertises in initialize; clients that don't speak them ignore the
// capability and never call the methods.

// ---- document sync (document/didOpen|didChange|didClose|didSave|didFocus) ----

// acpDocument is the last-known buffer for one open URI in a session.
type acpDocument struct {
	languageID string
	version    int
	text       string
}

// setDocument records an opened/replaced buffer.
func (s *acpSession) setDocument(uri string, version int, languageID, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.docs == nil {
		s.docs = map[string]acpDocument{}
	}
	existing := s.docs[uri]
	if languageID == "" {
		languageID = existing.languageID
	}
	s.docs[uri] = acpDocument{languageID: languageID, version: version, text: text}
}

// applyDocumentChange updates a document with the client's content changes.
// KajiCode advertises "full" sync, so each change carries the whole document;
// an incremental change (a range edit) is also applied so a client that ignores
// the advertised syncKind still works.
func (s *acpSession) applyDocumentChange(uri string, version int, changes []TextDocumentContentChangeEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.docs == nil {
		s.docs = map[string]acpDocument{}
	}
	doc := s.docs[uri]
	for _, change := range changes {
		if change.Range == nil {
			doc.text = change.Text
			continue
		}
		doc.text = applyRangeEdit(doc.text, *change.Range, change.Text)
	}
	doc.version = version
	s.docs[uri] = doc
}

func (s *acpSession) removeDocument(uri string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.docs, uri)
}

func (s *acpSession) document(uri string) (acpDocument, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, ok := s.docs[uri]
	return doc, ok
}

// applyRangeEdit replaces the text in [r.start, r.end) with newText. Positions
// use the advertised utf-8 encoding: line is 0-based, character is a byte offset
// within the line.
func applyRangeEdit(text string, r Range, newText string) string {
	start := byteOffset(text, r.Start)
	end := byteOffset(text, r.End)
	if start < 0 || end < start || end > len(text) {
		// A stale/invalid range: treat the change as a whole-document replace so
		// the buffer never ends up corrupted.
		return newText
	}
	return text[:start] + newText + text[end:]
}

// byteOffset converts a line/character position to an absolute byte offset.
func byteOffset(text string, pos Position) int {
	if pos.Line < 0 || pos.Character < 0 {
		return -1
	}
	offset := 0
	for line := 0; line < pos.Line; line++ {
		next := strings.IndexByte(text[offset:], '\n')
		if next < 0 {
			return -1
		}
		offset += next + 1
	}
	// character is a byte offset within the line; it must not cross the newline.
	lineEnd := len(text)
	if next := strings.IndexByte(text[offset:], '\n'); next >= 0 {
		lineEnd = offset + next
	}
	if pos.Character > lineEnd-offset {
		return -1
	}
	return offset + pos.Character
}

func (a *Agent) handleDidOpen(_ context.Context, params json.RawMessage) {
	var p DidOpenDocumentParams
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	if sess := a.session(p.SessionID); sess != nil {
		sess.setDocument(p.URI, p.Version, p.LanguageID, p.Text)
	}
}

func (a *Agent) handleDidChange(_ context.Context, params json.RawMessage) {
	var p DidChangeDocumentParams
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	if sess := a.session(p.SessionID); sess != nil {
		sess.applyDocumentChange(p.URI, p.Version, p.ContentChanges)
	}
}

func (a *Agent) handleDidClose(_ context.Context, params json.RawMessage) {
	var p DidCloseDocumentParams
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	if sess := a.session(p.SessionID); sess != nil {
		sess.removeDocument(p.URI)
	}
}

// didFocus carries the cursor/visible range. KajiCode has no separate focus
// state (NES uses the position carried on each nes/suggest request), so this is
// a validated no-op.
func (a *Agent) handleDidFocus(_ context.Context, params json.RawMessage) {
	var p DidFocusDocumentParams
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
}

// didSave carries no content; the latest didChange already holds the buffer. It
// still validates the session so an unknown session is not silently accepted.
func (a *Agent) handleDidSave(_ context.Context, params json.RawMessage) {
	var p DidSaveDocumentParams
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	_ = a.session(p.SessionID)
}

// ---- next edit suggestions (nes/*) ----

func (a *Agent) handleNesStart(_ context.Context, params json.RawMessage) (any, error) {
	var p StartNesParams
	if len(params) > 0 {
		_ = json.Unmarshal(params, &p)
	}
	return StartNesResult{}, nil
}

func (a *Agent) handleNesClose(_ context.Context, params json.RawMessage) (any, error) {
	var p CloseNesParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, RPCError(codeInvalidParams, "invalid nes/close params")
	}
	return CloseNesResult{}, nil
}

func (a *Agent) handleNesAccept(_ context.Context, params json.RawMessage) {
	var p AcceptNesParams
	_ = json.Unmarshal(params, &p)
}

func (a *Agent) handleNesReject(_ context.Context, params json.RawMessage) {
	var p RejectNesParams
	_ = json.Unmarshal(params, &p)
}

// handleNesSuggest builds a next-edit suggestion for the buffered document at the
// requested position. It uses the session's own document buffer (kept current by
// document/didChange), so it needs no filesystem read, and asks the configured
// provider for the edit through Deps.SuggestNes.
func (a *Agent) handleNesSuggest(ctx context.Context, params json.RawMessage) (any, error) {
	var p SuggestNesParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, RPCError(codeInvalidParams, "invalid nes/suggest params")
	}
	sess := a.session(p.SessionID)
	if sess == nil {
		return nil, RPCError(codeInvalidParams, "unknown session: "+p.SessionID)
	}
	if a.deps.SuggestNes == nil {
		return SuggestNesResult{Suggestions: []NesSuggestion{}}, nil
	}
	doc, ok := sess.document(p.URI)
	if !ok {
		// No buffered document: nothing to suggest from.
		return SuggestNesResult{Suggestions: []NesSuggestion{}}, nil
	}
	suggestion, err := a.deps.SuggestNes(ctx, NesSuggestInput{
		Cwd:         sess.cwd,
		URI:         p.URI,
		LanguageID:  doc.languageID,
		Text:        doc.text,
		Version:     p.Version,
		Position:    p.Position,
		TriggerKind: p.TriggerKind,
	})
	if err != nil {
		return nil, RPCError(codeInternalError, "nes/suggest: "+err.Error())
	}
	if suggestion == nil {
		return SuggestNesResult{Suggestions: []NesSuggestion{}}, nil
	}
	return SuggestNesResult{Suggestions: []NesSuggestion{*suggestion}}, nil
}

// NesSuggestInput is the provider-facing request for one suggestion.
type NesSuggestInput struct {
	Cwd         string
	URI         string
	LanguageID  string
	Text        string
	Version     int
	Position    Position
	TriggerKind string
}

// ---- provider configuration (providers/*) ----

func (a *Agent) handleProvidersList(_ context.Context, _ json.RawMessage) (any, error) {
	if a.deps.ListProviders == nil {
		return ListProvidersResult{Providers: []ProviderInfo{}}, nil
	}
	providers, err := a.deps.ListProviders()
	if err != nil {
		return nil, RPCError(codeInternalError, "providers/list: "+err.Error())
	}
	if providers == nil {
		providers = []ProviderInfo{}
	}
	return ListProvidersResult{Providers: providers}, nil
}

func (a *Agent) handleProvidersSet(_ context.Context, params json.RawMessage) (any, error) {
	var p SetProviderParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, RPCError(codeInvalidParams, "invalid providers/set params")
	}
	if strings.TrimSpace(p.ProviderID) == "" {
		return nil, RPCError(codeInvalidParams, "providers/set requires providerId")
	}
	if a.deps.SetProvider == nil {
		return nil, RPCError(codeMethodNotFound, "providers/set not supported")
	}
	if err := a.deps.SetProvider(p.ProviderID, p.APIType, p.BaseURL, p.Headers); err != nil {
		return nil, RPCError(codeInternalError, "providers/set: "+err.Error())
	}
	return SetProviderResult{}, nil
}

func (a *Agent) handleProvidersDisable(_ context.Context, params json.RawMessage) (any, error) {
	var p DisableProviderParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, RPCError(codeInvalidParams, "invalid providers/disable params")
	}
	if strings.TrimSpace(p.ProviderID) == "" {
		return nil, RPCError(codeInvalidParams, "providers/disable requires providerId")
	}
	if a.deps.DisableProvider == nil {
		return nil, RPCError(codeMethodNotFound, "providers/disable not supported")
	}
	if err := a.deps.DisableProvider(p.ProviderID); err != nil {
		return nil, RPCError(codeInternalError, "providers/disable: "+err.Error())
	}
	return DisableProviderResult{}, nil
}
