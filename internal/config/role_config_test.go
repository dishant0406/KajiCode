package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImagesConfigEffectiveVisionRouting(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "off"},
		{"auto", "auto"},
		{"model", "model"},
		{"off", "off"},
		{"  model  ", "model"},
		{"AUTO", "off"}, // case-sensitive by contract
		{"bogus", "off"},
	}
	for _, c := range cases {
		cfg := ImagesConfig{VisionRouting: c.in}
		if got := cfg.EffectiveVisionRouting(); got != c.want {
			t.Fatalf("EffectiveVisionRouting(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestImagesConfigEmpty(t *testing.T) {
	if !(ImagesConfig{}).Empty() {
		t.Fatal("zero ImagesConfig should be empty")
	}
	if (ImagesConfig{VisionRouting: "auto"}).Empty() {
		t.Fatal("ImagesConfig with visionRouting should not be empty")
	}
}

func TestFileConfigRoundTripsModelRoles(t *testing.T) {
	src := FileConfig{
		ActiveProvider: "openai",
		Providers: []ProviderProfile{
			{Name: "openai", ProviderKind: "openai", Model: "gpt-4.1"},
		},
		ModelRoles: map[string]string{
			"plan":   "@implement",
			"vision": "google:gemini-2.5-pro",
		},
		DefaultModel: "gpt-4.1",
		Images:       ImagesConfig{VisionRouting: "auto"},
	}
	data, err := src.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var dst FileConfig
	if err := dst.UnmarshalJSON(data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if dst.ModelRoles["plan"] != "@implement" {
		t.Fatalf("plan role not round-tripped: %+v", dst.ModelRoles)
	}
	if dst.ModelRoles["vision"] != "google:gemini-2.5-pro" {
		t.Fatalf("vision role not round-tripped: %+v", dst.ModelRoles)
	}
	if dst.DefaultModel != "gpt-4.1" {
		t.Fatalf("defaultModel not round-tripped: %q", dst.DefaultModel)
	}
	if dst.Images.EffectiveVisionRouting() != "auto" {
		t.Fatalf("visionRouting not round-tripped: %q", dst.Images.VisionRouting)
	}
}

func TestResolveThreadsModelRolesAndImages(t *testing.T) {
	// Clear the ambient provider env so the fixture's ActiveProvider wins, and so the
	// test does not depend on the developer's shell.
	t.Setenv(ActiveProviderEnv, "")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	body, err := json.Marshal(FileConfig{
		ActiveProvider: "openai",
		Providers: []ProviderProfile{
			{Name: "openai", ProviderKind: "openai", Model: "gpt-4.1"},
		},
		ModelRoles:   map[string]string{"implement": "gpt-4.1-mini"},
		DefaultModel: "gpt-4.1",
		Images:       ImagesConfig{VisionRouting: "model"},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	resolved, err := Resolve(ResolveOptions{UserConfigPath: path})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.ModelRoles["implement"] != "gpt-4.1-mini" {
		t.Fatalf("implement role not threaded through Resolve: %+v", resolved.ModelRoles)
	}
	if resolved.DefaultModel != "gpt-4.1" {
		t.Fatalf("defaultModel not threaded through Resolve: %q", resolved.DefaultModel)
	}
	if resolved.Images.EffectiveVisionRouting() != "model" {
		t.Fatalf("visionRouting not threaded through Resolve: %q", resolved.Images.VisionRouting)
	}
}

func TestMergeRoleOverrides(t *testing.T) {
	cfg := FileConfig{
		ModelRoles:   map[string]string{"plan": "gpt-4.1"},
		DefaultModel: "gpt-4.1",
		Images:       ImagesConfig{VisionRouting: "off"},
	}
	overrides := Overrides{
		ModelRoles:   map[string]string{"implement": "gpt-4.1-mini"},
		DefaultModel: "sonnet",
		Images:       ImagesConfig{VisionRouting: "auto"},
	}
	// Blanket applyOverrides mutates a copy; use the targeted merge directly.
	mergeRoleOverrides(&cfg, overrides)
	if cfg.ModelRoles["plan"] != "gpt-4.1" {
		t.Fatalf("existing plan role lost: %+v", cfg.ModelRoles)
	}
	if cfg.ModelRoles["implement"] != "gpt-4.1-mini" {
		t.Fatalf("implement role not merged: %+v", cfg.ModelRoles)
	}
	if cfg.DefaultModel != "sonnet" {
		t.Fatalf("defaultModel not overridden: %q", cfg.DefaultModel)
	}
	if cfg.Images.EffectiveVisionRouting() != "auto" {
		t.Fatalf("visionRouting not overridden: %q", cfg.Images.VisionRouting)
	}
}

func TestResolvedConfigCarriesAllProfilesForRouter(t *testing.T) {
	// The router needs ALL resolved profiles (not just the active one) so it can build
	// a provider for a role pointing at a non-active profile. Resolve must thread the
	// full normalized slice through ResolvedConfig.Providers with each profile's Model
	// intact.
	t.Setenv(ActiveProviderEnv, "")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	body, err := json.Marshal(FileConfig{
		ActiveProvider: "openai",
		Providers: []ProviderProfile{
			{Name: "openai", ProviderKind: "openai", Model: "gpt-4.1"},
			{Name: "anthropic", ProviderKind: "anthropic", Model: "claude-sonnet-4-5"},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	resolved, err := Resolve(ResolveOptions{UserConfigPath: path, Env: map[string]string{}})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.ActiveProvider != "openai" {
		t.Fatalf("unexpected active provider %q", resolved.ActiveProvider)
	}
	if len(resolved.Providers) != 2 {
		t.Fatalf("expected 2 providers for router, got %d", len(resolved.Providers))
	}
	found := false
	for _, p := range resolved.Providers {
		if p.Name == "anthropic" && p.Model == "claude-sonnet-4-5" {
			found = true
		}
	}
	if !found {
		t.Fatal("non-active provider claude-sonnet-4-5 not available to the router")
	}
}

func TestValidateRoleConfig(t *testing.T) {
	cfg := FileConfig{
		ModelRoles: map[string]string{
			"ok":    "gpt-4.1",                  // resolves
			"empty": "",                         // empty -> issue
			"alias": "@other",                   // alias -> no issue
			"junk":  "::does-not-exist-thing::", // unresolvable -> issue
		},
		DefaultModel: "gpt-4.1",
		Images:       ImagesConfig{VisionRouting: "bogus"},
	}
	issues := validateRoleConfig(cfg)
	var messages []string
	for _, i := range issues {
		messages = append(messages, i.FieldPath)
	}
	joined := strings.Join(messages, ",")
	if !strings.Contains(joined, "modelRoles.empty") {
		t.Fatalf("expected empty-role issue, got %v", messages)
	}
	if !strings.Contains(joined, "modelRoles.junk") {
		t.Fatalf("expected unresolvable-role issue, got %v", messages)
	}
	if strings.Contains(joined, "modelRoles.ok") {
		t.Fatalf("ok role should not warn: %v", messages)
	}
	if strings.Contains(joined, "modelRoles.alias") {
		t.Fatalf("alias role should not warn: %v", messages)
	}
	if !strings.Contains(joined, "images.visionRouting") {
		t.Fatalf("expected visionRouting issue, got %v", messages)
	}
}
