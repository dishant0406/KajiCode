package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dishant0406/KajiCode/internal/config"
)

// Regression for issue #555's follow-up: `kajicode providers check` must not
// error that a no-auth custom endpoint requires an API key, matching what
// /model and /providers already treat as usable.
func TestValidateProviderRuntimeReadyCustomEndpoint(t *testing.T) {
	cases := []struct {
		name    string
		profile config.ProviderProfile
		wantErr bool
	}{
		{
			name: "custom openai compatible with no credential configured",
			profile: config.ProviderProfile{
				Name:      "local-llama",
				CatalogID: "custom-openai-compatible",
				BaseURL:   "http://192.168.1.50:8080/v1",
				Model:     "custom-model",
			},
			wantErr: false,
		},
		{
			name: "custom openai compatible with stale legacy default env",
			profile: config.ProviderProfile{
				Name:      "local-llama",
				CatalogID: "custom-openai-compatible",
				BaseURL:   "http://192.168.1.50:8080/v1",
				APIKeyEnv: "OPENAI_API_KEY",
				Model:     "custom-model",
			},
			wantErr: false,
		},
		{
			name: "custom openai compatible with explicit non-default env still requires it",
			profile: config.ProviderProfile{
				Name:      "local-llama",
				CatalogID: "custom-openai-compatible",
				BaseURL:   "http://192.168.1.50:8080/v1",
				APIKeyEnv: "LLAMA_CPP_API_KEY",
				Model:     "custom-model",
			},
			wantErr: true,
		},
		{
			name: "catalog provider missing key still errors",
			profile: config.ProviderProfile{
				Name:      "groq",
				CatalogID: "groq",
				Model:     "llama-3.3-70b-versatile",
			},
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateProviderRuntimeReady(c.profile)
			if (err != nil) != c.wantErr {
				t.Fatalf("validateProviderRuntimeReady() error = %v, wantErr %v", err, c.wantErr)
			}
		})
	}
}

// --api-key-stdin reads the secret from stdin and moves it into the encrypted
// credential store: config.json gains the apiKeyStored marker and never the key.
func TestRunProvidersAddKeyFromStdin(t *testing.T) {
	t.Setenv("KAJICODE_CRED_STORAGE", "encrypted-file")
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	var stdout, stderr bytes.Buffer
	deps := providerSetupDeps(configPath)
	deps.stdin = strings.NewReader("sk-secret-value\n")
	exitCode := runWithDeps([]string{"providers", "add", "openai", "--api-key-stdin", "--json"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("exit = %d, stderr = %s", exitCode, stderr.String())
	}
	if strings.Contains(stdout.String(), "sk-secret-value") {
		t.Fatalf("stdout leaked the API key: %s", stdout.String())
	}
	cfg := readFileConfig(t, configPath)
	if len(cfg.Providers) != 1 {
		t.Fatalf("providers = %d, want 1", len(cfg.Providers))
	}
	if !cfg.Providers[0].APIKeyStored {
		t.Fatalf("apiKeyStored = false, want true")
	}
	if cfg.Providers[0].APIKey != "" {
		t.Fatalf("inline apiKey persisted, want empty")
	}
	store, err := config.ProviderKeyStoreAt(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	key, ok, err := store.Get(cfg.Providers[0].Name)
	if err != nil || !ok {
		t.Fatalf("stored key = %q ok=%v err=%v", key, ok, err)
	}
	if key != "sk-secret-value" {
		t.Fatalf("stored key = %q, want the stdin value", key)
	}
}

// An empty stdin is rejected rather than silently storing an empty credential.
func TestRunProvidersAddKeyFromStdinRejectsEmpty(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	var stdout, stderr bytes.Buffer
	deps := providerSetupDeps(configPath)
	deps.stdin = strings.NewReader("   \n")
	exitCode := runWithDeps([]string{"providers", "add", "openai", "--api-key-stdin"}, &stdout, &stderr, deps)
	if exitCode == exitSuccess {
		t.Fatalf("expected failure for an empty stdin key, got success: %s", stdout.String())
	}
}
