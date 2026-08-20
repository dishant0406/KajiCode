package modelregistry

import (
	"fmt"
	"strings"
)

// RoleRef is a single model selector for a task role. It is the resolved form of a
// modelRoles value after alias/`@role` expansion.
type RoleRef struct {
	// Selector is the expanded, alias-free selector. Empty means "no role override
	// is meaningful; fall back to the active model".
	Selector string
	// ProviderHint is the optional "provider:" prefix on the selector ("" if none),
	// already stripped from Selector.
	ProviderHint string
	// FromAlias reports whether this came from an `@role` alias expansion (used only
	// for cycle diagnostics + transcript messages).
	FromAlias bool
}

// ExpandRoleSelector expands a single modelRoles value. Supported forms:
//   - ""            -> empty (caller falls back to active model)
//   - "@role"       -> recursively expand modelRoles[role] (requires a roles map)
//   - "provider:model" / "alias" / "model" -> pass through unchanged (bare model)
//
// Returns (expanded, error). A cycle (@a -> @b -> @a) is an error; an unknown @role
// is an error. orbis is the set of role names already being expanded, to detect cycles.
func ExpandRoleSelector(selector string, roles map[string]string, orbis map[string]bool) (RoleRef, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return RoleRef{}, nil
	}
	if strings.HasPrefix(selector, "@") {
		role := strings.TrimPrefix(selector, "@")
		if orbis[role] {
			return RoleRef{}, fmt.Errorf("model role cycle detected involving %q", role)
		}
		if orbis == nil {
			orbis = map[string]bool{}
		}
		next, ok := roles[role]
		if !ok {
			return RoleRef{}, fmt.Errorf("unknown model role %q", role)
		}
		orbis[role] = true
		ref, err := ExpandRoleSelector(next, roles, orbis)
		ref.FromAlias = true
		return ref, err
	}
	// bare / provider:model — split the provider hint if a ":" is present.
	ref := RoleRef{Selector: selector}
	if i := strings.IndexByte(selector, ':'); i > 0 {
		ref.ProviderHint = selector[:i]
		ref.Selector = selector[i+1:]
		// If both sides are non-empty it's provider:model; else it's a lone model.
		if ref.Selector == "" {
			ref.ProviderHint = ""
			ref.Selector = selector
		}
	}
	return ref, nil
}

// ResolveRole resolves a modelRoles value against the registry to a ModelEntry.
// It expands @role aliases first, then runs the existing Resolve on the final model
// text. Returns the entry, whether a provider hint is present, and ok.
func (registry Registry) ResolveRole(value string, roles map[string]string) (ModelEntry, bool, bool) {
	ref, err := ExpandRoleSelector(value, roles, nil)
	if err != nil {
		return ModelEntry{}, false, false
	}
	if ref.Selector == "" {
		return ModelEntry{}, false, false // no override
	}
	entry, ok := registry.Resolve(ref.Selector)
	if !ok {
		// Maybe a deprecated id — honor the deprecation redirect.
		entry, _, ok = registry.ResolveWithFallback(ref.Selector)
	}
	return entry, ref.ProviderHint != "", ok
}

// ResolveRoleWithFallback expands a value, then resolves it, honoring deprecation
// redirects, returning the effective entry and an optional notice.
func (registry Registry) ResolveRoleWithFallback(value string, roles map[string]string) (ModelEntry, string, bool) {
	ref, err := ExpandRoleSelector(value, roles, nil)
	if err != nil {
		return ModelEntry{}, "", false
	}
	if ref.Selector == "" {
		return ModelEntry{}, "", false
	}
	entry, notice, ok := registry.ResolveWithFallback(ref.Selector)
	return entry, notice, ok
}

// MatchRoleProvider filters a resolved entry against a provider hint. If the hint is
// non-empty and does not match the entry's Provider (or any of its APIProviders),
// it is treated as not-matching so the caller can fall back. normalize is typically
// strings.ToLower.
func (entry ModelEntry) MatchRoleProvider(hint string, normalize func(string) string) bool {
	if hint == "" {
		return true
	}
	match := func(k ProviderKind) bool { return normalize(string(k)) == normalize(hint) }
	if match(entry.Provider) {
		return true
	}
	for _, p := range entry.APIProviders {
		if match(p) {
			return true
		}
	}
	return false
}
