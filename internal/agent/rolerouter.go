package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/dishant0406/KajiCode/internal/config"
	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/modelregistry"
)

// ProviderBuilder builds a runtime provider from a resolved profile. Wire it to
// cli's deps.newProvider (which already applies stored keys). ctx is unused by the
// cli builder; callers that need it may ignore it.
type ProviderBuilder func(ctx context.Context, profile config.ProviderProfile) (kajicoderuntime.Provider, error)

// RoleRouter maps a task role to a concrete provider profile and builds providers. It
// is stateless except for the resolver inputs, so it is safe to construct once per run.
type RoleRouter struct {
	// Resolved is the fully resolved config for the run. It carries the complete
	// provider list (ResolvedConfig.Providers) plus the active single profile.
	Resolved config.ResolvedConfig
	// Registry resolves model selectors to concrete model entries/capabilities.
	Registry modelregistry.Registry
	// NewProvider builds a runtime provider for a profile. nil disables routing.
	NewProvider ProviderBuilder
	// DefaultModel, when set, wins over the active profile's own Model for seeding
	// the run. Exported so cli can set it from resolved.DefaultModel.
	DefaultModel string
}

// EffectiveModel returns the model that should seed the run: the configured
// DefaultModel when set and resolvable, else the active profile's Model.
func (r *RoleRouter) EffectiveModel() string {
	if strings.TrimSpace(r.DefaultModel) != "" {
		if entry, _, ok := r.Registry.ResolveWithFallback(r.DefaultModel); ok && entry.APIModel != "" {
			return entry.APIModel
		}
	}
	return r.Resolved.Provider.Model
}

// ProfileFor returns the provider profile selected for a role. It returns the active
// profile when role is empty/"default", when there is no modelRoles override for it,
// or when no matching profile is found (non-fatal fallback). The bool is false only
// when a configured override exists but cannot be resolved to a profile.
//
// An UNCONFIGURED built-in default role (one in the DefaultRoleCatalog without a
// modelRoles entry) resolves via its capability: if any saved provider's model
// carries the role's capability (and differs from the active model, so routing is
// meaningful), that profile wins; otherwise it falls back to the active profile. This
// gives omp-style "pre-canned roles work out of the box" without forcing a model —
// explicit modelRoles/active model always override the capability default.
func (r *RoleRouter) ProfileFor(role string) (config.ProviderProfile, bool) {
	if role == "" || role == "default" {
		return r.Resolved.Provider, true
	}
	selector, ok := r.Resolved.ModelRoles[role]
	if !ok || strings.TrimSpace(selector) == "" {
		return r.profileForDefaultRole(role)
	}
	if strings.HasPrefix(strings.TrimSpace(selector), "@") {
		selector, ok = resolveRoleAlias(selector, r.Resolved.ModelRoles, map[string]bool{})
		if !ok {
			return r.Resolved.Provider, false
		}
	}
	return r.profileForSelector(selector)
}

// profileForDefaultRole resolves an unconfigured built-in role to a
// capability-appropriate provider profile. It returns the active profile when the
// role is not a known default role, or when no saved provider's model carries the
// role's capability (or none differs from the effective/active model).
func (r *RoleRouter) profileForDefaultRole(role string) (config.ProviderProfile, bool) {
	info, ok := modelregistry.RoleInfoByID(role)
	if !ok || info.Capability == "" {
		// Unknown/custom free-form role, or a built-in with no capability hint
		// (default/implement) — keep the active profile.
		return r.Resolved.Provider, true
	}
	activeModel := strings.TrimSpace(r.Resolved.Provider.Model)
	exclude := activeModel
	if em := strings.TrimSpace(r.EffectiveModel()); em != "" {
		exclude = em
	}
	for _, p := range r.Resolved.Providers {
		model := strings.TrimSpace(p.Model)
		if model == "" || model == exclude {
			continue
		}
		supported := r.Registry.SupportsCapability(model, info.Capability)
		if info.Capability == modelregistry.ModelCapabilityVision {
			supported = modelregistry.SupportsVision(r.Registry, model)
		}
		if supported {
			return p, true
		}
	}
	// No distinct capable provider: fall back to the active profile (non-fatal).
	return r.Resolved.Provider, true
}

func (r *RoleRouter) profileForSelector(selector string) (config.ProviderProfile, bool) {
	pname, model, hasProvider := splitRoleSelector(selector)
	if hasProvider {
		if p, ok := r.findProfileByName(pname); ok {
			if model != "" {
				p.Model = model
			}
			return p, true
		}
		return r.Resolved.Provider, false
	}
	if p, ok := r.findProfileByModel(selector); ok {
		return p, true
	}
	// A "/"-separated "owner/model" discovery id (models.dev spelling, e.g.
	// "azure/gpt-5.6-luna") whose owner token names a saved provider resolves to
	// that provider with the model overridden. This is a distinct spelling from
	// "owner:model" and repairs role bindings that persisted a discovery id as-is.
	if owner, mdl, ok := splitOwnerSlash(selector); ok {
		if p, ok := r.findProfileByName(owner); ok && mdl != "" {
			p.Model = mdl
			return p, true
		}
	}
	return r.Resolved.Provider, false
}

// ProviderFor builds a runtime provider for a role and returns it with the selected
// profile. On any routing failure it falls back to the active profile (non-fatal).
func (r *RoleRouter) ProviderFor(ctx context.Context, role string) (kajicoderuntime.Provider, config.ProviderProfile, error) {
	profile, ok := r.ProfileFor(role)
	if !ok {
		profile = r.Resolved.Provider
	}
	if r.NewProvider == nil {
		return nil, profile, fmt.Errorf("agent: RoleRouter has no NewProvider builder wired")
	}
	p, err := r.NewProvider(ctx, profile)
	return p, profile, err
}

// FirstVisionCapableProvider returns the first configured profile whose model is
// vision-capable and different from the excluded model. Used by auto vision routing.
func (r *RoleRouter) FirstVisionCapableProvider(excludeModel string) (config.ProviderProfile, bool) {
	for _, p := range r.Resolved.Providers {
		if p.Model != "" && p.Model != excludeModel &&
			modelregistry.SupportsVision(r.Registry, p.Model) {
			return p, true
		}
	}
	return config.ProviderProfile{}, false
}

// resolveRoleAlias expands @role references into a concrete selector, following the
// modelRoles chain with a cycle guard. Returns ("", false) on an unknown role or cycle.
func resolveRoleAlias(value string, roles map[string]string, orbis map[string]bool) (string, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "@") {
		return value, true
	}
	role := strings.TrimPrefix(value, "@")
	if orbis[role] {
		return "", false
	}
	next, ok := roles[role]
	if !ok || strings.TrimSpace(next) == "" {
		return "", false
	}
	orbis[role] = true
	return resolveRoleAlias(next, roles, orbis)
}

// splitRoleSelector splits a "provider:model" selector. Returns hasProvider=true only
// when both sides are non-empty; a lone "model" or a trailing-colon "provider:" form is
// treated as a bare model.
func splitRoleSelector(selector string) (provider, model string, hasProvider bool) {
	if i := strings.IndexByte(selector, ':'); i > 0 && i < len(selector)-1 {
		return selector[:i], selector[i+1:], true
	}
	return "", selector, false
}

func (r *RoleRouter) findProfileByName(name string) (config.ProviderProfile, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, p := range r.Resolved.Providers {
		if strings.ToLower(strings.TrimSpace(p.Name)) == name ||
			strings.ToLower(strings.TrimSpace(p.Provider)) == name ||
			providerKindSlug(p.ProviderKind) == name {
			return p, true
		}
	}
	return config.ProviderProfile{}, false
}

// splitOwnerSlash splits a "/"-separated "owner/model" discovery id. Returns
// ok=true only when both sides are non-empty and no colon is present (a colon
// form is handled by splitRoleSelector instead). The owner token is normalized
// to the trailing provider slug so "https://.../azure/..." matches too.
func splitOwnerSlash(selector string) (owner, model string, ok bool) {
	selector = strings.TrimSpace(selector)
	if selector == "" || strings.Contains(selector, ":") {
		return "", "", false
	}
	// models.dev ids are "provider-slug/model". A URL prefix (https://...) is an
	// accidental paste; take the last path segment before "/model".
	idx := strings.LastIndex(selector, "/")
	if idx <= 0 || idx >= len(selector)-1 {
		return "", "", false
	}
	ownerPart := strings.TrimSpace(selector[:idx])
	model = strings.TrimSpace(selector[idx+1:])
	if model == "" {
		return "", "", false
	}
	// Reduce owner to the trailing slug ("https://foo/provider" -> "provider").
	owner = ownerPart[strings.LastIndex(ownerPart, "/")+1:]
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return "", "", false
	}
	return owner, model, true
}

// providerKindSlug maps a ProviderKind to its conventional provider slug used by
// models.dev discovery ids, so an "azure/gpt-5.6-luna" selector can find an
// azure-openai provider. Falls back to the lowercased kind string.
func providerKindSlug(kind config.ProviderKind) string {
	switch kind {
	case config.ProviderKindAzureOpenAI:
		return "azure"
	case config.ProviderKindOpenAICompatible:
		return "openai-compatible"
	}
	return strings.ToLower(strings.TrimSpace(string(kind)))
}

func (r *RoleRouter) findProfileByModel(model string) (config.ProviderProfile, bool) {
	model = strings.TrimSpace(model)
	if model == "" {
		return config.ProviderProfile{}, false
	}
	// Prefer an exact profile.Model match.
	for _, p := range r.Resolved.Providers {
		if p.Model == model {
			return p, true
		}
	}
	// Then a registry-alias resolution: the selector resolves to a model entry whose
	// APIModel/ID equals a profile's model.
	entry, ok := r.Registry.Resolve(model)
	if !ok {
		return config.ProviderProfile{}, false
	}
	for _, p := range r.Resolved.Providers {
		if p.Model == entry.APIModel || p.Model == entry.ID {
			return p, true
		}
	}
	return config.ProviderProfile{}, false
}
