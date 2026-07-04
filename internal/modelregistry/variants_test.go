package modelregistry

import "testing"

func testRegistry(t *testing.T) Registry {
	t.Helper()
	registry, err := DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry() error = %v", err)
	}
	return registry
}

// expectedFastPairs is the known-good base->fast mapping. It is a literal
// regression guard: if the catalog silently drops or mis-wires a FastVariantID,
// the forward/reverse assertions below fail loudly rather than making /fast a
// quiet no-op. Keep in sync with the FastVariantID fields in catalog.go.
var expectedFastPairs = map[string]string{
	"claude-sonnet-4.5": "claude-haiku-4.5",
	"gpt-4.1":           "gpt-4.1-mini",
	"gpt-4o":            "gpt-4o-mini",
	"gemini-2.5-pro":    "gemini-2.5-flash",
}

// Every FastVariantID declared in the catalog must resolve to a real, non-self,
// non-deprecated model. NewRegistry already rejects an unresolvable id at build
// time; this additionally guards the runtime availability contract that /fast and
// the "f" marker depend on.
func TestCatalogFastVariantsResolve(t *testing.T) {
	registry := testRegistry(t)
	for _, entry := range registry.List(ListOptions{IncludeDeprecated: true}) {
		if entry.FastVariantID == "" {
			continue
		}
		target, ok := registry.Resolve(entry.FastVariantID)
		if !ok {
			t.Errorf("model %q fast variant %q does not resolve", entry.ID, entry.FastVariantID)
			continue
		}
		if target.ID == entry.ID {
			t.Errorf("model %q names itself as its fast variant", entry.ID)
		}
		if target.Status == ModelStatusDeprecated {
			t.Errorf("model %q fast variant %q is deprecated", entry.ID, entry.FastVariantID)
		}
	}
}

// No model may be both a base and a fast, or the two directions become ambiguous
// (a model would be "currently fast" AND advertise a fast variant). Derived from
// the registry so a bad catalog edit — e.g. chaining mini->nano — is caught.
func TestFastVariantPairsUnambiguous(t *testing.T) {
	registry := testRegistry(t)
	role := map[string]string{} // canonical id -> "base" | "fast"
	assign := func(id, r string) {
		entry, ok := registry.Resolve(id)
		if !ok {
			return
		}
		if prev, seen := role[entry.ID]; seen && prev != r {
			t.Fatalf("model %q is declared as both a base and a fast variant", entry.ID)
		}
		role[entry.ID] = r
	}
	for _, entry := range registry.List(ListOptions{IncludeDeprecated: true}) {
		if entry.FastVariantID == "" {
			continue
		}
		assign(entry.ID, "base")
		assign(entry.FastVariantID, "fast")
	}
}

// The known pairs must be present and wired in both directions, and neither end
// may leak into the other role (a base is not itself fast; a fast has no fast).
func TestFastVariantAndBaseVariant(t *testing.T) {
	registry := testRegistry(t)

	for base, fast := range expectedFastPairs {
		// base -> fast (forward), and the fast id has NO fast variant of its own.
		if got, ok := registry.FastVariant(base); !ok {
			t.Errorf("FastVariant(%q) = not found, want the fast variant", base)
		} else if canon, _ := registry.Resolve(fast); got.ID != canon.ID {
			t.Errorf("FastVariant(%q).ID = %q, want %q", base, got.ID, canon.ID)
		}
		if _, ok := registry.FastVariant(fast); ok {
			t.Errorf("FastVariant(%q) resolved, but a fast model should have no fast variant", fast)
		}

		// fast -> base (reverse), and the base id is NOT itself a fast variant.
		if got, ok := registry.BaseVariant(fast); !ok {
			t.Errorf("BaseVariant(%q) = not found, want the base variant", fast)
		} else if canon, _ := registry.Resolve(base); got.ID != canon.ID {
			t.Errorf("BaseVariant(%q).ID = %q, want %q", fast, got.ID, canon.ID)
		}
		if _, ok := registry.BaseVariant(base); ok {
			t.Errorf("BaseVariant(%q) resolved, but a base model is not a fast variant", base)
		}
	}
}

// HasFastVariant is the TUI marker predicate: true only for base models with an
// available fast variant, false for the fast models themselves, unpaired models,
// and unknown ids.
func TestHasFastVariant(t *testing.T) {
	registry := testRegistry(t)
	for base, fast := range expectedFastPairs {
		if !registry.HasFastVariant(base) {
			t.Errorf("HasFastVariant(%q) = false, want true", base)
		}
		if registry.HasFastVariant(fast) {
			t.Errorf("HasFastVariant(%q) = true, want false (a fast model has no fast variant)", fast)
		}
	}
	for _, id := range []string{"claude-opus-4.1", "definitely-not-a-real-model", ""} {
		if registry.HasFastVariant(id) {
			t.Errorf("HasFastVariant(%q) = true, want false", id)
		}
	}
}

func TestFastVariantAbsentAndUnknown(t *testing.T) {
	registry := testRegistry(t)

	// A real model with no configured fast/base variant reports none in both
	// directions (a valid, handled case — /fast will say "no fast mode available").
	const noVariant = "claude-opus-4.1"
	if _, ok := registry.Resolve(noVariant); !ok {
		t.Skipf("%q not in registry; skipping no-variant case", noVariant)
	}
	if _, ok := registry.FastVariant(noVariant); ok {
		t.Errorf("FastVariant(%q) resolved, want none", noVariant)
	}
	if _, ok := registry.BaseVariant(noVariant); ok {
		t.Errorf("BaseVariant(%q) resolved, want none", noVariant)
	}

	// An unknown id resolves to nothing in either direction.
	if _, ok := registry.FastVariant("definitely-not-a-real-model"); ok {
		t.Error("FastVariant(unknown) resolved, want none")
	}
	if _, ok := registry.BaseVariant("definitely-not-a-real-model"); ok {
		t.Error("BaseVariant(unknown) resolved, want none")
	}
}
