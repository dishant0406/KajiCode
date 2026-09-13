package modelregistry

import "github.com/dishant0406/KajiCode/internal/modelsource"

// SupportsVision reports whether the model identified by modelID accepts image
// input. Facts come from models.dev (see internal/modelsource): the registry
// answer wins for a curated model, then models.dev decides for any model it
// knows — in BOTH directions, so a text-only model is refused authoritatively.
//
// There is deliberately no model-name table here. Name heuristics ("claude-",
// "gpt-5", "*-vl") were a source of drift: they had to be hand-extended for every
// new family and could not say "no" for a model whose name resembled a vision
// family. models.dev carries modalities for real models; the embedded seed covers
// offline first-run. A model unknown to both is treated as not vision-capable.
//
// modelID is resolved through the registry's normal alias/pattern matching, so
// any spelling that Get accepts works here too.
func SupportsVision(registry Registry, modelID string) bool {
	if registry.SupportsCapability(modelID, ModelCapabilityVision) {
		return true
	}
	// The catalog knows this model and it lacks vision: trust that — models.dev
	// cannot overrule a curated "no".
	if _, known := registry.Get(modelID); known {
		return false
	}
	// Not curated. models.dev (snapshot, then embedded seed) is the authority,
	// including an authoritative "no" for a text-only model it knows.
	if record, ok := modelsource.ResolveRecord(boundSlug(), modelID); ok && len(record.InputModalities) > 0 {
		return record.AcceptsImages()
	}
	return false
}

// boundSlug returns the session's bound models.dev provider slug, so a vision
// lookup prefers the provider's own row before the canonical one.
func boundSlug() string {
	provider, _ := modelsource.Bound()
	return provider
}
