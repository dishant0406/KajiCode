// Package modelsource resolves a model's real capabilities and limits from the
// public models.dev database (https://models.dev) instead of hardcoding them.
//
// It is deliberately plain: it fetches one JSON document (api.json), caches it on
// disk, and answers questions like "does this model accept images?" or "how big
// is its context window?" by looking the model up — exact id first, then a fuzzy
// name match. Nothing here imports modelregistry, so any layer can use it.
package modelsource

import "strings"

// Record is one model's facts as published by models.dev. Fields are the union of
// what api.json and models.json contain; a provider row and the canonical row for
// the same model carry the same shape.
type Record struct {
	ID       string
	Name     string
	Family   string
	Provider string // models.dev provider slug this row came from ("" = canonical)

	InputModalities  []string
	OutputModalities []string

	Attachment bool // models.dev flag: accepts file/image attachments
	Reasoning  bool
	ToolCall   bool

	// ReasoningEfforts are the effort tiers from reasoning_options, e.g.
	// ["low","medium","high"]. Empty means the model has no effort tiers.
	ReasoningEfforts []string

	ContextWindow int
	MaxOutput     int

	InputCost  float64
	OutputCost float64
	CacheRead  float64
	CacheWrite float64

	Status string // "", "beta", or "deprecated"
}

// AcceptsImages reports whether the model lists "image" as an input modality.
func (r Record) AcceptsImages() bool {
	return hasModality(r.InputModalities, "image")
}

// AcceptsPDF reports whether the model lists "pdf" as an input modality.
func (r Record) AcceptsPDF() bool {
	return hasModality(r.InputModalities, "pdf")
}

// SupportsReasoning reports whether the model emits reasoning and/or exposes
// effort tiers.
func (r Record) SupportsReasoning() bool {
	return r.Reasoning || len(r.ReasoningEfforts) > 0
}

// Deprecated reports whether models.dev marks the model deprecated.
func (r Record) Deprecated() bool {
	return strings.EqualFold(strings.TrimSpace(r.Status), "deprecated")
}

func hasModality(modalities []string, want string) bool {
	for _, m := range modalities {
		if strings.EqualFold(strings.TrimSpace(m), want) {
			return true
		}
	}
	return false
}

// rawRecord mirrors the JSON shape of a models.dev model object. It is
// unexported and only exists to decode; the public Record is what callers see.
type rawRecord struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Family     string `json:"family"`
	Attachment bool   `json:"attachment"`
	Reasoning  bool   `json:"reasoning"`
	ToolCall   bool   `json:"tool_call"`
	Status     string `json:"status"`
	Modalities struct {
		Input  []string `json:"input"`
		Output []string `json:"output"`
	} `json:"modalities"`
	ReasoningOptions []struct {
		Type   string   `json:"type"`
		Values []string `json:"values"`
	} `json:"reasoning_options"`
	Limit struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	} `json:"limit"`
	Cost struct {
		Input      float64 `json:"input"`
		Output     float64 `json:"output"`
		CacheRead  float64 `json:"cache_read"`
		CacheWrite float64 `json:"cache_write"`
	} `json:"cost"`
}

// toRecord converts a decoded row into the public Record. provider is the slug the
// row came from ("" for a canonical row); fallbackID is used when the row omits id.
func (r rawRecord) toRecord(provider, fallbackID string) Record {
	id := strings.TrimSpace(r.ID)
	if id == "" {
		id = fallbackID
	}
	return Record{
		ID:               id,
		Name:             strings.TrimSpace(r.Name),
		Family:           strings.TrimSpace(r.Family),
		Provider:         provider,
		InputModalities:  cleanStrings(r.Modalities.Input),
		OutputModalities: cleanStrings(r.Modalities.Output),
		Attachment:       r.Attachment,
		Reasoning:        r.Reasoning,
		ToolCall:         r.ToolCall,
		ReasoningEfforts: effortTiers(r.ReasoningOptions),
		ContextWindow:    r.Limit.Context,
		MaxOutput:        r.Limit.Output,
		InputCost:        r.Cost.Input,
		OutputCost:       r.Cost.Output,
		CacheRead:        r.Cost.CacheRead,
		CacheWrite:       r.Cost.CacheWrite,
		Status:           strings.TrimSpace(r.Status),
	}
}

// effortTiers pulls the effort values out of reasoning_options, e.g.
// [{"type":"effort","values":["low","medium","high"]}] -> ["low","medium","high"].
// Options that only describe a toggle/budget carry no tiers.
func effortTiers(options []struct {
	Type   string   `json:"type"`
	Values []string `json:"values"`
}) []string {
	var tiers []string
	for _, option := range options {
		if strings.EqualFold(strings.TrimSpace(option.Type), "effort") {
			tiers = append(tiers, cleanStrings(option.Values)...)
		}
	}
	return tiers
}

func cleanStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
