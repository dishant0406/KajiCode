package specialist

import "testing"

func TestBuiltinsExposeHarnessRoles(t *testing.T) {
	byName := map[string]Manifest{}
	for _, manifest := range Builtins() {
		byName[manifest.Metadata.Name] = manifest
	}

	for _, name := range []string{"worker", "planner", "explorer", "verifier", "code-review"} {
		manifest, ok := byName[name]
		if !ok {
			t.Fatalf("missing built-in specialist %q", name)
		}
		if manifest.Metadata.Description == "" {
			t.Fatalf("built-in specialist %q must describe when to use it", name)
		}
		if manifest.SystemPrompt == "" {
			t.Fatalf("built-in specialist %q must have a system prompt", name)
		}
	}
}

func TestReadOnlySpecialistCategoryIncludesNavigationTools(t *testing.T) {
	resolved, err := ResolveTools([]string{"read-only"})
	if err != nil {
		t.Fatalf("ResolveTools: %v", err)
	}
	for _, want := range []string{"lsp_navigate", "skill"} {
		if !contains(resolved, want) {
			t.Fatalf("read-only category missing %q: %#v", want, resolved)
		}
	}
	if !manifestIsReadOnly(Manifest{ResolvedTools: resolved}) {
		t.Fatalf("read-only category should remain auto-approvable: %#v", resolved)
	}
}
