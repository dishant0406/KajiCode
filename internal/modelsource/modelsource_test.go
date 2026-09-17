package modelsource

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fixture is a trimmed catalog.json: a canonical model list plus provider rows,
// shaped exactly like https://models.dev/catalog.json.
const fixture = `{
  "models": {
    "moonshotai/kimi-k3": {
      "id": "moonshotai/kimi-k3", "name": "Kimi K3", "family": "kimi",
      "reasoning": false, "tool_call": true,
      "modalities": {"input": ["text", "image", "video"], "output": ["text"]},
      "limit": {"context": 1048576, "output": 131072},
      "cost": {"input": 0.6, "output": 2.4, "cache_read": 0.15}
    },
    "meta/muse-spark-1.3": {
      "id": "meta/muse-spark-1.3", "name": "Muse Spark 1.3",
      "tool_call": true,
      "modalities": {"input": ["text", "image", "video", "pdf", "audio"], "output": ["text"]},
      "limit": {"context": 200000, "output": 32000}
    },
    "zhipuai/glm-5.x": {
      "id": "zhipuai/glm-5.x", "name": "GLM-5.x", "reasoning": true,
      "reasoning_options": [{"type": "toggle"}, {"type": "effort", "values": ["low", "medium", "high"]}],
      "tool_call": true,
      "modalities": {"input": ["text"], "output": ["text"]},
      "limit": {"context": 200000, "output": 32000},
      "cost": {"input": 0.5, "output": 1.5}
    },
    "openai/gpt-4-turbo": {
      "id": "openai/gpt-4-turbo", "name": "GPT-4 Turbo", "status": "deprecated",
      "modalities": {"input": ["text", "image"], "output": ["text"]},
      "limit": {"context": 128000, "output": 4096}
    }
  },
  "providers": {
    "opencode": {
      "npm": "@ai-sdk/openai-compatible",
      "models": {
        "glm-5": {
          "id": "glm-5", "name": "GLM-5",
          "modalities": {"input": ["text"], "output": ["text"]},
          "limit": {"context": 128000, "output": 16000}
        },
        "union-alpha": {
          "id": "union-alpha", "name": "Union Alpha",
          "provider": {"npm": "@ai-sdk/anthropic"},
          "modalities": {"input": ["text"], "output": ["text"]},
          "limit": {"context": 200000, "output": 32000}
        },
        "muse-spark-1.3": {
          "id": "muse-spark-1.3", "name": "Muse Spark 1.3",
          "provider": {"npm": "@ai-sdk/openai"},
          "modalities": {"input": ["text"], "output": ["text"]},
          "limit": {"context": 200000, "output": 32000}
        }
      }
    },
    "tokengo": {
      "models": {
        "z-ai/glm-5.3-flash": {
          "id": "z-ai/glm-5.3-flash", "name": "GLM 5.3 Flash",
          "modalities": {"input": ["text", "image", "video", "pdf"], "output": ["text"]},
          "limit": {"context": 200000, "output": 32000}
        }
      }
    }
  }
}`

func loadFixture(t *testing.T) {
	t.Helper()
	resetForTest()
	t.Cleanup(resetForTest)
	Enable()
	if err := LoadDocument([]byte(fixture)); err != nil {
		t.Fatalf("LoadDocument: %v", err)
	}
}

func TestResolveNPM(t *testing.T) {
	loadFixture(t)
	// Only a per-model npm override produces a protocol signal; a row without one
	// has none (the provider's kind is the default, never inherited here).
	if rec, ok := ResolveRecord("opencode", "union-alpha"); !ok || rec.NPM != "@ai-sdk/anthropic" {
		t.Errorf("union-alpha npm = %q (ok=%v), want @ai-sdk/anthropic", rec.NPM, ok)
	}
	if rec, ok := ResolveRecord("opencode", "muse-spark-1.3"); !ok || rec.NPM != "@ai-sdk/openai" {
		t.Errorf("muse-spark-1.3 npm = %q (ok=%v), want @ai-sdk/openai", rec.NPM, ok)
	}
	if rec, ok := ResolveRecord("opencode", "glm-5"); !ok || rec.NPM != "" {
		t.Errorf("glm-5 npm = %q (ok=%v), want empty (no model-level override)", rec.NPM, ok)
	}
}

func TestProviderRecordIsExact(t *testing.T) {
	loadFixture(t)
	// A row that exists only for another provider (or canonical) must not resolve
	// through the exact provider lookup, so no custom provider inherits it.
	if _, ok := ProviderRecord("freeform-gateway", "union-alpha"); ok {
		t.Error("ProviderRecord must not fall back to another provider's row")
	}
	if rec, ok := ProviderRecord("opencode", "union-alpha"); !ok || rec.NPM != "@ai-sdk/anthropic" {
		t.Errorf("ProviderRecord(opencode, union-alpha) = %q (ok=%v), want @ai-sdk/anthropic", rec.NPM, ok)
	}
}

// TestEmbeddedSeedCarriesNPM guards the offline first-run path: the embedded
// snapshot must parse and carry model-level npm overrides, so a machine with no
// cache and no network still routes a model like union-alpha to its real endpoint
// instead of defaulting to chat-completions.
func TestEmbeddedSeedCarriesNPM(t *testing.T) {
	models, providers, ok := SeedSummary()
	if !ok || models == 0 || providers == 0 {
		t.Fatalf("embedded seed unreadable: models=%d providers=%d ok=%v", models, providers, ok)
	}
	cat := seedCatalog()
	rows := cat.byProvider["opencode"]
	if rows == nil {
		t.Fatal("embedded seed has no opencode provider rows")
	}
	if rec, ok := rows[normalizeKey("union-alpha")]; !ok || rec.NPM != "@ai-sdk/anthropic" {
		t.Errorf("seed opencode/union-alpha npm = %q (ok=%v), want @ai-sdk/anthropic", rec.NPM, ok)
	}
}

func TestResolveCanonicalVision(t *testing.T) {
	loadFixture(t)
	facts, ok := Lookup("", "moonshotai/kimi-k3")
	if !ok {
		t.Fatal("expected kimi-k3 to resolve")
	}
	if !facts.SupportsVision {
		t.Error("kimi-k3 should accept images")
	}
	if facts.ContextWindow != 1048576 {
		t.Errorf("context = %d, want 1048576", facts.ContextWindow)
	}
	if facts.InputCostPerMillion != 0.6 {
		t.Errorf("input cost = %v, want 0.6", facts.InputCostPerMillion)
	}
}

func TestResolveProviderRowBeatsCanonical(t *testing.T) {
	loadFixture(t)
	// The opencode row for glm-5 is text-only and 128k; canonical zhipuai/glm-5.x
	// is 200k reasoning. The provider row must win.
	facts, ok := Lookup("opencode", "glm-5")
	if !ok {
		t.Fatal("expected opencode glm-5 to resolve")
	}
	if facts.SupportsVision {
		t.Error("opencode glm-5 row is text-only")
	}
	if facts.ContextWindow != 128000 {
		t.Errorf("context = %d, want provider row 128000", facts.ContextWindow)
	}
}

func TestResolveFuzzyByBareSlug(t *testing.T) {
	loadFixture(t)
	// No exact "kimi-k3" row; the canonical "moonshotai/kimi-k3" should match via
	// suffix scoring.
	facts, ok := Lookup("", "kimi-k3")
	if !ok {
		t.Fatal("expected fuzzy resolve of kimi-k3")
	}
	if !facts.SupportsVision {
		t.Error("fuzzy-resolved kimi-k3 should accept images")
	}
}

func TestResolveUnknownReturnsNoFacts(t *testing.T) {
	loadFixture(t)
	if _, ok := Lookup("", "totally-made-up-model-zzz"); ok {
		t.Error("unknown model should not resolve")
	}
}

func TestReasoningEffortsFromOptions(t *testing.T) {
	loadFixture(t)
	facts, ok := Lookup("", "zhipuai/glm-5.x")
	if !ok {
		t.Fatal("expected glm-5.x to resolve")
	}
	if !facts.SupportsReasoning {
		t.Error("glm-5.x should support reasoning")
	}
	for _, tier := range []string{"low", "medium", "high"} {
		if !facts.HasReasoningEffort(tier) {
			t.Errorf("missing effort tier %q", tier)
		}
	}
	if facts.HasReasoningEffort("none") {
		t.Error("glm-5.x should not claim a 'none' tier")
	}
}

func TestDeprecatedFlag(t *testing.T) {
	loadFixture(t)
	facts, _ := Lookup("", "openai/gpt-4-turbo")
	if !facts.Deprecated {
		t.Error("gpt-4-turbo should be deprecated")
	}
}

func TestDisabledStoreReturnsNoFacts(t *testing.T) {
	resetForTest()
	t.Cleanup(resetForTest)
	// Enable() intentionally not called.
	if Enabled() {
		t.Fatal("store should start disabled")
	}
	if _, ok := Lookup("", "moonshotai/kimi-k3"); ok {
		t.Error("disabled store must not resolve")
	}
}

func TestKillSwitchDisablesStore(t *testing.T) {
	loadFixture(t)
	t.Setenv("KAJICODE_DISABLE_MODELS_FETCH", "1")
	if _, ok := Lookup("", "moonshotai/kimi-k3"); ok {
		t.Error("kill switch must disable resolution")
	}
}

func TestReadFreshCacheHonorsTTL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")
	if err := os.WriteFile(path, []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readFreshCache(path, time.Hour); err != nil {
		t.Fatalf("fresh cache should read: %v", err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := readFreshCache(path, time.Hour); err == nil {
		t.Fatal("expected stale cache to be rejected")
	}
}

func TestNormalizeKeyStripsQualifier(t *testing.T) {
	cases := map[string]string{
		"openai/gpt-4.1":    "openai/gpt-4.1",
		"OpenAI:gpt-4.1":    "gpt-4.1",
		"  GPT-4.1  ":       "gpt-4.1",
		"provider:model/id": "model/id",
	}
	for input, want := range cases {
		if got := normalizeKey(input); got != want {
			t.Errorf("normalizeKey(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestFuzzyScoreThreshold(t *testing.T) {
	relevant := indexed{key: "moonshotai/kimi-k3", tokens: tokenize("moonshotai/kimi-k3")}
	if score := scoreMatch("kimi-k3", relevant); score < minFuzzyScore {
		t.Errorf("suffix match scored %v, want >= %v", score, minFuzzyScore)
	}
	unrelated := indexed{key: "openai/gpt-4-turbo", tokens: tokenize("openai/gpt-4-turbo")}
	if score := scoreMatch("kimi-k3", unrelated); score >= minFuzzyScore {
		t.Errorf("unrelated models scored %v, want < %v", score, minFuzzyScore)
	}
}
