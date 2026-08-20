package modelregistry

import "testing"

func TestDefaultRoleCatalogIntegrity(t *testing.T) {
	ids := DefaultRoleIDs()
	if len(ids) < 5 {
		t.Fatalf("expected at least 5 default roles, got %d", len(ids))
	}
	// No duplicate ids.
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("duplicate default role id %q", id)
		}
		seen[id] = true
	}
	// RoleInfoByID round-trips every catalog id.
	for _, id := range ids {
		info, ok := RoleInfoByID(id)
		if !ok {
			t.Fatalf("RoleInfoByID(%q) reported not-found for a catalog id", id)
		}
		if info.ID == "" || info.DisplayName == "" || info.Tag == "" {
			t.Fatalf("role %q missing identity fields: %+v", id, info)
		}
	}
}

func TestDefaultRoleCatalogCanonicalIDs(t *testing.T) {
	for _, want := range []string{RoleDefault, RolePlan, RoleDesign, RoleImplement, RoleVision, RoleReview, RoleFast} {
		if _, ok := RoleInfoByID(want); !ok {
			t.Fatalf("expected canonical default role %q to exist", want)
		}
	}
}

func TestDefaultRoleCapabilities(t *testing.T) {
	plan, _ := RoleInfoByID(RolePlan)
	if plan.Capability != ModelCapabilityReasoning {
		t.Fatalf("plan role should target reasoning, got %q", plan.Capability)
	}
	vision, _ := RoleInfoByID(RoleVision)
	if vision.Capability != ModelCapabilityVision {
		t.Fatalf("vision role should target vision, got %q", vision.Capability)
	}
	def, _ := RoleInfoByID(RoleDefault)
	if def.Capability != "" {
		t.Fatalf("default role should have no capability hint, got %q", def.Capability)
	}
}

func TestRoleInfoByIDUnknownAndCaseInsensitive(t *testing.T) {
	if _, ok := RoleInfoByID("definitely-not-a-role"); ok {
		t.Fatal("expected unknown role to be not-found")
	}
	info, ok := RoleInfoByID("  PLAN ")
	if !ok || info.ID != RolePlan {
		t.Fatalf("expected case/space-insensitive lookup, got %+v ok=%v", info, ok)
	}
}

func TestDefaultRoleSuggestionsAreDisplayOnly(t *testing.T) {
	registry, err := DefaultRegistry()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	// Every non-empty default selector must resolve to a real model so the
	// suggestion shown in the /role picker is never a dead link.
	for _, id := range DefaultRoleIDs() {
		info, _ := RoleInfoByID(id)
		if info.DefaultSelector == "" {
			continue
		}
		if _, _, ok := registry.ResolveWithFallback(info.DefaultSelector); !ok {
			t.Fatalf("default role %q suggestion %q does not resolve", id, info.DefaultSelector)
		}
	}
}
