package modelsource

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Endpoints and cache policy. catalog.json carries the canonical model list and
// every provider's rows in one document, so it is the single fetch.
const (
	defaultURL = "https://models.dev/catalog.json"

	refreshAfter = 24 * time.Hour
	maxAge       = 7 * 24 * time.Hour
	fetchLimit   = 64 << 20 // 64 MiB response guard (catalog.json is ~5 MB today)
	fetchWindow  = 20 * time.Second
)

// cachePath returns the on-disk cache location.
// KAJICODE_MODELS_CACHE_PATH overrides it.
func cachePath() (string, error) {
	if override := strings.TrimSpace(os.Getenv("KAJICODE_MODELS_CACHE_PATH")); override != "" {
		return override, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "kajicode", "modelsdev.json"), nil
}

// sourceURL returns the fetch URL. KAJICODE_MODELS_URL overrides it.
func sourceURL() string {
	if override := strings.TrimSpace(os.Getenv("KAJICODE_MODELS_URL")); override != "" {
		return override
	}
	return defaultURL
}

// SnapshotPath returns the resolved cache path (for diagnostics).
func SnapshotPath() (string, error) { return cachePath() }

// readFreshCache reads a cache file when it exists and is younger than ttl.
func readFreshCache(path string, ttl time.Duration) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if ttl > 0 && time.Since(info.ModTime()) > ttl {
		return nil, fmt.Errorf("modelsource: cache %s older than %s", path, ttl)
	}
	return os.ReadFile(path)
}

// Refresh fetches the models.dev document into the on-disk cache when the cache is
// missing or older than refreshAfter. Safe to call fire-and-forget from startup:
// it never affects the current process (see snapshot). The URL can be overridden
// with KAJICODE_MODELS_URL; KAJICODE_DISABLE_MODELS_FETCH disables it entirely.
func Refresh(ctx context.Context) error {
	if strings.TrimSpace(os.Getenv("KAJICODE_DISABLE_MODELS_FETCH")) != "" {
		return nil
	}
	path, err := cachePath()
	if err != nil {
		return err
	}
	if _, err := readFreshCache(path, refreshAfter); err == nil {
		return nil
	}
	data, err := fetch(ctx, sourceURL())
	if err != nil {
		return err
	}
	// Validate before persisting: a bad body must never clobber a good cache.
	if _, err := parseCatalog(data); err != nil {
		return err
	}
	return writeCache(path, data)
}

// fetch GETs url and returns the body, capped at fetchLimit.
func fetch(ctx context.Context, url string) ([]byte, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, fetchWindow)
	defer cancel()
	request, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "kajicode-models/0.1")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("modelsource: fetch %s: HTTP %d", url, response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, fetchLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > fetchLimit {
		return nil, fmt.Errorf("modelsource: response exceeds %d byte limit", fetchLimit)
	}
	return data, nil
}

// writeCache atomically replaces the cache file with data.
func writeCache(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "modelsdev-*.json")
	if err != nil {
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		_ = os.Remove(temp.Name())
		return err
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(temp.Name())
		return err
	}
	return os.Rename(temp.Name(), path)
}

// ErrDisabled is returned by Refresh when fetching is disabled.
var ErrDisabled = errors.New("modelsource: fetching disabled")
