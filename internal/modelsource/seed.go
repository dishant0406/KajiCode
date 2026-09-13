package modelsource

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"fmt"
	"io"
	"sync"
)

// modelsdev_seed.json.gz is a trimmed snapshot of https://models.dev/catalog.json
// (canonical models plus every provider's rows). It is the last-resort fallback
// when no on-disk cache is present and the network is unavailable — e.g. a first
// run on a locked-down machine. Without it, a first run offline would know
// nothing about any model; with it, common models resolve from facts baked into
// the binary instead of from name-pattern guesses.
//
// Provenance and the regeneration recipe are recorded in seed.go's header and in
// docs/architecture.md. To regenerate, fetch catalog.json and re-write this file
// with the same trimmed shape (see the modelsource package docs).
//
//go:embed modelsdev_seed.json.gz
var seedGzip []byte

var (
	seedOnce sync.Once
	seedCat  *catalog
)

// seedCatalog returns the embedded models.dev snapshot, parsed once per process.
// It is used only when the on-disk cache is unavailable, so a stale binary seed
// never shadows a fresh cache.
func seedCatalog() *catalog {
	seedOnce.Do(func() {
		reader, err := gzip.NewReader(bytes.NewReader(seedGzip))
		if err != nil {
			return
		}
		defer reader.Close()
		// The seed is trusted build input, but cap the read anyway so a corrupt
		// embed cannot balloon memory.
		data, err := io.ReadAll(io.LimitReader(reader, fetchLimit))
		if err != nil {
			return
		}
		cat, err := parseCatalog(data)
		if err != nil {
			return
		}
		seedCat = cat
	})
	return seedCat
}

// SeedSummary describes the embedded seed for diagnostics (how many models and
// providers it carries, or ok=false when the embed is unreadable).
func SeedSummary() (models int, providers int, ok bool) {
	cat := seedCatalog()
	if cat == nil {
		return 0, 0, false
	}
	return len(cat.canonical), len(cat.byProvider), true
}

// loadSeedDocument decompresses and parses the embedded seed. Exposed for tests
// that want to assert the embedded document is well-formed.
func loadSeedDocument() ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(seedGzip))
	if err != nil {
		return nil, fmt.Errorf("modelsource: open seed: %w", err)
	}
	defer reader.Close()
	return io.ReadAll(io.LimitReader(reader, fetchLimit))
}
