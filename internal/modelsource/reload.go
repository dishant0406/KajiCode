package modelsource

import (
	"context"
	"sync"
)

// reloadMu serializes reloads so concurrent callers (e.g. a picker refresh and a
// background refresh) cannot interleave a reset with a read.
var reloadMu sync.Mutex

// Reload drops the in-process snapshot memo so the next Resolve re-reads the
// cache from disk. Call it after the cache file has changed (a fetch, or a manual
// edit) to make the running session see the new facts without a restart — the
// snapshot is otherwise read exactly once per process for hot-path speed.
//
// The caller must re-Bind if the active provider/model may have changed; Reload
// only clears the parsed catalog, not the session binding.
func Reload() {
	reloadMu.Lock()
	defer reloadMu.Unlock()
	resetSnapshot()
}

// RefreshAndReload fetches the models.dev document when the cache is stale, then
// reloads the in-process snapshot so the current session uses what was fetched.
// Unlike Refresh (which is a fire-and-forget benefit-the-next-run writer), this
// is the "make it live now" path used by an explicit user refresh.
//
// It degrades gracefully: a disabled kill-switch or a fetch failure leaves the
// existing (possibly embedded-seed or older-cache) snapshot in place — the reload
// still runs so a cache written by another process is picked up. The returned
// error is the fetch error, if any; callers may surface it but should not treat a
// non-nil error as fatal, since a usable snapshot remains.
func RefreshAndReload(ctx context.Context) error {
	fetchErr := Refresh(ctx)
	Reload()
	return fetchErr
}

// resetSnapshot clears the once-guarded parsed catalog so snapshot() re-reads.
// Caller holds reloadMu (or is a test).
func resetSnapshot() {
	cacheOnce = sync.Once{}
	cachedCat = nil
}
