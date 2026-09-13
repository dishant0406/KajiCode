package modelregistry

import (
	"strings"

	"github.com/dishant0406/KajiCode/internal/modelsource"
)

// models.dev is the authority for a model's volatile facts: modalities (vision),
// reasoning support and effort tiers, context/output limits, and pricing. The
// curated catalog remains the authority for identity (ids, aliases, match
// patterns, deprecations, escalation targets) and is the offline fallback. These
// bridges project a models.dev record onto a curated entry, and synthesize an
// entry for a model models.dev knows but the curated catalog does not — so every
// reader (vision gates, compaction sizing, /effort, cost math, picker labels)
// agrees on one record without each call site talking to models.dev.

// applyModelsDevFacts layers models.dev facts onto curated entries. When the
// snapshot knows a model, its modalities/reasoning/limits/pricing win; identity
// fields stay curated. Entries the snapshot does not know are left untouched.
func applyModelsDevFacts(entries []ModelEntry) []ModelEntry {
	for i := range entries {
		entry := &entries[i]
		record, ok := modelsource.ResolveRecord(modelsDevSlugFor(entry.Provider), entry.APIModel)
		if !ok {
			continue
		}
		applyRecordToEntry(entry, record)
	}
	return entries
}

// synthesizeModelEntry builds a ModelEntry for a lookup pattern the curated
// catalog does not know but models.dev does — e.g. a proxy / custom /
// openai-compatible id. ok is false when models.dev has no match, so the caller
// keeps its existing "unknown model" behavior.
func synthesizeModelEntry(pattern string) (ModelEntry, bool) {
	providerSlug, _ := modelsource.Bound()
	record, ok := modelsource.ResolveRecord(providerSlug, pattern)
	if !ok {
		return ModelEntry{}, false
	}
	id := strings.TrimSpace(pattern)
	entry := ModelEntry{
		ID:            id,
		DisplayName:   firstNonBlank(record.Name, id),
		APIModel:      id,
		Provider:      ProviderOpenAICompatible,
		APIProviders:  []ProviderKind{ProviderOpenAICompatible},
		ContextLimits: ContextLimits{ContextWindow: record.ContextWindow, MaxOutputTokens: record.MaxOutput},
		Capabilities:  capabilitiesFromRecord(record),
		Cost: ModelCost{
			Currency:              "USD",
			Unit:                  "per_1m_tokens",
			InputPerMillion:       record.InputCost,
			OutputPerMillion:      record.OutputCost,
			CachedInputPerMillion: record.CacheRead,
			CacheWritePerMillion:  record.CacheWrite,
			Source:                "models.dev",
			SourceLastVerified:    sourceLastVerified,
		},
		Status:      statusFromRecord(record),
		Aliases:     []string{id},
		Description: firstNonBlank(record.Name, id),
	}
	applyReasoningEfforts(&entry, record)
	return entry, true
}

// SynthesizeModelEntry exposes synthesizedModelEntry to callers outside this
// package (e.g. the providers factory and the CLI) that need facts for a model
// the curated catalog does not know but models.dev does.
func SynthesizeModelEntry(pattern string) (ModelEntry, bool) {
	return synthesizeModelEntry(pattern)
}

// applyRecordToEntry mutates a curated entry with models.dev's facts. Only fields
// models.dev actually reports are overwritten; zero values are left alone so a
// sparse record cannot blank a curated fact.
func applyRecordToEntry(entry *ModelEntry, record modelsource.Record) {
	if record.ContextWindow > 0 {
		entry.ContextLimits.ContextWindow = record.ContextWindow
	}
	if record.MaxOutput > 0 {
		entry.ContextLimits.MaxOutputTokens = record.MaxOutput
	}
	// Modalities are authoritative: models.dev says image-or-not.
	if len(record.InputModalities) > 0 {
		setCapability(entry, ModelCapabilityVision, record.AcceptsImages())
	}
	applyReasoningEfforts(entry, record)
	if record.ToolCall {
		setCapability(entry, ModelCapabilityToolCalling, true)
	}
	if entry.ContextLimits.ContextWindow >= 200_000 {
		setCapability(entry, ModelCapabilityLongContext, true)
	}
	if entry.Status == "" {
		entry.Status = statusFromRecord(record)
	}
	// Tiered pricing (e.g. Gemini >200k) has no models.dev equivalent; never mix a
	// flat live base rate with curated tiers.
	if len(entry.Cost.Tiers) == 0 && record.InputCost > 0 && record.OutputCost > 0 {
		entry.Cost.InputPerMillion = record.InputCost
		entry.Cost.OutputPerMillion = record.OutputCost
		if record.CacheRead > 0 {
			entry.Cost.CachedInputPerMillion = record.CacheRead
		}
		if record.CacheWrite > 0 {
			entry.Cost.CacheWritePerMillion = record.CacheWrite
		}
		entry.Cost.Source = "models.dev"
	}
}

// applyReasoningEfforts maps models.dev's effort tiers onto the entry's
// ReasoningEfforts, keeping only tiers KajiCode understands. When models.dev
// reports reasoning but no explicit tiers, a standard low/medium/high set is used
// (the same set a reasoning-capable curated model would carry).
func applyReasoningEfforts(entry *ModelEntry, record modelsource.Record) {
	efforts := mapEffortTiers(record.ReasoningEfforts)
	if len(efforts) > 0 {
		entry.ReasoningEfforts = efforts
		if !entryHasEffort(entry, entry.DefaultReasoningEffort) {
			entry.DefaultReasoningEffort = defaultEffortFor(efforts)
		}
		setCapability(entry, ModelCapabilityReasoning, true)
		return
	}
	if record.Reasoning {
		entry.ReasoningEfforts = standardReasoningEfforts()
		if entry.DefaultReasoningEffort == "" {
			entry.DefaultReasoningEffort = ReasoningEffortMedium
		}
		setCapability(entry, ModelCapabilityReasoning, true)
	}
}

// mapEffortTiers converts models.dev effort strings to registry effort constants,
// dropping unknown values. KajiCode's "xhigh" maps to models.dev's "xhigh"/"max".
func mapEffortTiers(tiers []string) []ReasoningEffort {
	var efforts []ReasoningEffort
	seen := map[ReasoningEffort]struct{}{}
	for _, tier := range tiers {
		effort := ReasoningEffort(strings.ToLower(strings.TrimSpace(tier)))
		if !ValidReasoningEffort(effort) {
			if effort == "max" {
				effort = ReasoningEffortMax
			} else {
				continue
			}
		}
		if _, ok := seen[effort]; ok {
			continue
		}
		seen[effort] = struct{}{}
		efforts = append(efforts, effort)
	}
	return efforts
}

// capabilitiesFromRecord builds the capability list for a synthesized entry: the
// base chat/streaming/tool/system set plus vision, reasoning, and long-context as
// models.dev reports them.
func capabilitiesFromRecord(record modelsource.Record) []ModelCapability {
	caps := withBaseCapabilities()
	if record.AcceptsImages() {
		caps = append(caps, ModelCapabilityVision)
	}
	if record.SupportsReasoning() {
		caps = append(caps, ModelCapabilityReasoning)
	}
	if record.ContextWindow >= 200_000 {
		caps = append(caps, ModelCapabilityLongContext)
	}
	return caps
}

func statusFromRecord(record modelsource.Record) ModelStatus {
	if record.Deprecated() {
		return ModelStatusDeprecated
	}
	return ModelStatusActive
}

func setCapability(entry *ModelEntry, capability ModelCapability, enabled bool) {
	has := entry.Supports(capability)
	switch {
	case enabled && !has:
		entry.Capabilities = append(entry.Capabilities, capability)
	case !enabled && has:
		filtered := entry.Capabilities[:0]
		for _, existing := range entry.Capabilities {
			if existing != capability {
				filtered = append(filtered, existing)
			}
		}
		entry.Capabilities = filtered
	}
}

func entryHasEffort(entry *ModelEntry, effort ReasoningEffort) bool {
	if effort == "" {
		return true
	}
	for _, existing := range entry.ReasoningEfforts {
		if existing == effort {
			return true
		}
	}
	return false
}

// defaultEffortFor returns the middle tier when one exists, else the highest.
func defaultEffortFor(efforts []ReasoningEffort) ReasoningEffort {
	if len(efforts) == 0 {
		return ""
	}
	for _, effort := range efforts {
		if effort == ReasoningEffortMedium {
			return ReasoningEffortMedium
		}
	}
	return efforts[len(efforts)-1]
}

// modelsDevSlugFor maps a primary provider kind to its models.dev provider slug.
func modelsDevSlugFor(kind ProviderKind) string {
	switch kind {
	case ProviderAnthropic:
		return "anthropic"
	case ProviderOpenAI:
		return "openai"
	case ProviderGoogle:
		return "google"
	default:
		return ""
	}
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
