package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestWebSearchProviderStepArrowNavigation verifies the reported bug: an arrow
// key on the provider list must move the highlight only — it must never select.
func TestWebSearchProviderStepArrowNavigation(t *testing.T) {
	m := model{}.openWebSearchForm()
	if m.webSearchForm == nil {
		t.Fatal("form failed to open")
	}
	n := len(m.webSearchForm.providers)

	// ↓ moves down (not select), wrapping at the bottom.
	d1, _ := m.handleWebSearchKey(testKey(tea.KeyDown))
	got := d1.(model)
	if got.webSearchForm.step != webSearchStepProvider || got.webSearchForm.selected != 1 {
		t.Fatalf("first down: step=%v selected=%d (want provider/1)", got.webSearchForm.step, got.webSearchForm.selected)
	}
	for range n - 1 {
		d, _ := got.handleWebSearchKey(testKey(tea.KeyDown))
		got = d.(model)
	}
	if got.webSearchForm.selected != 0 {
		t.Fatalf("wrapping down: selected=%d want 0", got.webSearchForm.selected)
	}
	if got.webSearchForm.step != webSearchStepProvider {
		t.Fatalf("down should stay on provider step, got %v", got.webSearchForm.step)
	}

	// ↑ moves up.
	u, _ := got.handleWebSearchKey(testKey(tea.KeyUp))
	gotUp := u.(model)
	if gotUp.webSearchForm.selected != n-1 {
		t.Fatalf("up from 0 should wrap to %d, got %d", n-1, gotUp.webSearchForm.selected)
	}
}
