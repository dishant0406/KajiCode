package tui

import (
	"testing"

	"github.com/dishant0406/KajiCode/internal/modelregistry"
	"github.com/dishant0406/KajiCode/internal/modelsource"
)

// TestRefreshModelPickerDispatchesModelSourceRefresh pins the wiring: the picker
// "Refresh models" action always dispatches a command that includes the models.dev
// snapshot refresh alongside provider discovery.
func TestRefreshModelPickerDispatchesModelSourceRefresh(t *testing.T) {
	resetModelSourceForTest(t)
	modelsource.Enable()

	m := newModel(t.Context(), Options{ModelName: "gpt-4.1"})
	_, cmd := m.refreshModelPicker()
	if cmd == nil {
		t.Fatal("refreshModelPicker must dispatch a command (includes the models.dev refresh)")
	}
}

// TestModelSourceRefreshAppliedRebuildsAndRebinds pins that applying a refresh
// rebuilds the model registry from the current snapshot and re-binds the session,
// so fact reads (the vision gate here) reflect the newly loaded catalog rather
// than the record layered in at construction.
func TestModelSourceRefreshAppliedRebuildsAndRebinds(t *testing.T) {
	resetModelSourceForTest(t)
	modelsource.Enable()

	const textOnly = `{"models":{"acme/widget-1":{"id":"acme/widget-1","name":"Widget 1",
	  "modalities":{"input":["text"],"output":["text"]},"limit":{"context":100000,"output":8000}}}}`
	if err := modelsource.LoadDocument([]byte(textOnly)); err != nil {
		t.Fatal(err)
	}
	modelregistry.ResetSynthesizedCache()
	m := newModel(t.Context(), Options{ModelName: "acme/widget-1"})
	if m.modelSupportsVisionTUI() {
		t.Fatal("precondition: widget-1 is text-only in the loaded snapshot")
	}

	// The snapshot now reports widget-1 as image-capable (as a refresh would).
	const withImage = `{"models":{"acme/widget-1":{"id":"acme/widget-1","name":"Widget 1",
	  "modalities":{"input":["text","image"],"output":["text"]},"limit":{"context":200000,"output":16000}}}}`
	if err := modelsource.LoadDocument([]byte(withImage)); err != nil {
		t.Fatal(err)
	}

	next := m.modelSourceRefreshApplied()
	if !next.modelSupportsVisionTUI() {
		t.Fatal("after applying a refresh, widget-1 should be vision-capable")
	}
	// The session binding must follow the current effective model.
	if _, id := modelsource.Bound(); id != "acme/widget-1" {
		t.Fatalf("Bound() id = %q, want acme/widget-1 after rebind", id)
	}
}

func resetModelSourceForTest(t *testing.T) {
	t.Helper()
	modelsource.Disable()
	modelregistry.ResetSynthesizedCache()
	t.Cleanup(func() {
		modelregistry.ResetSynthesizedCache()
		modelsource.Bind("", "")
		modelsource.Disable()
	})
}
