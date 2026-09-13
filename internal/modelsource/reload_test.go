package modelsource

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestReloadPicksUpNewCache proves the live-refresh path: the snapshot is memoized
// once per process, so writing a new cache file is invisible until Reload drops
// the memo and the next Resolve re-reads it. This is exactly what the /models
// "Refresh models" action relies on to update model facts without a restart.
func TestReloadPicksUpNewCache(t *testing.T) {
	resetForTest()
	t.Cleanup(resetForTest)

	path := filepath.Join(t.TempDir(), "modelsdev.json")
	t.Setenv("KAJICODE_MODELS_CACHE_PATH", path)
	Enable()

	// Version 1: kimi-k3 is image-capable.
	if err := os.WriteFile(path, []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if facts, ok := Lookup("", "moonshotai/kimi-k3"); !ok || !facts.SupportsVision {
		t.Fatalf("v1 kimi-k3 = %+v (ok=%v), want image-capable", facts, ok)
	}

	// Version 2: same key, now text-only and a different context window. Overwrite
	// the cache without a Reload — the memoized snapshot must NOT change.
	const v2 = `{"models":{"moonshotai/kimi-k3":{"id":"moonshotai/kimi-k3","name":"Kimi K3",
	  "modalities":{"input":["text"],"output":["text"]},"limit":{"context":65536,"output":8192}}}}`
	if err := os.WriteFile(path, []byte(v2), 0o644); err != nil {
		t.Fatal(err)
	}
	if facts, _ := Lookup("", "moonshotai/kimi-k3"); !facts.SupportsVision {
		t.Fatal("without Reload the memoized snapshot must still answer from v1")
	}

	// Reload drops the memo; the next Lookup reads v2.
	Reload()
	facts, ok := Lookup("", "moonshotai/kimi-k3")
	if !ok {
		t.Fatal("kimi-k3 should still resolve after reload")
	}
	if facts.SupportsVision {
		t.Error("after Reload, v2 says kimi-k3 is text-only")
	}
	if facts.ContextWindow != 65536 {
		t.Errorf("after Reload context = %d, want v2's 65536", facts.ContextWindow)
	}
}

// TestReloadKeepsBinding pins that Reload clears only the parsed catalog, not the
// session binding — callers pair Reload with an explicit re-Bind.
func TestReloadKeepsBinding(t *testing.T) {
	resetForTest()
	t.Cleanup(resetForTest)
	Bind("opencode", "glm-5")
	t.Cleanup(func() { Bind("", "") })

	Reload()

	provider, id := Bound()
	if provider != "opencode" || id != "glm-5" {
		t.Fatalf("Bound() = (%q,%q), want (opencode,glm-5) preserved across Reload", provider, id)
	}
}

// TestRefreshAndReloadFetchFailureStillReloads pins the graceful degradation: a
// failed fetch (unreachable URL) must leave the store usable — RefreshAndReload
// still reloads from the existing cache/seed and reports the fetch error without
// wiping the snapshot.
func TestRefreshAndReloadFetchFailureStillReloads(t *testing.T) {
	resetForTest()
	t.Cleanup(resetForTest)
	Enable()
	if err := LoadDocument([]byte(fixture)); err != nil {
		t.Fatal(err)
	}
	if _, ok := Lookup("", "moonshotai/kimi-k3"); !ok {
		t.Fatal("fixture should resolve before refresh")
	}
	// Point the fetch at an unroutable address and force a refresh attempt.
	t.Setenv("KAJICODE_MODELS_URL", "http://127.0.0.1:1/catalog.json")
	t.Setenv("KAJICODE_MODELS_CACHE_PATH", filepath.Join(t.TempDir(), "absent.json"))

	err := RefreshAndReload(context.Background())
	if err == nil {
		t.Fatal("unreachable URL should surface a fetch error")
	}
	// A fetch failure must not break the in-process snapshot.
	if _, ok := Lookup("", "moonshotai/kimi-k3"); !ok {
		t.Fatal("snapshot must still resolve after a failed refresh")
	}
}
