package tui

import (
	"context"
	"errors"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/dishant0406/KajiCode/internal/agent"
	"github.com/dishant0406/KajiCode/internal/config"
	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/modelregistry"
)

// errNoProviderRebuild reports that the TUI session has no provider-rebuild hook,
// so role/vision routing cannot build the destination provider.
var errNoProviderRebuild = errors.New("provider rebuild is not available for this TUI session")

// handleRoleCommand implements /role [list|name|clear]. With no args it shows the
// current active role and the configured role→model map; `list` shows the map;
// `clear` unsets the explicit role; any other token sets it as the explicit role
// for the TUI session (routed on the next run). It returns the (possibly updated)
// model plus the transcript text.
func (m model) handleRoleCommand(args string) (model, string) {
	args = strings.TrimSpace(args)
	switch strings.ToLower(args) {
	case "", "status":
		return m, m.roleStatusText()
	case "list", "ls":
		return m, m.roleListText()
	}
	if strings.EqualFold(args, "clear") {
		if m.activeRole == "" {
			return m, "Role\nNo explicit role is set; the run follows the default model."
		}
		previous := m.activeRole
		m = m.withActiveRole("")
		m = m.persistActiveRole()
		return m, "Role\nCleared explicit role \"" + previous + "\". The run follows the default model."
	}
	// Any other token sets the explicit role. Validate it resolves to a provider
	// profile + non-empty model so a bogus role doesn't silently no-op routing.
	router := m.roleRouter()
	if router == nil {
		return m, "Role\nMulti-model routing is not available for this TUI session."
	}
	lower := strings.ToLower(args)
	_, isDefaultRole := modelregistry.RoleInfoByID(lower)
	if _, ok := m.modelRoles[lower]; !ok && !isDefaultRole {
		// Unknown role: still allow it (the loop non-fatally falls back), but tell
		// the operator which roles ARE available so a typo is caught.
		return m, "Role\nUnknown role \"" + args + "\". Available roles: " + m.roleNames() + "."
	}
	profile, ok := router.ProfileFor(lower)
	if !ok || profile.Model == "" {
		return m, "Role\nRole \"" + args + "\" does not resolve to a provider + model."
	}
	m = m.withActiveRole(lower)
	m = m.persistActiveRole()
	return m, "Role\nSet explicit role \"" + args + "\" → routes to " + profile.Model + " · " + displayValue(profile.Name, profile.Provider) + " on the next run."
}

// persistActiveRole writes the current activeRole to the user config so the role
// binds globally across sessions and projects. Returns m with a transcript error
// appended when the write fails (the in-memory setting is kept; only persistence
// is lost). It is a no-op when there is no user config path.
func (m model) persistActiveRole() model {
	if path := strings.TrimSpace(m.userConfigPath); path != "" {
		if _, err := config.SetActiveRole(path, strings.TrimSpace(m.activeRole)); err != nil {
			m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendError, text: "role save error: " + err.Error()})
		}
	}
	return m
}

// newRolePicker builds the stage-1 /role list: a row per default role (built-ins
// first, with bound selector or a curated suggestion) plus a row per configured
// custom role, and the "add new role"/"clear"/"default model" control rows.
// Returns nil when no role routing is configured and no registry is available to
// offer a fresh role.
func (m model) newRolePicker() *commandPicker {
	items := m.roleRows()
	items = append(items,
		pickerItem{Label: "➕ add new role", Value: rolePickerAddNew, Meta: "type a role name"},
		pickerItem{Label: "clear current role", Value: rolePickerClear, Meta: "use default / unset active"},
		pickerItem{Label: "default model", Value: rolePickerDefault, Meta: "clear explicit role"},
	)
	if len(items) == 0 {
		return nil
	}
	return &commandPicker{
		kind:     pickerRole,
		title:    "Pick a task role",
		items:    items,
		allItems: append([]pickerItem{}, items...),
		selected: 0,
	}
}

// roleRows merges the built-in default roles (first) with configured custom roles
// for display in the /role picker and /role list. A bound selector is shown as-is;
// an unbound default role shows its curated model suggestion instead of "unset".
func (m model) roleRows() []pickerItem {
	items := make([]pickerItem, 0, len(m.modelRoles)+len(modelregistry.DefaultRoleIDs()))
	seen := map[string]bool{}
	// Built-in default roles, in catalog order.
	for _, id := range modelregistry.DefaultRoleIDs() {
		info, _ := modelregistry.RoleInfoByID(id)
		seen[id] = true
		meta := strings.TrimSpace(m.modelRoles[id])
		if roleValueModel(meta) == "" {
			if info.DefaultSelector != "" {
				meta = "suggest " + info.DefaultSelector
			} else {
				meta = "unset"
			}
		}
		label := info.DisplayName
		if strings.EqualFold(id, strings.TrimSpace(m.activeRole)) {
			label = "● " + label
		}
		items = append(items, pickerItem{Label: label, Value: id, Meta: meta, Group: "default"})
	}
	// Configured custom roles (anything not a default id), sorted.
	var custom []string
	for name := range m.modelRoles {
		if !seen[name] {
			custom = append(custom, name)
		}
	}
	sort.Strings(custom)
	for _, name := range custom {
		meta := m.modelRoles[name]
		if roleValueModel(meta) == "" {
			meta = "unset"
		}
		label := name
		if strings.EqualFold(name, strings.TrimSpace(m.activeRole)) {
			label = "● " + name
		}
		items = append(items, pickerItem{Label: label, Value: name, Meta: meta, Group: "custom"})
	}
	return items
}

// roleValueModel extracts the bare model ("model" or "provider:model") of a role
// selector for display. Empty when the selector is an alias or unset.
func roleValueModel(selector string) string {
	selector = strings.TrimSpace(selector)
	if selector == "" || strings.HasPrefix(selector, "@") {
		return ""
	}
	if i := strings.IndexByte(selector, ':'); i > 0 && i < len(selector)-1 {
		return selector[i+1:]
	}
	return selector
}

// openRolePicker opens the stage-1 role list. Falls back to a transcript status
// line when routing is unavailable (no modelRoles, no registry, no config path).
func (m model) openRolePicker() (model, tea.Cmd, string) {
	if m.pending {
		return m, nil, pickerBusyText("/role")
	}
	picker := m.newRolePicker()
	if picker == nil {
		return m, nil, m.roleStatusText()
	}
	m.picker = picker
	m.roleBindTarget = ""
	return m, nil, ""
}

// bindRoleToModel records a model binding for a role: writes modelRoles[role] to
// config, updates m.modelRoles, clears the binding context, and reports the change
// in a transcript line. It is the stage-2 terminal step of the interactive /role
// flow (a role chosen from the stage-1 list, a model chosen from the model picker).
func (m model) bindRoleToModel(role, modelID string) model {
	role = strings.TrimSpace(role)
	modelID = strings.TrimSpace(modelID)
	selector := m.roleSelectorForModel(modelID)
	if m.modelRoles == nil {
		m.modelRoles = map[string]string{}
	}
	previous := m.modelRoles[role]
	m.modelRoles[role] = selector
	m.roleBindTarget = ""
	if path := strings.TrimSpace(m.userConfigPath); path != "" {
		if _, err := config.SetModelRole(path, role, selector); err != nil {
			m.modelRoles[role] = previous
			m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendError, text: "role save error: " + err.Error()})
			return m
		}
	}
	m = m.withActiveRole(role)
	m = m.persistActiveRole()
	line := "Role\nRole \"" + role + "\" → " + displayValue(modelID, selector) + " (bound)"
	if strings.TrimSpace(m.defaultModel) != "" && !strings.Contains(selector, ":") {
		if entry, _, ok := m.modelCatalog.ResolveWithFallback(modelID); ok {
			if api := strings.TrimSpace(entry.APIModel); api != "" {
				line = "Role\nRole \"" + role + "\" → " + api + " " + displayValue(entry.ID, string(entry.Provider)) + " (bound)"
			}
		}
	}
	m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: line})
	return m
}

// roleSelectorForModel converts a chosen model id into the selector persisted for
// a role: the model id if it lives on the active provider, else "provider:model"
// using the owning saved provider so routing switches providers for that role.
func (m model) roleSelectorForModel(modelID string) string {
	modelID = strings.TrimSpace(modelID)
	if strings.TrimSpace(m.providerName) != "" {
		if entry, _, ok := m.modelCatalog.ResolveWithFallback(modelID); ok &&
			strings.EqualFold(string(entry.Provider), strings.TrimSpace(m.providerName)) {
			return modelID
		}
	}
	// Prefer the owning saved provider so a role can route to a different provider.
	for _, p := range m.savedProviders {
		if p.Model == modelID || strings.EqualFold(strings.TrimSpace(p.Name), strings.TrimSpace(m.providerName)) {
			continue
		}
		if p.Model == modelID {
			return strings.TrimSpace(p.Name) + ":" + modelID
		}
	}
	return modelID
}

// openRoleModelPicker opens the standard model picker in "bind to role" mode. The
// chosen model becomes the role's selector (bound via bindRoleToModel), rather
// than switching the active provider.
func (m model) openRoleModelPicker(role string) (model, tea.Cmd) {
	role = strings.TrimSpace(role)
	if role == "" || role == rolePickerAddNew || role == rolePickerDefault {
		return m, nil
	}
	if m.pending {
		return m, nil
	}
	picker := m.newModelPicker()
	if picker == nil {
		m.roleBindTarget = ""
		return m, nil
	}
	picker.title = "Choose a model for role \"" + role + "\""
	m.roleBindTarget = role
	m.picker = picker
	m.clearModelPickerLoadState()
	m.modelPickerForceShowAll = false
	return m, m.bumpModelPickerEpisode()
}

// handleRolePickerChoice applies a stage-1 /role selection. A role row advances to
// the bound model picker; the control rows act immediately (add-new, clear, default).
func (m model) handleRolePickerChoice(item pickerItem) (model, tea.Cmd) {
	value := item.Value
	switch value {
	case rolePickerClear:
		m.picker = nil
		m.roleBindTarget = ""
		if strings.TrimSpace(m.activeRole) != "" {
			m = m.withActiveRole("")
			m = m.persistActiveRole()
			m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: "Role\nCleared explicit role; the run follows the default model."})
		}
		return m, nil
	case rolePickerDefault:
		m.picker = nil
		m.roleBindTarget = ""
		m = m.withActiveRole("")
		m = m.persistActiveRole()
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: "Role\nUsing the default model (explicit role cleared)."})
		return m, nil
	case rolePickerAddNew:
		// Creating a new role needs a name; drop the picker and pre-fill the
		// composer with "/role add " so the user types the name and Enter opens the
		// bound model picker for it. This reuses the existing /role add path instead
		// of introducing a one-off name-entry modal.
		m.picker = nil
		m.roleBindTarget = ""
		m.clearSuggestions()
		m.clearComposer()
		m.input.SetValue("/role add ")
		m.input.SetCursor(len("/role add "))
		m.recomputeSuggestions()
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: "Role\nType a role name after /role add <name> to create it and pick its model."})
		return m, nil
	}
	// Normal role row: advance to the bound model picker for that role.
	return m.openRoleModelPicker(value)
}

func (m model) withActiveRole(role string) model {
	m.activeRole = role
	return m
}

func (m model) roleStatusText() string {
	lines := []string{"Role"}
	if m.activeRole != "" {
		lines = append(lines, "Active role: "+m.activeRole)
	} else {
		lines = append(lines, "Active role: (none — default model)")
	}
	lines = append(lines, "Roles: "+m.roleNames())
	if len(m.modelRoles) == 0 && m.defaultModel == "" {
		lines = append(lines, "No role routing configured in config (modelRoles). Default roles still resolve via capability.")
	}
	if m.defaultModel != "" {
		lines = append(lines, "defaultModel: "+m.defaultModel)
	}
	lines = append(lines, "use /role <name> to set, /role clear to unset")
	return strings.Join(lines, "\n")
}

// roleNames lists every routable role: built-in default roles plus configured
// custom roles, in catalog-first order. Delta from the old free-form-only list —
// default roles are now always listed so the operator can see the full catalog.
func (m model) roleNames() string {
	items := m.roleRows()
	names := make([]string, 0, len(items))
	for _, it := range items {
		names = append(names, it.Value)
	}
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}

func (m model) roleListText() string {
	items := m.roleRows()
	if len(items) == 0 {
		return "Role\nNo roles available."
	}
	lines := []string{"Role"}
	for _, it := range items {
		marker := " "
		if strings.EqualFold(it.Value, strings.TrimSpace(m.activeRole)) {
			marker = "*"
		}
		meta := it.Meta
		lines = append(lines, marker+" "+it.Value+" → "+meta)
	}
	lines = append(lines, "use /role <name> to set the active role, * marks it")
	return strings.Join(lines, "\n")
}

// roleRouter builds the shared RoleRouter for the TUI session, resolving role
// selectors against the saved providers + model catalog. The NewProvider adapter
// routes through m.newProvider so role/vision routing stays authenticated.
func (m model) roleRouter() *agent.RoleRouter {
	if m.modelRoles == nil && m.defaultModel == "" && len(m.savedProviders) == 0 {
		return nil
	}
	resolved := config.ResolvedConfig{
		Providers:    m.savedProviders,
		Provider:     m.providerProfile,
		ModelRoles:   m.modelRoles,
		DefaultModel: m.defaultModel,
	}
	return &agent.RoleRouter{
		Resolved: resolved,
		Registry: m.modelCatalog,
		NewProvider: func(_ context.Context, profile config.ProviderProfile) (kajicoderuntime.Provider, error) {
			if m.newProvider == nil {
				return nil, errNoProviderRebuild
			}
			return m.newProvider(profile)
		},
		DefaultModel: m.defaultModel,
	}
}

// effectiveVisionRouting returns the normalized per-message vision-routing mode
// (the resolved images.visionRouting setting, collapsed to a safe "auto"/"model"/"off").
func (m model) effectiveVisionRouting() string {
	return config.ImagesConfig{VisionRouting: m.visionRouting}.EffectiveVisionRouting()
}

// visionRoutingDestination resolves the profile an image turn routes to under
// per-message vision auto-routing. It mirrors exec's installVisionProvider policy:
// "model" first (ProfileFor("vision")), then "auto" (first vision-capable distinct
// provider), returning a distinct, vision-capable profile — the destination must
// differ from the effective model, or routing is a no-op and the caller should fall
// through to drop+warn. Returns ok=false when vision routing is "off", there is no
// router, or no usable destination exists.
func (m model) visionRoutingDestination(router *agent.RoleRouter) (config.ProviderProfile, bool) {
	if m.effectiveVisionRouting() == "off" || router == nil {
		return config.ProviderProfile{}, false
	}
	effective := strings.TrimSpace(m.effectiveModelName())
	if effective == "" {
		effective = strings.TrimSpace(m.providerProfile.Model)
	}
	// "model": the configured/capability vision role.
	if profile, ok := router.ProfileFor("vision"); ok {
		model := strings.TrimSpace(profile.Model)
		if model != "" && model != effective && modelregistry.SupportsVision(m.modelCatalog, model) {
			return profile, true
		}
	}
	// "auto": first vision-capable distinct provider.
	if profile, ok := router.FirstVisionCapableProvider(effective); ok {
		return profile, true
	}
	return config.ProviderProfile{}, false
}

// canRouteVisionImages reports whether an image turn that the current effective
// model cannot accept can be served by per-message vision auto-routing, so the
// submit gate keeps the images (letting the loop route the turn to a vision model
// and swap back) instead of dropping them.
func (m model) canRouteVisionImages(router *agent.RoleRouter) bool {
	_, ok := m.visionRoutingDestination(router)
	return ok
}

// roleRoutingOptions wires multi-model task routing into an agent.Options for a
// run. It returns nil (loop byte-identical) unless an explicit role is set, or
// per-message vision auto-routing is enabled. When either applies, RoleFor becomes
// message-aware: an image turn whose effective model cannot accept images routes to
// the "vision" role (vision model, else default), and subsequent turns return the
// normal active role (or "" = default), which the loop's swap-back seam uses to
// return to the default model after the image turn.
func (m model) roleRoutingOptions(newProvider func(config.ProviderProfile) (kajicoderuntime.Provider, error)) *agent.RoleRouting {
	role := strings.TrimSpace(m.activeRole)
	visionRouter := m.roleRouter()
	visionEnabled := m.effectiveVisionRouting() != "off"
	if role == "" && !(visionEnabled && visionRouter != nil) {
		return nil
	}
	if visionRouter == nil || newProvider == nil {
		return nil
	}
	effectiveModel := strings.TrimSpace(m.effectiveModelName())
	if effectiveModel == "" {
		effectiveModel = strings.TrimSpace(m.providerProfile.Model)
	}
	mode := m.effectiveVisionRouting()
	return &agent.RoleRouting{
		Current: func(ctx context.Context, r string) (agent.Provider, config.ProviderProfile, bool) {
			r = strings.TrimSpace(r)
			if r == "" {
				return nil, config.ProviderProfile{}, false
			}
			var (
				profile config.ProviderProfile
				ok      bool
			)
			if r == "vision" && mode == "auto" {
				// "auto": pick the first vision-capable distinct provider directly,
				// rather than requiring a configured ModelRoles["vision"] entry.
				profile, ok = visionRouter.FirstVisionCapableProvider(effectiveModel)
			} else {
				profile, ok = visionRouter.ProfileFor(r)
			}
			if !ok || strings.TrimSpace(profile.Model) == "" {
				return nil, config.ProviderProfile{}, false
			}
			p, err := newProvider(profile)
			if err != nil || p == nil {
				return nil, config.ProviderProfile{}, false
			}
			return p, profile, true
		},
		RoleFor: func(ctx agent.RoleContext) string {
			if ctx.HasImages && m.visionRoutingEnabledFor(role) {
				return "vision"
			}
			return role
		},
		ContextWindowFor: func(profile config.ProviderProfile) int {
			return m.modelContextWindow(profile.Model)
		},
	}
}

// visionRoutingEnabledFor reports whether an image turn should be routed to the
// vision role under the active routing mode: the effective model cannot accept
// images AND a distinct vision-capable destination is available. When no such
// destination exists the turn keeps its normal role so the submit gate's
// drop+warn (not routing) governs.
func (m model) visionRoutingEnabledFor(role string) bool {
	if m.effectiveVisionRouting() == "off" || m.modelSupportsVisionTUI() {
		return false
	}
	router := m.roleRouter()
	if router == nil {
		return false
	}
	effective := strings.TrimSpace(m.effectiveModelName())
	if effective == "" {
		effective = strings.TrimSpace(m.providerProfile.Model)
	}
	if profile, ok := router.ProfileFor("vision"); ok {
		model := strings.TrimSpace(profile.Model)
		if model != "" && model != effective && modelregistry.SupportsVision(m.modelCatalog, model) {
			return true
		}
	}
	if _, ok := router.FirstVisionCapableProvider(effective); ok {
		return true
	}
	return false
}
