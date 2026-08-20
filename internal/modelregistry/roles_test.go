package modelregistry

import (
	"strings"
	"testing"
)

func TestExpandRoleSelectorBare(t *testing.T) {
	roles := map[string]string{
		"design": "gpt-4.1",
	}
	ref, err := ExpandRoleSelector("gpt-4.1", roles, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref.Selector != "gpt-4.1" {
		t.Fatalf("expected gpt-4.1, got %q", ref.Selector)
	}
	if ref.ProviderHint != "" {
		t.Fatalf("expected empty provider hint, got %q", ref.ProviderHint)
	}
	if ref.FromAlias {
		t.Fatal("expected FromAlias false for a bare selector")
	}
}

func TestExpandRoleSelectorProviderPrefixed(t *testing.T) {
	ref, err := ExpandRoleSelector("openai:gpt-4.1", map[string]string{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref.ProviderHint != "openai" {
		t.Fatalf("expected provider hint openai, got %q", ref.ProviderHint)
	}
	if ref.Selector != "gpt-4.1" {
		t.Fatalf("expected model gpt-4.1, got %q", ref.Selector)
	}
}

func TestExpandRoleSelectorEmpty(t *testing.T) {
	ref, err := ExpandRoleSelector("", map[string]string{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref.Selector != "" {
		t.Fatalf("expected empty selector, got %q", ref.Selector)
	}
}

func TestExpandRoleSelectorAlias(t *testing.T) {
	roles := map[string]string{
		"design":  "@planner",
		"planner": "openai:gpt-4.1",
	}
	ref, err := ExpandRoleSelector("@design", roles, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref.Selector != "gpt-4.1" {
		t.Fatalf("expected expanded gpt-4.1, got %q", ref.Selector)
	}
	if ref.ProviderHint != "openai" {
		t.Fatalf("expected provider hint openai, got %q", ref.ProviderHint)
	}
	if !ref.FromAlias {
		t.Fatal("expected FromAlias true for an alias chain")
	}
}

func TestExpandRoleSelectorCycle(t *testing.T) {
	roles := map[string]string{
		"a": "@b",
		"b": "@a",
	}
	_, err := ExpandRoleSelector("@a", roles, nil)
	if err == nil {
		t.Fatal("expected a cycle error")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle in error, got %v", err)
	}
}

func TestExpandRoleSelectorUnknownRole(t *testing.T) {
	_, err := ExpandRoleSelector("@missing", map[string]string{}, nil)
	if err == nil {
		t.Fatal("expected an unknown-role error")
	}
	if !strings.Contains(err.Error(), "unknown model role") {
		t.Fatalf("expected unknown role in error, got %v", err)
	}
}

func TestResolveRoleProfile(t *testing.T) {
	registry, err := NewRegistry(DefaultModelEntries())
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	roles := map[string]string{
		"implement": "gpt-4.1",
	}
	entry, hasHint, ok := registry.ResolveRole("gpt-4.1", roles)
	if !ok {
		t.Fatal("expected resolve success")
	}
	if hasHint {
		t.Fatal("expected no provider hint")
	}
	if entry.ID != "gpt-4.1" {
		t.Fatalf("expected gpt-4.1, got %q", entry.ID)
	}
}

func TestResolveRoleFromAlias(t *testing.T) {
	registry, err := NewRegistry(DefaultModelEntries())
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	roles := map[string]string{
		"design":  "@planner",
		"planner": "openai:gpt-4.1",
	}
	entry, hasHint, ok := registry.ResolveRole("@design", roles)
	if !ok {
		t.Fatal("expected resolve success")
	}
	if !hasHint {
		t.Fatal("expected provider hint present after alias expansion")
	}
	if entry.Provider != ProviderOpenAI {
		t.Fatalf("expected openai provider, got %q", entry.Provider)
	}
}

func TestResolveRoleDeprecatedFallback(t *testing.T) {
	registry, err := NewRegistry(DefaultModelEntries())
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	// gpt-4-turbo is deprecated with fallback to gpt-4.1.
	entry, _, ok := registry.ResolveRoleWithFallback("gpt-4-turbo", map[string]string{})
	if !ok {
		t.Fatal("expected fallback resolve success")
	}
	if entry.ID != "gpt-4.1" {
		t.Fatalf("expected fallback gpt-4.1, got %q", entry.ID)
	}
}

func TestMatchRoleProvider(t *testing.T) {
	registry, err := NewRegistry(DefaultModelEntries())
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	entry, ok := registry.Resolve("gpt-4.1")
	if !ok {
		t.Fatal("resolve failed")
	}
	if !entry.MatchRoleProvider("openai", strings.ToLower) {
		t.Fatal("expected hint openai to match")
	}
	if entry.MatchRoleProvider("anthropic", strings.ToLower) {
		t.Fatal("expected hint anthropic to NOT match")
	}
	if !entry.MatchRoleProvider("", strings.ToLower) {
		t.Fatal("empty hint should always match")
	}
}
