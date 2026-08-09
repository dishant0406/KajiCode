package lsp

import (
	"encoding/json"
	"testing"
)

func TestDecodeLocationsArrayOfLocation(t *testing.T) {
	raw := json.RawMessage(`[{"uri":"file:///a.go","range":{"start":{"line":4,"character":2},"end":{"line":4,"character":8}}}]`)
	locs, err := decodeLocations(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(locs) != 1 || locs[0].URI != "file:///a.go" || locs[0].Range.Start.Line != 4 {
		t.Fatalf("unexpected locations: %#v", locs)
	}
}

func TestDecodeLocationsSingleLocation(t *testing.T) {
	raw := json.RawMessage(`{"uri":"file:///b.go","range":{"start":{"line":1,"character":0},"end":{"line":1,"character":3}}}`)
	locs, err := decodeLocations(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(locs) != 1 || locs[0].URI != "file:///b.go" {
		t.Fatalf("unexpected single location: %#v", locs)
	}
}

func TestDecodeLocationsLocationLink(t *testing.T) {
	// definition can return LocationLink (targetUri/targetRange) instead of Location.
	raw := json.RawMessage(`[{"targetUri":"file:///c.go","targetRange":{"start":{"line":9,"character":1},"end":{"line":9,"character":5}}}]`)
	locs, err := decodeLocations(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(locs) != 1 || locs[0].URI != "file:///c.go" || locs[0].Range.Start.Line != 9 {
		t.Fatalf("LocationLink not converted: %#v", locs)
	}
}

func TestDecodeLocationsNull(t *testing.T) {
	for _, raw := range []string{`null`, ``, `   `, `[]`} {
		locs, err := decodeLocations(json.RawMessage(raw))
		if err != nil {
			t.Fatalf("decode(%q): %v", raw, err)
		}
		if len(locs) != 0 {
			t.Fatalf("decode(%q) = %#v, want empty", raw, locs)
		}
	}
}

func TestDecodeSymbols(t *testing.T) {
	raw := json.RawMessage(`[{"name":"Run","kind":12,"location":{"uri":"file:///loop.go","range":{"start":{"line":99,"character":0},"end":{"line":99,"character":3}}}}]`)
	syms, err := decodeSymbols(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(syms) != 1 || syms[0].Name != "Run" || syms[0].Kind != "function" {
		t.Fatalf("unexpected symbols: %#v", syms)
	}
}

func TestNavigateUnknownOp(t *testing.T) {
	m := NewManager(t.TempDir())
	_, ok, err := m.Navigate(nil, NavRequest{Op: "bogus", Path: "x.go"})
	if err == nil {
		t.Fatal("expected an error for an unknown nav op")
	}
	if ok {
		t.Fatal("ok should be false for an unknown op")
	}
}

func TestNavigateUnsupportedExtensionDegrades(t *testing.T) {
	// A file type with no configured server degrades to ok=false, no error.
	m := NewManager(t.TempDir())
	_, ok, err := m.Navigate(nil, NavRequest{Op: NavDefinition, Path: "notes.unknownext", Line: 1, Character: 1})
	if err != nil {
		t.Fatalf("unsupported extension should not error, got %v", err)
	}
	if ok {
		t.Fatal("unsupported extension should degrade to ok=false")
	}
}

func TestSymbolKindName(t *testing.T) {
	cases := map[int]string{5: "class", 11: "interface", 12: "function", 23: "struct", 999: "symbol", 9: "constructor", 26: "enum", 6: "method", 8: "field", 14: "constant"}
	for kind, want := range cases {
		if got := symbolKindName(kind); got != want {
			t.Fatalf("symbolKindName(%d) = %q, want %q", kind, got, want)
		}
	}
}

func TestDecodeHoverObjectContents(t *testing.T) {
	raw := json.RawMessage(`{"contents":{"kind":"markdown","value":"**Run** is the entry point."},"range":{"start":{"line":1,"character":0},"end":{"line":1,"character":3}}}`)
	var hover struct {
		Contents struct {
			Kind  string `json:"kind"`
			Value string `json:"value"`
		} `json:"contents"`
		Range *Range `json:"range"`
	}
	if err := json.Unmarshal(raw, &hover); err != nil {
		t.Fatalf("unmarshal hover: %v", err)
	}
	if got := renderHoverContents(hover.Contents.Value, raw); got != "**Run** is the entry point." {
		t.Fatalf("renderHoverContents = %q", got)
	}
}

func TestDecodeOutlineNestedDocumentSymbols(t *testing.T) {
	raw := json.RawMessage(`[{"name":"Run","kind":6,"range":{"start":{"line":1,"character":0},"end":{"line":1,"character":8}},"selectionRange":{"start":{"line":1,"character":0},"end":{"line":1,"character":3}},"children":[{"name":"start","kind":12,"range":{"start":{"line":2,"character":0},"end":{"line":2,"character":5}},"children":[]}]}]`)
	outline, err := decodeOutline(raw)
	if err != nil {
		t.Fatalf("decodeOutline: %v", err)
	}
	if len(outline) != 1 || outline[0].Name != "Run" || len(outline[0].Children) != 1 || outline[0].Children[0].Name != "start" {
		t.Fatalf("unexpected outline: %#v", outline)
	}
}

func TestDecodeOutlineFlatSymbolInformation(t *testing.T) {
	raw := json.RawMessage(`[{"name":"helper","kind":12,"location":{"uri":"file:///a.go","range":{"start":{"line":3,"character":0},"end":{"line":3,"character":6}}}}]`)
	outline, err := decodeOutline(raw)
	if err != nil {
		t.Fatalf("decodeOutline flat: %v", err)
	}
	if len(outline) != 1 || outline[0].Name != "helper" || outline[0].Kind != "function" {
		t.Fatalf("unexpected flat outline: %#v", outline)
	}
}

func TestDecodeCallHierarchyItemsPanelsAndEdges(t *testing.T) {
	single := json.RawMessage(`{"name":"Run","kind":6,"uri":"file:///loop.go","range":{"start":{"line":1,"character":0},"end":{"line":1,"character":8}},"selectionRange":{"start":{"line":1,"character":0},"end":{"line":1,"character":3}}}`)
	items, err := decodeCallHierarchyItems(single)
	if err != nil {
		t.Fatalf("decodeCallHierarchyItems single: %v", err)
	}
	if len(items) != 1 || items[0].Name != "Run" || items[0].Kind != "method" {
		t.Fatalf("unexpected call items: %#v", items)
	}

	array := json.RawMessage(`[{"name":"A","kind":6,"uri":"file:///a.go","range":{"start":{"line":1,"character":0},"end":{"line":1,"character":8}},"selectionRange":{"start":{"line":1,"character":0},"end":{"line":1,"character":3}}}]`)
	items, err = decodeCallHierarchyItems(array)
	if err != nil || len(items) != 1 || items[0].Name != "A" {
		t.Fatalf("unexpected array items: %#v %v", items, err)
	}

	edgesRaw := json.RawMessage(`[{"from":{"name":"Callee","kind":12,"uri":"file:///b.go","range":{"start":{"line":2,"character":0},"end":{"line":2,"character":6}},"selectionRange":{"start":{"line":2,"character":0},"end":{"line":2,"character":6}}},"fromRanges":[{"start":{"line":2,"character":10},"end":{"line":2,"character":16}}],"to":{"name":"Caller","kind":12,"uri":"file:///a.go","range":{"start":{"line":1,"character":0},"end":{"line":1,"character":6}},"selectionRange":{"start":{"line":1,"character":0},"end":{"line":1,"character":6}}}}]`)
	edges, err := decodeCallHierarchyCalls(edgesRaw)
	if err != nil {
		t.Fatalf("decodeCallHierarchyCalls: %v", err)
	}
	if len(edges) != 1 || edges[0].Item.Name != "Caller" || len(edges[0].FromRanges) != 1 {
		t.Fatalf("unexpected call edges: %#v", edges)
	}
}

func TestPositionOpsIncludesNewOps(t *testing.T) {
	ops := PositionOps()
	have := map[NavOp]bool{}
	for _, op := range ops {
		have[op] = true
	}
	for _, want := range []NavOp{NavHover, NavPrepareCallHierarchy, NavIncomingCalls, NavOutgoingCalls} {
		if !have[want] {
			t.Fatalf("PositionOps missing %s", want)
		}
	}
	if have[NavDocumentSymbol] {
		t.Fatal("document_symbol is whole-file, not a position op")
	}
}
