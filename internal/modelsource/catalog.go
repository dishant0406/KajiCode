package modelsource

import (
	"encoding/json"
	"fmt"
	"strings"
)

// catalog is a decoded models.dev document. It keeps three indexes:
//
//   - byProvider: provider slug -> normalized model key -> Record. Used to prefer
//     the exact row the active provider serves (proxy rows carry their own
//     modalities).
//   - canonical: normalized model key -> Record, from the provider-agnostic
//     models.json document (keyed "<lab>/<slug>").
//   - all: every record with its normalized key and pre-tokenized key, for the
//     fuzzy fallback.
//
// Keys are normalized with normalizeKey so lookup ignores case and separators.
type catalog struct {
	byProvider map[string]map[string]Record
	canonical  map[string]Record
	all        []indexed
}

// indexed is a Record plus its lookup key and pre-tokenized key, so fuzzy scoring
// never re-tokenizes the whole catalog on each miss.
type indexed struct {
	key    string
	tokens []string
	record Record
}

// parseCatalog decodes catalog.json: {"models": {"<lab>/<slug>": <record>},
// "providers": {"<slug>": {"models": {"<key>": <record>}}}}. This single document
// carries both the canonical model list and every provider's rows, so one fetch
// indexes everything.
func parseCatalog(data []byte) (*catalog, error) {
	var doc struct {
		Models    map[string]rawRecord `json:"models"`
		Providers map[string]struct {
			Models map[string]rawRecord `json:"models"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("modelsource: parse catalog.json: %w", err)
	}
	cat := &catalog{
		byProvider: make(map[string]map[string]Record, len(doc.Providers)),
		canonical:  make(map[string]Record, len(doc.Models)),
	}
	for key, raw := range doc.Models {
		record := raw.toRecord("", key)
		cat.canonical[normalizeKey(key)] = record
	}
	for slug, provider := range doc.Providers {
		if len(provider.Models) == 0 {
			continue
		}
		rows := make(map[string]Record, len(provider.Models))
		for key, raw := range provider.Models {
			rows[normalizeKey(key)] = raw.toRecord(slug, key)
		}
		cat.byProvider[strings.TrimSpace(slug)] = rows
	}
	if len(cat.canonical) == 0 && len(cat.byProvider) == 0 {
		return nil, fmt.Errorf("modelsource: catalog.json has no models")
	}
	cat.reindex()
	return cat, nil
}

// parseAPI decodes api.json: {"<provider>": {"models": {"<key>": <record>}}}.
func parseAPI(data []byte) (*catalog, error) {
	var doc map[string]struct {
		Models map[string]rawRecord `json:"models"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("modelsource: parse api.json: %w", err)
	}
	cat := &catalog{byProvider: make(map[string]map[string]Record, len(doc))}
	for slug, provider := range doc {
		if len(provider.Models) == 0 {
			continue
		}
		rows := make(map[string]Record, len(provider.Models))
		for key, raw := range provider.Models {
			rows[normalizeKey(key)] = raw.toRecord(slug, key)
		}
		cat.byProvider[strings.TrimSpace(slug)] = rows
	}
	if len(cat.byProvider) == 0 {
		return nil, fmt.Errorf("modelsource: api.json has no providers")
	}
	cat.reindex()
	return cat, nil
}

// parseModels decodes models.json: {"<lab>/<slug>": <record>}.
func parseModels(data []byte) (*catalog, error) {
	var doc map[string]rawRecord
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("modelsource: parse models.json: %w", err)
	}
	cat := &catalog{canonical: make(map[string]Record, len(doc))}
	for key, raw := range doc {
		cat.canonical[normalizeKey(key)] = raw.toRecord("", key)
	}
	if len(cat.canonical) == 0 {
		return nil, fmt.Errorf("modelsource: models.json has no models")
	}
	cat.reindex()
	return cat, nil
}

// reindex rebuilds the fuzzy index from the canonical and provider maps. Provider
// rows come after canonical rows so an exact provider row is still preferred by
// resolve (it is tried first); ties in fuzzy scoring keep the first seen.
func (c *catalog) reindex() {
	c.all = c.all[:0]
	for key, record := range c.canonical {
		c.all = append(c.all, indexed{key: key, tokens: tokenize(key), record: record})
	}
	for _, rows := range c.byProvider {
		for key, record := range rows {
			c.all = append(c.all, indexed{key: key, tokens: tokenize(key), record: record})
		}
	}
}
