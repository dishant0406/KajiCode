package modelsource

import "strings"

// Facts is the resolved, provider-neutral description of a model that the rest of
// KajiCode consumes: capabilities, limits, pricing, and reasoning tiers. It is a
// plain struct with no JSON tags (Record handles decoding) so it can iterate
// freely. LookupOK is false when no models.dev row matched, in which case the
// zero value means "no facts" and callers keep their curated fallback.
type Facts struct {
	ID     string
	Name   string
	Family string

	LookupOK bool

	SupportsVision    bool
	SupportsPDF       bool
	SupportsTools     bool
	SupportsReasoning bool

	ReasoningEfforts []string

	ContextWindow int
	MaxOutput     int

	InputCostPerMillion       float64
	OutputCostPerMillion      float64
	CachedInputCostPerMillion float64
	CacheWriteCostPerMillion  float64

	Deprecated bool
}

// FactsFromRecord projects a Record into Facts.
func FactsFromRecord(record Record) Facts {
	return Facts{
		ID:                        record.ID,
		Name:                      record.Name,
		Family:                    record.Family,
		LookupOK:                  true,
		SupportsVision:            record.AcceptsImages(),
		SupportsPDF:               record.AcceptsPDF(),
		SupportsTools:             record.ToolCall,
		SupportsReasoning:         record.SupportsReasoning(),
		ReasoningEfforts:          append([]string{}, record.ReasoningEfforts...),
		ContextWindow:             record.ContextWindow,
		MaxOutput:                 record.MaxOutput,
		InputCostPerMillion:       record.InputCost,
		OutputCostPerMillion:      record.OutputCost,
		CachedInputCostPerMillion: record.CacheRead,
		CacheWriteCostPerMillion:  record.CacheWrite,
		Deprecated:                record.Deprecated(),
	}
}

// HasReasoningEffort reports whether tier is one of the model's effort tiers
// (case-insensitive).
func (f Facts) HasReasoningEffort(tier string) bool {
	want := strings.ToLower(strings.TrimSpace(tier))
	for _, value := range f.ReasoningEfforts {
		if strings.ToLower(value) == want {
			return true
		}
	}
	return false
}

// Resolve returns the models.dev facts for a model, looked up by the active
// provider slug and the model id/slug. When the store is disabled, no cache is
// present, or nothing matches, it returns a zero Facts with LookupOK false and
// callers must fall back to curated data.
func Resolve(provider, id string) Facts {
	cat := snapshot()
	if cat == nil {
		return Facts{}
	}
	record, ok := cat.resolve(provider, id)
	if !ok {
		return Facts{}
	}
	return FactsFromRecord(record)
}

// Lookup is Resolve with an explicit ok return, for callers that prefer the
// two-value form.
func Lookup(provider, id string) (Facts, bool) {
	facts := Resolve(provider, id)
	return facts, facts.LookupOK
}

// ResolveRecord returns the raw models.dev record for a model, for callers that
// need the underlying modalities/reasoning-options rather than the projected
// Facts.
func ResolveRecord(provider, id string) (Record, bool) {
	cat := snapshot()
	if cat == nil {
		return Record{}, false
	}
	return cat.resolve(provider, id)
}
