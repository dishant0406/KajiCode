package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/dishant0406/KajiCode/internal/agent"
	"github.com/dishant0406/KajiCode/internal/config"
	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/modelregistry"
)

func TestRoleCommandShowsStatusWithoutRole(t *testing.T) {
	m := newModel(context.Background(), Options{
		ProviderName: "openai",
		ModelName:    "gpt-4.1",
		Provider:     &fakeProvider{},
		ModelRoles:   map[string]string{"implement": "gpt-4.1", "design": "anthropic:claude-sonnet-4-5"},
	})
	m.input.SetValue("/role status")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd != nil {
		t.Fatal("expected /role status to be handled without starting an agent run")
	}
	for _, want := range []string{"Role", "Active role: (none — default model)"} {
		if !transcriptContains(next.transcript, want) {
			t.Fatalf("expected role transcript to contain %q, got %#v", want, next.transcript)
		}
	}
	if next.activeRole != "" {
		t.Fatalf("expected no active role, got %q", next.activeRole)
	}
}

func TestRoleCommandOpensInteractivePicker(t *testing.T) {
	saved := config.ProviderProfile{Name: "openai", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-4.1"}
	m := newModel(context.Background(), Options{
		ProviderName:    "openai",
		ModelName:       "gpt-4.1",
		Provider:        &fakeProvider{},
		ProviderProfile: saved,
		SavedProviders:  []config.ProviderProfile{saved},
		ModelRoles:      map[string]string{"implement": "gpt-4.1"},
		DefaultModel:    "gpt-4.1",
		NewProvider:     func(config.ProviderProfile) (kajicoderuntime.Provider, error) { return &fakeProvider{}, nil },
	})
	m.input.SetValue("/role")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if next.picker == nil || next.picker.kind != pickerRole {
		t.Fatalf("expected bare /role to open the role picker, got picker=%v kind=%v", next.picker, pickerKindOf(next.picker))
	}
	if next.activeRole != "" {
		t.Fatalf("expected no active role committed by opening the picker, got %q", next.activeRole)
	}
	_ = cmd
}

func pickerKindOf(p *commandPicker) pickerKind {
	if p == nil {
		return pickerKind(-1)
	}
	return p.kind
}

func TestRoleInteractiveBindModel(t *testing.T) {
	saved := config.ProviderProfile{Name: "openai", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-4.1"}
	m := newModel(context.Background(), Options{
		ProviderName:    "openai",
		ModelName:       "gpt-4.1",
		Provider:        &fakeProvider{},
		ProviderProfile: saved,
		SavedProviders:  []config.ProviderProfile{saved},
		ModelRoles:      map[string]string{"implement": "gpt-4.1"},
		DefaultModel:    "gpt-4.1",
		NewProvider:     func(config.ProviderProfile) (kajicoderuntime.Provider, error) { return &fakeProvider{}, nil },
	})
	m.input.SetValue("/role")
	updated, _ := m.Update(testKey(tea.KeyEnter))
	m = updated.(model)
	if m.picker == nil || m.picker.kind != pickerRole {
		t.Fatalf("expected role picker, got %v", m.picker)
	}
	// Locate the "implement" row (default roles now precede custom roles).
	idx := -1
	for i, it := range m.picker.items {
		if it.Value == "implement" {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatalf("expected an implement row in the picker, got items=%v", m.picker.items)
	}
	m.picker.selected = idx
	mUpdated, _ := m.choosePicker()
	nextModelPicker := mUpdated.(model)

	if nextModelPicker.picker == nil || nextModelPicker.picker.kind != pickerModel {
		t.Fatalf("expected stage-2 model picker after choosing a role, got %v", nextModelPicker.picker)
	}
	if nextModelPicker.roleBindTarget != "implement" {
		t.Fatalf("expected roleBindTarget=implement, got %q", nextModelPicker.roleBindTarget)
	}

	// Pick a model row and enter. It should bind to the role, not switch the model.
	npUpdated, _ := nextModelPicker.choosePicker()
	np := npUpdated.(model)
	if np.picker != nil {
		t.Fatalf("expected picker to close after binding, got %v", np.picker)
	}
	if np.roleBindTarget != "" {
		t.Fatalf("expected roleBindTarget cleared after binding, got %q", np.roleBindTarget)
	}
	if got := np.modelRoles["implement"]; got == "" {
		t.Fatalf("expected modelRoles[implement] bound, got %q", got)
	}
	if np.activeRole != "implement" {
		t.Fatalf("expected activeRole=implement after binding, got %q", np.activeRole)
	}
}

func TestRoleClearAndDefaultControlRows(t *testing.T) {
	saved := config.ProviderProfile{Name: "openai", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-4.1"}
	m := newModel(context.Background(), Options{
		ProviderName:    "openai",
		ModelName:       "gpt-4.1",
		Provider:        &fakeProvider{},
		ProviderProfile: saved,
		SavedProviders:  []config.ProviderProfile{saved},
		ModelRoles:      map[string]string{"implement": "gpt-4.1"},
		DefaultModel:    "gpt-4.1",
		NewProvider:     func(config.ProviderProfile) (kajicoderuntime.Provider, error) { return &fakeProvider{}, nil },
	})
	m = m.withActiveRole("implement")

	m, _ = m.handleRolePickerChoice(pickerItem{Value: rolePickerClear})
	if m.activeRole != "" {
		t.Fatalf("expected clear to unset active role, got %q", m.activeRole)
	}
	if m.picker != nil {
		t.Fatalf("expected picker closed after clear, got %v", m.picker)
	}

	m = m.withActiveRole("implement")
	m, _ = m.handleRolePickerChoice(pickerItem{Value: rolePickerDefault})
	if m.activeRole != "" {
		t.Fatalf("expected default row to clear active role, got %q", m.activeRole)
	}
}

func TestRoleAddOpensBoundModelPicker(t *testing.T) {
	saved := config.ProviderProfile{Name: "openai", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-4.1"}
	m := newModel(context.Background(), Options{
		ProviderName:    "openai",
		ModelName:       "gpt-4.1",
		Provider:        &fakeProvider{},
		ProviderProfile: saved,
		SavedProviders:  []config.ProviderProfile{saved},
		ModelRoles:      map[string]string{"implement": "gpt-4.1"},
		DefaultModel:    "gpt-4.1",
		NewProvider:     func(config.ProviderProfile) (kajicoderuntime.Provider, error) { return &fakeProvider{}, nil },
	})
	m.input.SetValue("/role add reviewer")
	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if next.picker == nil || next.picker.kind != pickerModel {
		t.Fatalf("expected /role add to open the model picker, got %v", next.picker)
	}
	if next.roleBindTarget != "reviewer" {
		t.Fatalf("expected roleBindTarget=reviewer, got %q", next.roleBindTarget)
	}
}

func TestRoleCommandSetsExplicitRole(t *testing.T) {
	saved := config.ProviderProfile{Name: "openai", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-4.1"}
	m := newModel(context.Background(), Options{
		ProviderName:    "openai",
		ModelName:       "gpt-4.1",
		Provider:        &fakeProvider{},
		ProviderProfile: saved,
		SavedProviders:  []config.ProviderProfile{saved},
		ModelRoles:      map[string]string{"implement": "gpt-4.1", "design": "claude-sonnet-4-5"},
		DefaultModel:    "gpt-4.1",
		NewProvider:     func(config.ProviderProfile) (kajicoderuntime.Provider, error) { return &fakeProvider{}, nil },
	})
	m.input.SetValue("/role implement")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd != nil {
		t.Fatal("expected /role to be handled without starting an agent run")
	}
	if next.activeRole != "implement" {
		t.Fatalf("expected active role to be implement, got %q", next.activeRole)
	}
	if !transcriptContains(next.transcript, "Role") {
		t.Fatalf("expected role transcript to contain Role, got %#v", next.transcript)
	}
}

func TestRoleCommandClearsRole(t *testing.T) {
	m := newModel(context.Background(), Options{
		ProviderName: "openai",
		ModelName:    "gpt-4.1",
		Provider:     &fakeProvider{},
		ModelRoles:   map[string]string{"implement": "gpt-4.1"},
	})
	m = m.withActiveRole("implement")
	m.input.SetValue("/role clear")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd != nil {
		t.Fatal("expected /role to be handled without starting an agent run")
	}
	if next.activeRole != "" {
		t.Fatalf("expected cleared active role, got %q", next.activeRole)
	}
}

func TestRolePickerShowsDefaultRolesWithoutConfig(t *testing.T) {
	saved := config.ProviderProfile{Name: "openai", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-4.1"}
	m := newModel(context.Background(), Options{
		ProviderName:    "openai",
		ModelName:       "gpt-4.1",
		Provider:        &fakeProvider{},
		ProviderProfile: saved,
		SavedProviders:  []config.ProviderProfile{saved},
		DefaultModel:    "gpt-4.1",
		NewProvider:     func(config.ProviderProfile) (kajicoderuntime.Provider, error) { return &fakeProvider{}, nil },
	})
	picker := m.newRolePicker()
	if picker == nil {
		t.Fatal("expected a non-nil role picker for a fresh session with defaults")
	}
	// Every default role id from the catalog has a row.
	got := map[string]bool{}
	for _, it := range picker.items {
		got[it.Value] = true
	}
	for _, id := range modelregistry.DefaultRoleIDs() {
		if !got[id] {
			t.Fatalf("expected default role row %q in picker, items=%v", id, picker.items)
		}
	}
	// Suggestion-recommending default roles show their curated selector, not "unset".
	for _, it := range picker.items {
		if info, ok := modelregistry.RoleInfoByID(it.Value); ok && info.DefaultSelector != "" {
			if it.Meta != "suggest "+info.DefaultSelector {
				t.Fatalf("role %q unbound meta = %q, want suggest %q", it.Value, it.Meta, info.DefaultSelector)
			}
		}
	}
	// Selecting a default role opens the stage-2 model picker bound to it.
	for idx, it := range picker.items {
		if it.Value == modelregistry.RolePlan {
			m.picker = picker
			m.picker.selected = idx
			updated, _ := m.choosePicker()
			nextModelPicker := updated.(model)
			if nextModelPicker.picker == nil || nextModelPicker.picker.kind != pickerModel {
				t.Fatalf("expected model picker after selecting plan, got %v", nextModelPicker.picker)
			}
			if nextModelPicker.roleBindTarget != modelregistry.RolePlan {
				t.Fatalf("expected roleBindTarget=%q, got %q", modelregistry.RolePlan, nextModelPicker.roleBindTarget)
			}
			break
		}
	}
}

func TestRoleListTextIncludesDefaultRoles(t *testing.T) {
	m := newModel(context.Background(), Options{
		ProviderName: "openai",
		ModelName:    "gpt-4.1",
		Provider:     &fakeProvider{},
	})
	text := m.roleListText()
	if !strings.Contains(text, modelregistry.RolePlan) || !strings.Contains(text, modelregistry.RoleVision) {
		t.Fatalf("expected role list to include default roles, got %q", text)
	}
	if !strings.Contains(text, "suggest ") {
		t.Fatalf("expected role list to show suggestions for unbound default roles, got %q", text)
	}
}

func TestRoleCommandSetsDefaultRoleWithoutConfigEntry(t *testing.T) {
	saved := config.ProviderProfile{Name: "openai", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-4.1"}
	m := newModel(context.Background(), Options{
		ProviderName:    "openai",
		ModelName:       "gpt-4.1",
		Provider:        &fakeProvider{},
		ProviderProfile: saved,
		SavedProviders:  []config.ProviderProfile{saved},
		DefaultModel:    "gpt-4.1",
		NewProvider:     func(config.ProviderProfile) (kajicoderuntime.Provider, error) { return &fakeProvider{}, nil },
	})
	// A default role with no modelRoles entry should be settable (capability fallback).
	m.input.SetValue("/role " + modelregistry.RoleVision)
	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if next.activeRole != modelregistry.RoleVision {
		t.Fatalf("expected active role %q, got %q", modelregistry.RoleVision, next.activeRole)
	}
	if !transcriptContains(next.transcript, "Role") {
		t.Fatalf("expected role transcript, got %#v", next.transcript)
	}
}

func TestRoleRoutingOptionsNilWithoutExplicitRole(t *testing.T) {
	m := newModel(context.Background(), Options{
		ProviderName: "openai",
		ModelName:    "gpt-4.1",
		Provider:     &fakeProvider{},
		ModelRoles:   map[string]string{"implement": "gpt-4.1"},
	})
	if m.roleRoutingOptions(func(config.ProviderProfile) (kajicoderuntime.Provider, error) { return &fakeProvider{}, nil }) != nil {
		t.Fatal("expected nil RoleRouting when no explicit role is set")
	}
}

func TestRoleRoutingOptionsWiredWithRole(t *testing.T) {
	saved := config.ProviderProfile{Name: "openai", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-4.1"}
	m := newModel(context.Background(), Options{
		ProviderName:    "openai",
		ModelName:       "gpt-4.1",
		Provider:        &fakeProvider{},
		ProviderProfile: saved,
		SavedProviders:  []config.ProviderProfile{saved},
		ModelRoles:      map[string]string{"implement": "gpt-4.1"},
		DefaultModel:    "gpt-4.1",
		NewProvider:     func(config.ProviderProfile) (kajicoderuntime.Provider, error) { return &fakeProvider{}, nil },
	})
	m = m.withActiveRole("implement")

	routing := m.roleRoutingOptions(func(config.ProviderProfile) (kajicoderuntime.Provider, error) { return &fakeProvider{}, nil })
	if routing == nil {
		t.Fatal("expected non-nil RoleRouting when an explicit role is set")
	}
	if got := routing.RoleFor(agent.RoleContext{}); got != "implement" {
		t.Fatalf("expected RoleFor to return implement, got %q", got)
	}
}

func TestRoleTitleSegmentShowsMethodRole(t *testing.T) {
	m := newModel(context.Background(), Options{
		ProviderName: "openai",
		ModelName:    "gpt-4.1",
		Provider:     &fakeProvider{},
		ModelRoles:   map[string]string{"implement": "gpt-4.1"},
	})
	title := plainRender(t, m.titleModelSegment())
	if !strings.Contains(title, "openai/gpt-4.1") {
		t.Fatalf("expected provider/model in title, got %q", title)
	}
	if strings.Contains(title, "role") {
		t.Fatalf("no role set, expected no role marker, got %q", title)
	}
	m = m.withActiveRole("implement")
	title = plainRender(t, m.titleModelSegment())
	if !strings.Contains(title, "role implement") {
		t.Fatalf("expected role marker in title, got %q", title)
	}
}

func TestBTWRoleCommandUnavailable(t *testing.T) {
	cases := []struct {
		arg  string
		want bool
	}{
		{"", false},         // status
		{"status", false},   // status
		{"list", false},     // list allowed
		{"implement", true}, // setting a role in BTW is unavailable
		{"clear", true},     // clearing a role in BTW is unavailable
	}
	for _, tc := range cases {
		if got := btwCommandUnavailable(parsedCommand{kind: commandRole, text: tc.arg}); got != tc.want {
			t.Fatalf("btwCommandUnavailable(commandRole, %q) = %v, want %v", tc.arg, got, tc.want)
		}
	}
}

func TestRoleRoutingOptionsVisionRoutesImageTurn(t *testing.T) {
	vision := config.ProviderProfile{Name: "openai", Model: "gpt-4.1"}
	dflt := config.ProviderProfile{Name: "anthropic", Model: "claude-haiku-3.5"}
	m := newModel(context.Background(), Options{
		ProviderName:    "anthropic",
		ModelName:       "claude-haiku-3.5",
		Provider:        &fakeProvider{},
		ProviderProfile: dflt,
		SavedProviders:  []config.ProviderProfile{dflt, vision},
		DefaultModel:    "claude-haiku-3.5",
		VisionRouting:   "model",
		NewProvider:     func(config.ProviderProfile) (kajicoderuntime.Provider, error) { return &fakeProvider{}, nil },
	})
	routing := m.roleRoutingOptions(func(config.ProviderProfile) (kajicoderuntime.Provider, error) { return &fakeProvider{}, nil })
	if routing == nil {
		t.Fatal("expected non-nil RoleRouting when vision auto-routing is enabled")
	}
	if got := routing.RoleFor(agent.RoleContext{HasImages: true}); got != modelregistry.RoleVision {
		t.Fatalf("expected image turn to route to %q, got %q", modelregistry.RoleVision, got)
	}
	if got := routing.RoleFor(agent.RoleContext{}); got != "" {
		t.Fatalf("expected plain turn to stay on default, got %q", got)
	}
}

func TestRoleRoutingOptionsVisionModelKeepsRoleOnImageTurn(t *testing.T) {
	m := newModel(context.Background(), Options{
		ProviderName:    "openai",
		ModelName:       "gpt-4.1",
		Provider:        &fakeProvider{},
		ProviderProfile: config.ProviderProfile{Name: "openai", Model: "gpt-4.1"},
		SavedProviders:  []config.ProviderProfile{{Name: "openai", Model: "gpt-4.1"}},
		DefaultModel:    "gpt-4.1",
		VisionRouting:   "auto",
		ModelRoles:      map[string]string{"implement": "gpt-4.1"},
		NewProvider:     func(config.ProviderProfile) (kajicoderuntime.Provider, error) { return &fakeProvider{}, nil },
	})
	m = m.withActiveRole("implement")
	routing := m.roleRoutingOptions(func(config.ProviderProfile) (kajicoderuntime.Provider, error) { return &fakeProvider{}, nil })
	if routing == nil {
		t.Fatal("expected non-nil RoleRouting for explicit role")
	}
	if got := routing.RoleFor(agent.RoleContext{HasImages: true}); got != "implement" {
		t.Fatalf("expected image turn to keep implement role, got %q", got)
	}
}

func TestRoleRoutingOptionsNilWithoutRoleOrVision(t *testing.T) {
	m := newModel(context.Background(), Options{
		ProviderName: "anthropic",
		ModelName:    "claude-haiku-3.5",
		Provider:     &fakeProvider{},
		ModelRoles:   map[string]string{"implement": "gpt-4.1"},
	})
	if m.roleRoutingOptions(func(config.ProviderProfile) (kajicoderuntime.Provider, error) { return &fakeProvider{}, nil }) != nil {
		t.Fatal("expected nil RoleRouting when no explicit role and vision routing is off")
	}
}

func TestVisionRoutingDestinationPicksDistinctVisionModel(t *testing.T) {
	vision := config.ProviderProfile{Name: "openai", Model: "gpt-4.1"}
	dflt := config.ProviderProfile{Name: "anthropic", Model: "claude-haiku-3.5"}
	m := newModel(context.Background(), Options{
		ProviderName:    "anthropic",
		ModelName:       "claude-haiku-3.5",
		Provider:        &fakeProvider{},
		ProviderProfile: dflt,
		SavedProviders:  []config.ProviderProfile{dflt, vision},
		DefaultModel:    "claude-haiku-3.5",
		VisionRouting:   "model",
		NewProvider:     func(config.ProviderProfile) (kajicoderuntime.Provider, error) { return &fakeProvider{}, nil },
	})
	router := m.roleRouter()
	if router == nil {
		t.Fatal("expected a role router when vision routing is enabled")
	}
	if !m.canRouteVisionImages(router) {
		t.Fatal("expected vision routing to be available for a non-vision default + vision provider")
	}
	dest, ok := m.visionRoutingDestination(router)
	if !ok || strings.TrimSpace(dest.Model) != "gpt-4.1" {
		t.Fatalf("expected vision destination gpt-4.1, got %#v ok=%v", dest, ok)
	}
}

func TestCanRouteVisionImagesFalseWhenOff(t *testing.T) {
	m := newModel(context.Background(), Options{
		ProviderName:    "anthropic",
		ModelName:       "claude-haiku-3.5",
		Provider:        &fakeProvider{},
		ProviderProfile: config.ProviderProfile{Name: "anthropic", Model: "claude-haiku-3.5"},
		SavedProviders:  []config.ProviderProfile{{Name: "anthropic", Model: "claude-haiku-3.5"}, {Name: "openai", Model: "gpt-4.1"}},
		DefaultModel:    "claude-haiku-3.5",
		VisionRouting:   "off",
		NewProvider:     func(config.ProviderProfile) (kajicoderuntime.Provider, error) { return &fakeProvider{}, nil },
	})
	router := m.roleRouter()
	if router != nil && m.canRouteVisionImages(router) {
		t.Fatal("expected no vision routing when images.visionRouting is off")
	}
	if m.effectiveVisionRouting() != "off" {
		t.Fatalf("expected EffectiveVisionRouting off, got %q", m.effectiveVisionRouting())
	}
}
