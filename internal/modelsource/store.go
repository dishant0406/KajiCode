package modelsource

import (
	"os"
	"strings"
	"sync"
	"sync/atomic"
)

var (
	cacheOnce   sync.Once
	cachedCat   *catalog
	enabled     atomic.Bool
	storeMu     sync.RWMutex
	overrideCat *catalog // in-memory override (tests / injected snapshot)
)

// Enable turns on use of the on-disk snapshot. The CLI entrypoint calls it once
// at startup; tests and library consumers that never call it fall back to the
// registry's curated facts, so a cache file on the machine can't perturb them.
// KAJICODE_DISABLE_MODELS_FETCH disables both read and refresh.
func Enable() { enabled.Store(true) }

// Enabled reports whether the models.dev snapshot is in use.
func Enabled() bool { return enabled.Load() }

// Disable turns the snapshot off and clears memoization. Paired with Enable for
// tests that must observe the pure curated catalog.
func Disable() {
	enabled.Store(false)
	resetForTest()
}

// snapshot loads the catalog once per process from disk (or an injected override).
// Missing, stale (> maxAge), disabled, or malformed all yield nil and callers use
// their curated fallback. Read once deliberately: Resolve is called on hot paths
// and must not re-stat the file every time.
func snapshot() *catalog {
	if !enabled.Load() || strings.TrimSpace(os.Getenv("KAJICODE_DISABLE_MODELS_FETCH")) != "" {
		return nil
	}
	storeMu.RLock()
	if overrideCat != nil {
		cat := overrideCat
		storeMu.RUnlock()
		return cat
	}
	storeMu.RUnlock()
	cacheOnce.Do(func() {
		// Prefer a fresh on-disk cache; fall back to the embedded seed so a first
		// run with no network still knows common models from baked-in facts rather
		// than from name-pattern guesses.
		if path, err := cachePath(); err == nil {
			if data, err := readFreshCache(path, maxAge); err == nil {
				if cat, err := parseCatalog(data); err == nil {
					cachedCat = cat
					return
				}
			}
		}
		cachedCat = seedCatalog()
	})
	return cachedCat
}

// setOverride injects an in-memory catalog (tests, or a caller that already has a
// document). Passing nil clears it.
func setOverride(cat *catalog) {
	storeMu.Lock()
	overrideCat = cat
	storeMu.Unlock()
}

// resetForTest clears process memoization and disables the store.
func resetForTest() {
	resetSnapshot()
	enabled.Store(false)
	setOverride(nil)
}
