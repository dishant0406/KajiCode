package modelregistry

import (
	"fmt"
	"strings"
)

// Resolve maps user input to a model: exact id/api-model/alias first, then a
// regex MatchPattern (e.g. "sonnet 4.5" -> the canonical id). It does NOT apply
// deprecation fallbacks — use ResolveWithFallback for that.
func (registry Registry) Resolve(input string) (ModelEntry, bool) {
	if model, ok := registry.Get(input); ok {
		return model, true
	}
	trimmed := strings.TrimSpace(input)
	for _, pattern := range registry.patterns {
		if pattern.re.MatchString(trimmed) {
			return registry.Get(pattern.modelID)
		}
	}
	return ModelEntry{}, false
}

// ResolveWithFallback resolves input (exact/alias/pattern) and, when the resolved
// model is deprecated and declares a fallback, redirects to the replacement. The
// returned notice is non-empty when a redirect happened or a soft-deprecation
// warning applies, so callers can surface it to the user.
func (registry Registry) ResolveWithFallback(input string) (ModelEntry, string, bool) {
	model, ok := registry.Resolve(input)
	if !ok {
		return ModelEntry{}, "", false
	}
	if model.Status == ModelStatusDeprecated && model.Deprecation != nil && strings.TrimSpace(model.Deprecation.FallbackID) != "" {
		if fallback, ok := registry.Get(model.Deprecation.FallbackID); ok {
			notice := strings.TrimSpace(model.Deprecation.WarningMsg)
			if notice == "" {
				notice = fmt.Sprintf("%s is deprecated; using %s instead", model.ID, fallback.ID)
			}
			return fallback, notice, true
		}
	}
	if model.Deprecation != nil && strings.TrimSpace(model.Deprecation.WarningMsg) != "" {
		return model, strings.TrimSpace(model.Deprecation.WarningMsg), true
	}
	return model, "", true
}

// EffectiveReasoningEffort returns the effort to use for a model: the requested
// value if the model supports it, otherwise the model's default (or first
// supported, or none). It resolves the supported set through
// effectiveReasoningEfforts, the same helper the /effort picker uses, so the two
// never disagree about which tiers a model supports.
func EffectiveReasoningEffort(model ModelEntry, requested ReasoningEffort) ReasoningEffort {
	efforts := effectiveReasoningEfforts(model)
	if requested != "" {
		for _, effort := range efforts {
			if effort == requested {
				return requested
			}
		}
	}
	if model.DefaultReasoningEffort != "" {
		return model.DefaultReasoningEffort
	}
	if len(efforts) > 0 {
		return efforts[0]
	}
	return ReasoningEffortNone
}

// effectiveReasoningEfforts returns a model's supported reasoning efforts. The
// entry's own ReasoningEfforts already carry models.dev's tier list for models it
// knows (the registry layers it in — see modelsource_bridge.go) or the curated
// list otherwise. There is no name-based inference: a model with no known tiers
// advertises none rather than guessing from its name. Both the /effort picker
// (Registry.ReasoningEfforts) and the run-time resolver (EffectiveReasoningEffort)
// read efforts through this single helper, so the two can never disagree.
func effectiveReasoningEfforts(model ModelEntry) []ReasoningEffort {
	return model.ReasoningEfforts
}
