package lsp

import (
	"context"
	"encoding/json"
	"fmt"
)

// NavOp is a supported LSP navigation operation.
type NavOp string

const (
	NavDefinition      NavOp = "definition"
	NavReferences      NavOp = "references"
	NavImplementation  NavOp = "implementation"
	NavWorkspaceSymbol NavOp = "workspace_symbol"
	// NavHover returns hover documentation/content for the symbol at a position.
	NavHover NavOp = "hover"
	// NavDocumentSymbol returns the symbol outline (functions, methods, types…)
	// inside a file.
	NavDocumentSymbol NavOp = "document_symbol"
	// NavPrepareCallHierarchy prepares the call hierarchy at a position; the
	// caller then requests incoming/outgoing calls.
	NavPrepareCallHierarchy NavOp = "prepare_call_hierarchy"
	// NavIncomingCalls / NavOutgoingCalls traverse the call hierarchy rooted at
	// the position.
	NavIncomingCalls NavOp = "incoming_calls"
	NavOutgoingCalls NavOp = "outgoing_calls"
)

// PositionOps are the ops that anchor at a file:line:character position.
func PositionOps() []NavOp {
	return []NavOp{NavDefinition, NavReferences, NavImplementation, NavHover, NavPrepareCallHierarchy, NavIncomingCalls, NavOutgoingCalls}
}

// NavResult carries whatever a navigation op produces. Only the fields relevant
// to the requested op are populated; the rest are zero.
type NavResult struct {
	Locations []Location          // definition/references/implementation
	Symbols   []SymbolResult      // workspace_symbol
	Hover     *HoverResult        // hover
	Outline   []SymbolOutline     // document_symbol
	CallItems []CallHierarchyItem // prepare_call_hierarchy
	CallEdges []CallHierarchyCall // incoming_calls / outgoing_calls
}

// HoverResult is the rendered hover content for a position.
type HoverResult struct {
	Contents string
	Range    *Range
}

// SymbolOutline is one document symbol: name, kind and its span, with nesting.
type SymbolOutline struct {
	Name     string
	Kind     string
	Range    Range
	Children []SymbolOutline
}

// CallHierarchyItem identifies a call-hierarchy participant: the enclosing
// symbol name, its kind, and where it lives.
type CallHierarchyItem struct {
	Name  string
	Kind  string
	URI   string
	Range Range
}

// CallHierarchyCall is one caller/callee edge plus the spans that reference it.
type CallHierarchyCall struct {
	Item       CallHierarchyItem
	FromRanges []Range
}

// SymbolResult is one workspace-symbol match (name + where it lives).
type SymbolResult struct {
	Name     string
	Kind     string
	Location Location
}

// NavRequest describes a navigation query. For the position-based ops
// (definition/references/implementation/hover/call-hierarchy) Path + Line +
// Character are required (1-based, as the agent sees file:line:col). For
// workspace_symbol only Query is used. IncludeDeclaration applies to references.
type NavRequest struct {
	Op                 NavOp
	Path               string
	Line               int // 1-based
	Character          int // 1-based
	Query              string
	Text               string // current file content, for didOpen sync
	IncludeDeclaration bool
}

// Navigate runs an LSP navigation request and returns the resulting data. A
// file whose extension has no available server, or a server lacking the
// capability, degrades to ok=false with no error — LSP is opportunistic. A
// genuine protocol/transport failure returns an error.
func (m *Manager) Navigate(ctx context.Context, req NavRequest) (NavResult, bool, error) {
	switch req.Op {
	case NavWorkspaceSymbol:
		return m.workspaceSymbol(ctx, req)
	case NavDefinition, NavReferences, NavImplementation:
		return m.positionNav(ctx, req)
	case NavHover:
		return m.hover(ctx, req)
	case NavDocumentSymbol:
		return m.documentSymbol(ctx, req)
	case NavPrepareCallHierarchy:
		return m.prepareCallHierarchy(ctx, req)
	case NavIncomingCalls, NavOutgoingCalls:
		return m.callHierarchyEdges(ctx, req)
	default:
		return NavResult{}, false, fmt.Errorf("lsp: unknown navigation op %q", req.Op)
	}
}

// session runs a server-aware request for the file, returning the session or a
// degraded (ok=false) signal when no server serves the file type.
func (m *Manager) session(ctx context.Context, req NavRequest) (*session, bool, error) {
	command, served := ServerFor(req.Path)
	if !served {
		return nil, false, nil
	}
	languageID, _ := LanguageID(req.Path)
	sess, err := m.sessionFor(ctx, command)
	if err != nil {
		if isServerUnavailable(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if err := sess.sync(ctx, m.absPath(req.Path), languageID, req.Text); err != nil {
		return nil, false, err
	}
	return sess, true, nil
}

func (m *Manager) positionNav(ctx context.Context, req NavRequest) (NavResult, bool, error) {
	sess, ok, err := m.session(ctx, req)
	if err != nil || !ok {
		return NavResult{}, ok, err
	}
	uri := PathToURI(m.absPath(req.Path))
	pos := Position{Line: maxZero(req.Line - 1), Character: maxZero(req.Character - 1)}
	method, params := positionRequest(req.Op, uri, pos, req.IncludeDeclaration)
	raw, err := sess.client.Call(ctx, method, params)
	if err != nil {
		return NavResult{}, false, err
	}
	locs, err := decodeLocations(raw)
	if err != nil {
		return NavResult{}, false, err
	}
	return NavResult{Locations: locs}, true, nil
}

func (m *Manager) hover(ctx context.Context, req NavRequest) (NavResult, bool, error) {
	sess, ok, err := m.session(ctx, req)
	if err != nil || !ok {
		return NavResult{}, ok, err
	}
	uri := PathToURI(m.absPath(req.Path))
	pos := Position{Line: maxZero(req.Line - 1), Character: maxZero(req.Character - 1)}
	raw, err := sess.client.Call(ctx, "textDocument/hover", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": pos.Line, "character": pos.Character},
	})
	if err != nil {
		return NavResult{}, false, err
	}
	hoverRaw := trimJSON(raw)
	if len(hoverRaw) == 0 || string(hoverRaw) == "null" {
		return NavResult{}, true, nil
	}
	var hover struct {
		Contents struct {
			Kind  string `json:"kind"`
			Value string `json:"value"`
		} `json:"contents"`
		Range *Range `json:"range"`
	}
	if err := json.Unmarshal(hoverRaw, &hover); err != nil {
		return NavResult{}, false, err
	}
	contents := hover.Contents.Value
	breakContent := renderHoverContents(contents, hoverRaw)
	return NavResult{Hover: &HoverResult{Contents: breakContent, Range: hover.Range}}, true, nil
}

// renderHoverContents coalesces hover `contents`, which LSP allows to be a
// string, an array of markup strings, or a {kind,value} object, into one line.
func renderHoverContents(objectValue string, raw json.RawMessage) string {
	if objectValue != "" {
		return objectValue
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString
	}
	return ""
}

func (m *Manager) documentSymbol(ctx context.Context, req NavRequest) (NavResult, bool, error) {
	sess, ok, err := m.session(ctx, req)
	if err != nil || !ok {
		return NavResult{}, ok, err
	}
	uri := PathToURI(m.absPath(req.Path))
	raw, err := sess.client.Call(ctx, "textDocument/documentSymbol", map[string]any{
		"textDocument": map[string]any{"uri": uri},
	})
	if err != nil {
		return NavResult{}, false, err
	}
	outline, err := decodeOutline(raw)
	if err != nil {
		return NavResult{}, false, err
	}
	return NavResult{Outline: outline}, true, nil
}

func (m *Manager) prepareCallHierarchy(ctx context.Context, req NavRequest) (NavResult, bool, error) {
	sess, ok, err := m.session(ctx, req)
	if err != nil || !ok {
		return NavResult{}, ok, err
	}
	uri := PathToURI(m.absPath(req.Path))
	pos := Position{Line: maxZero(req.Line - 1), Character: maxZero(req.Character - 1)}
	raw, err := sess.client.Call(ctx, "textDocument/prepareCallHierarchy", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": pos.Line, "character": pos.Character},
	})
	if err != nil {
		return NavResult{}, false, err
	}
	items, err := decodeCallHierarchyItems(raw)
	if err != nil {
		return NavResult{}, false, err
	}
	return NavResult{CallItems: items}, true, nil
}

func (m *Manager) callHierarchyEdges(ctx context.Context, req NavRequest) (NavResult, bool, error) {
	sess, ok, err := m.session(ctx, req)
	if err != nil || !ok {
		return NavResult{}, ok, err
	}
	uri := PathToURI(m.absPath(req.Path))
	pos := Position{Line: maxZero(req.Line - 1), Character: maxZero(req.Character - 1)}
	method := "textDocument/incomingCalls"
	if req.Op == NavOutgoingCalls {
		method = "textDocument/outgoingCalls"
	}
	rangeField := map[string]any{
		"start": map[string]any{"line": pos.Line, "character": pos.Character},
		"end":   map[string]any{"line": pos.Line, "character": pos.Character},
	}
	raw, err := sess.client.Call(ctx, method, map[string]any{
		"item": map[string]any{
			"uri":            uri,
			"range":          rangeField,
			"selectionRange": rangeField,
			"name":           "",
			"kind":           0,
		},
	})
	if err != nil {
		return NavResult{}, false, err
	}
	edges, err := decodeCallHierarchyCalls(raw)
	if err != nil {
		return NavResult{}, false, err
	}
	return NavResult{CallEdges: edges}, true, nil
}

func (m *Manager) workspaceSymbol(ctx context.Context, req NavRequest) (NavResult, bool, error) {
	sess, ok, err := m.session(ctx, req)
	if err != nil || !ok {
		return NavResult{}, ok, err
	}
	raw, err := sess.client.Call(ctx, "workspace/symbol", map[string]any{"query": req.Query})
	if err != nil {
		return NavResult{}, false, err
	}
	symbols, err := decodeSymbols(raw)
	if err != nil {
		return NavResult{}, false, err
	}
	return NavResult{Symbols: symbols}, true, nil
}

func positionRequest(op NavOp, uri string, pos Position, includeDecl bool) (string, any) {
	base := map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": pos.Line, "character": pos.Character},
	}
	switch op {
	case NavImplementation:
		return "textDocument/implementation", base
	case NavReferences:
		base["context"] = map[string]any{"includeDeclaration": includeDecl}
		return "textDocument/references", base
	case NavDefinition:
		return "textDocument/definition", base
	default:
		return "textDocument/definition", base
	}
}

// decodeLocations handles the three shapes definition/references can return: a
// single Location, an array of Location, or an array of LocationLink.
func decodeLocations(raw json.RawMessage) ([]Location, error) {
	trimmed := trimJSON(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, nil
	}
	if trimmed[0] == '[' {
		var locs []Location
		if err := json.Unmarshal(trimmed, &locs); err == nil && allHaveURI(locs) {
			return locs, nil
		}
		var links []locationLink
		if err := json.Unmarshal(trimmed, &links); err != nil {
			return nil, err
		}
		out := make([]Location, 0, len(links))
		for _, l := range links {
			out = append(out, Location{URI: l.TargetURI, Range: l.TargetRange})
		}
		return out, nil
	}
	var single Location
	if err := json.Unmarshal(trimmed, &single); err != nil {
		return nil, err
	}
	if single.URI == "" {
		return nil, nil
	}
	return []Location{single}, nil
}

type locationLink struct {
	TargetURI   string `json:"targetUri"`
	TargetRange Range  `json:"targetRange"`
}

func decodeOutline(raw json.RawMessage) ([]SymbolOutline, error) {
	trimmed := trimJSON(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, nil
	}
	// documentSymbol can return either []SymbolInformation or []DocumentSymbol
	// (which nests Children). Try the nested form first.
	var docSyms []struct {
		Name           string          `json:"name"`
		Kind           int             `json:"kind"`
		Range          Range           `json:"range"`
		SelectionRange Range           `json:"selectionRange"`
		Children       json.RawMessage `json:"children"`
	}
	if err := json.Unmarshal(trimmed, &docSyms); err == nil && len(docSyms) > 0 {
		out := make([]SymbolOutline, 0, len(docSyms))
		for _, ds := range docSyms {
			out = append(out, SymbolOutline{
				Name:     ds.Name,
				Kind:     symbolKindName(ds.Kind),
				Range:    ds.Range,
				Children: nestedOutline(ds.Children),
			})
		}
		return out, nil
	}
	// Flat SymbolInformation form: {name, kind, location}.
	var flat []struct {
		Name     string   `json:"name"`
		Kind     int      `json:"kind"`
		Location Location `json:"location"`
	}
	if err := json.Unmarshal(trimmed, &flat); err != nil {
		return nil, err
	}
	out := make([]SymbolOutline, 0, len(flat))
	for _, s := range flat {
		out = append(out, SymbolOutline{Name: s.Name, Kind: symbolKindName(s.Kind), Range: s.Location.Range})
	}
	return out, nil
}

func nestedOutline(raw json.RawMessage) []SymbolOutline {
	trimmed := trimJSON(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}
	var children []struct {
		Name     string          `json:"name"`
		Kind     int             `json:"kind"`
		Range    Range           `json:"range"`
		Children json.RawMessage `json:"children"`
	}
	if err := json.Unmarshal(trimmed, &children); err != nil {
		return nil
	}
	out := make([]SymbolOutline, 0, len(children))
	for _, c := range children {
		out = append(out, SymbolOutline{Name: c.Name, Kind: symbolKindName(c.Kind), Range: c.Range, Children: nestedOutline(c.Children)})
	}
	return out
}

type chItem struct {
	Name           string `json:"name"`
	Kind           int    `json:"kind"`
	URI            string `json:"uri"`
	Range          Range  `json:"range"`
	SelectionRange Range  `json:"selectionRange"`
}

func decodeCallHierarchyItems(raw json.RawMessage) ([]CallHierarchyItem, error) {
	trimmed := trimJSON(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, nil
	}
	if trimmed[0] == '[' {
		var items []chItem
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return nil, err
		}
		out := make([]CallHierarchyItem, 0, len(items))
		for _, it := range items {
			out = append(out, CallHierarchyItem{Name: it.Name, Kind: symbolKindName(it.Kind), URI: it.URI, Range: it.Range})
		}
		return out, nil
	}
	var single chItem
	if err := json.Unmarshal(trimmed, &single); err != nil {
		return nil, err
	}
	return []CallHierarchyItem{{Name: single.Name, Kind: symbolKindName(single.Kind), URI: single.URI, Range: single.Range}}, nil
}

func decodeCallHierarchyCalls(raw json.RawMessage) ([]CallHierarchyCall, error) {
	trimmed := trimJSON(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, nil
	}
	var calls []struct {
		From       chItem  `json:"from"`
		FromRanges []Range `json:"fromRanges"`
		To         chItem  `json:"to"`
	}
	if err := json.Unmarshal(trimmed, &calls); err != nil {
		return nil, err
	}
	out := make([]CallHierarchyCall, 0, len(calls))
	for _, c := range calls {
		item := c.To
		if item.Name == "" {
			item = c.From
		}
		out = append(out, CallHierarchyCall{
			Item:       CallHierarchyItem{Name: item.Name, Kind: symbolKindName(item.Kind), URI: item.URI, Range: item.Range},
			FromRanges: c.FromRanges,
		})
	}
	return out, nil
}

type workspaceSymbol struct {
	Name     string   `json:"name"`
	Kind     int      `json:"kind"`
	Location Location `json:"location"`
}

func decodeSymbols(raw json.RawMessage) ([]SymbolResult, error) {
	trimmed := trimJSON(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, nil
	}
	var syms []workspaceSymbol
	if err := json.Unmarshal(trimmed, &syms); err != nil {
		return nil, err
	}
	out := make([]SymbolResult, 0, len(syms))
	for _, s := range syms {
		out = append(out, SymbolResult{Name: s.Name, Kind: symbolKindName(s.Kind), Location: s.Location})
	}
	return out, nil
}

func allHaveURI(locs []Location) bool {
	for _, l := range locs {
		if l.URI == "" {
			return false
		}
	}
	return len(locs) > 0
}

func maxZero(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

func trimJSON(raw json.RawMessage) json.RawMessage {
	s := 0
	for s < len(raw) && (raw[s] == ' ' || raw[s] == '\n' || raw[s] == '\t' || raw[s] == '\r') {
		s++
	}
	return raw[s:]
}

// symbolKindName maps the LSP SymbolKind enum to a short label.
func symbolKindName(kind int) string {
	switch kind {
	case 5:
		return "class"
	case 6:
		return "method"
	case 8:
		return "field"
	case 9:
		return "constructor"
	case 11:
		return "interface"
	case 12:
		return "function"
	case 13:
		return "variable"
	case 14:
		return "constant"
	case 23:
		return "struct"
	case 26:
		return "enum"
	default:
		return "symbol"
	}
}
