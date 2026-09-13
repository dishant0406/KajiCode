package modelsource

import (
	"encoding/json"
	"fmt"
	"sync"
)

// active holds the session's selected provider slug and model id. It is bound once
// the CLI has resolved the effective model, and everyone else can then ask for the
// session's facts without plumbing the pair through every call site.
var active struct {
	sync.RWMutex
	provider string
	id       string
}

// Bind records the session's active provider slug and model id. Empty values are
// allowed; Resolve simply has less to match on.
func Bind(provider, id string) {
	active.Lock()
	active.provider = provider
	active.id = id
	active.Unlock()
}

// Bound returns the session's active provider slug and model id.
func Bound() (provider, id string) {
	active.RLock()
	defer active.RUnlock()
	return active.provider, active.id
}

// ActiveFacts returns the facts for the session's bound model, or a zero Facts
// when the store is disabled or nothing matches.
func ActiveFacts() Facts {
	provider, id := Bound()
	return Resolve(provider, id)
}

// LoadDocument parses a models.dev document and installs it as the in-memory
// snapshot, bypassing the on-disk cache. It accepts the catalog.json shape (which
// carries both canonical models and provider rows), the api.json shape, or the
// models.json shape. Used by tests and by callers that fetched the document
// themselves.
func LoadDocument(data []byte) error {
	cat, err := unmarshalSource(data)
	if err != nil {
		return err
	}
	setOverride(cat)
	return nil
}

// unmarshalSource picks the parser for a document by trying catalog.json shape
// first and falling back to api.json / models.json.
func unmarshalSource(data []byte) (*catalog, error) {
	if cat, err := parseCatalog(data); err == nil {
		return cat, nil
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("modelsource: unrecognized document: %w", err)
	}
	if _, ok := probe["providers"]; ok {
		return parseCatalog(data)
	}
	if cat, err := parseAPI(data); err == nil {
		return cat, nil
	}
	return parseModels(data)
}
