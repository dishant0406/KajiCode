package modelregistry

import "strings"

// The base<->fast model mapping is data, not logic: it lives entirely in each
// ModelEntry's FastVariantID field (populated in the catalog, validated in
// NewRegistry). Adding a pair is a one-line catalog change — never a code change
// here. Both directions derive from that one field: FastVariant walks a model to
// the fast id it names, BaseVariant scans for the model that names id as its fast
// variant. This is why "fast mode" is encoded by model identity and needs no flag.
//
// Only models with an unambiguous 1:1 fast counterpart carry a FastVariantID. A
// model with no clean counterpart (e.g. a top-tier Opus, where both Sonnet and
// Haiku would be candidates) is intentionally left unset, so /fast reports "no
// fast mode available" for it rather than guessing.

// FastVariant resolves id to its configured faster same-family counterpart. It
// resolves id to a concrete entry, reads that entry's FastVariantID, and returns
// the target only when it resolves and is not deprecated. Returns (_, false) when
// id is unknown, has no fast variant, or the variant is unavailable/deprecated.
// Mirrors Registry.UpgradeTarget.
func (registry Registry) FastVariant(id string) (ModelEntry, bool) {
	source, ok := registry.Resolve(id)
	if !ok {
		return ModelEntry{}, false
	}
	fastID := strings.TrimSpace(source.FastVariantID)
	if fastID == "" {
		return ModelEntry{}, false
	}
	target, ok := registry.Resolve(fastID)
	if !ok || target.Status == ModelStatusDeprecated {
		return ModelEntry{}, false
	}
	return target, true
}

// BaseVariant resolves id to the base model that declares id as its fast variant
// — the reverse of FastVariant. A non-empty result therefore means id IS a fast
// model: this is exactly how the /fast command derives "currently in fast mode"
// without storing any flag. Same resolution and availability rules as FastVariant.
func (registry Registry) BaseVariant(id string) (ModelEntry, bool) {
	source, ok := registry.Resolve(id)
	if !ok {
		return ModelEntry{}, false
	}
	for _, entry := range registry.models {
		fastID := strings.TrimSpace(entry.FastVariantID)
		if fastID == "" {
			continue
		}
		target, ok := registry.Resolve(fastID)
		if !ok || target.ID != source.ID {
			continue
		}
		if entry.Status == ModelStatusDeprecated {
			return ModelEntry{}, false
		}
		base, ok := registry.Resolve(entry.ID)
		if !ok {
			return ModelEntry{}, false
		}
		return base, true
	}
	return ModelEntry{}, false
}

// HasFastVariant reports whether id has an available fast variant — i.e. whether
// `/fast on` would switch to a faster model from here. It is the predicate the
// TUI uses to show the "f" fast-mode marker on a model (in the model/provider
// pickers and the active-model indicator).
func (registry Registry) HasFastVariant(id string) bool {
	_, ok := registry.FastVariant(id)
	return ok
}
