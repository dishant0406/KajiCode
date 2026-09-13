package modelsource

import "strings"

// minFuzzyScore is the lowest similarity accepted as a fuzzy match. Below it we
// return no match rather than guess, so an unknown model never silently inherits
// another model's capabilities.
const minFuzzyScore = 0.6

// resolve finds the best record for id. When provider is non-empty its own row is
// tried first (proxy rows carry provider-specific modalities), then the canonical
// provider-agnostic row, then a fuzzy name match. ok is false when nothing clears
// the fuzzy threshold.
func (c *catalog) resolve(provider, id string) (Record, bool) {
	key := normalizeKey(id)
	if key == "" {
		return Record{}, false
	}
	// 1. Exact provider row (only when we know which provider is active).
	if rows, ok := c.byProvider[strings.TrimSpace(provider)]; ok {
		if record, ok := rows[key]; ok {
			return record, true
		}
		if record, ok := matchSuffix(rows, key); ok {
			return record, true
		}
	}
	// 2. Exact canonical row.
	if record, ok := c.canonical[key]; ok {
		return record, true
	}
	if record, ok := matchSuffix(c.canonical, key); ok {
		return record, true
	}
	// 2b. Any provider row with an exact key even from another provider, so a bare
	// id only a proxy lists still resolves.
	for _, rows := range c.byProvider {
		if record, ok := rows[key]; ok {
			return record, true
		}
	}
	// 3. Fuzzy: best-scoring record wins if it clears the threshold.
	best, score := Record{}, 0.0
	for _, entry := range c.all {
		if candidate := scoreMatch(key, entry); candidate > score {
			best, score = entry.record, candidate
		}
	}
	if score >= minFuzzyScore {
		return best, true
	}
	return Record{}, false
}

// matchSuffix finds a row whose model key ends with "/"+key or "-"+key, i.e. the
// provider lists "vendor/<key>" or "vendor-<key>" for a bare query.
func matchSuffix(rows map[string]Record, key string) (Record, bool) {
	for rowKey, record := range rows {
		if strings.HasSuffix(rowKey, "/"+key) || strings.HasSuffix(rowKey, "-"+key) {
			return record, true
		}
	}
	return Record{}, false
}

// scoreMatch scores how well entry matches query, in [0,1]. It rewards an exact
// suffix match (the query is the model's own slug) and otherwise the overlap of
// hyphen/dot tokens.
func scoreMatch(query string, entry indexed) float64 {
	candidate := entry.key
	if query == candidate {
		return 1
	}
	if strings.HasSuffix(candidate, "/"+query) || strings.HasSuffix(candidate, "-"+query) {
		return 0.95
	}
	queryTokens := tokenize(query)
	if len(queryTokens) == 0 {
		return 0
	}
	matched := 0
	for _, token := range queryTokens {
		for _, candidateToken := range entry.tokens {
			if token == candidateToken {
				matched++
				break
			}
		}
	}
	return float64(matched) / float64(len(queryTokens))
}

// tokenize splits on separators used in model ids: /, -, ., :, _ and whitespace.
func tokenize(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case '/', '-', '.', ':', '_', ' ':
			return true
		}
		return false
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if field = strings.TrimSpace(field); field != "" {
			out = append(out, field)
		}
	}
	return out
}

// normalizeKey lowercases and trims a lookup key, dropping a leading "provider/"
// or "provider:" qualifier so "openai/gpt-4.1" and "gpt-4.1" share a key.
func normalizeKey(value string) string {
	key := strings.ToLower(strings.TrimSpace(value))
	if index := strings.IndexByte(key, ':'); index >= 0 {
		key = key[index+1:]
	}
	return strings.TrimSpace(key)
}
