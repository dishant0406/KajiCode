package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRecipe(t *testing.T) {
	tools := map[string]bool{"bash": true, "read_file": true}
	good := Recipe{
		Name:     "check dirty",
		Commands: []RecipeCommand{{ID: "run", Tool: "bash", Args: map[string]any{"command": "git status"}}},
	}
	if err := ValidateRecipe(good, tools); err != nil {
		t.Fatalf("good recipe rejected: %v", err)
	}

	cases := []struct {
		name string
		r    Recipe
	}{
		{"empty name", Recipe{Commands: good.Commands}},
		{"no commands", Recipe{Name: "x"}},
		{"empty tool", Recipe{Name: "x", Commands: []RecipeCommand{{ID: "a", Tool: " "}}}},
		{"unknown tool", Recipe{Name: "x", Commands: []RecipeCommand{{ID: "a", Tool: "nope"}}}},
	}
	for _, c := range cases {
		if err := ValidateRecipe(c.r, tools); err == nil {
			t.Fatalf("%s: expected error", c.name)
		}
	}
	// nil toolNames skips registry-consistency validation.
	if err := ValidateRecipe(Recipe{Name: "x", Commands: []RecipeCommand{{ID: "a", Tool: "anything"}}}, nil); err != nil {
		t.Fatalf("nil toolNames should skip tool check: %v", err)
	}
}

func TestSaveAndListRecipes(t *testing.T) {
	root := filepath.Join(t.TempDir(), "learning")
	recipe := Recipe{
		Name:        "check_git_dirty",
		Description: "Check working tree",
		Commands:    []RecipeCommand{{ID: "run", Tool: "bash", Args: map[string]any{"command": "git status --porcelain"}}},
	}
	manifest, err := SaveRecipe(root, recipe, map[string]bool{"bash": true})
	if err != nil {
		t.Fatalf("SaveRecipe: %v", err)
	}
	if !strings.HasSuffix(manifest, filepath.Join(RecipesDir, "check_git_dirty", RecipeFile)) {
		t.Fatalf("manifest path = %q, want under recipes/name/recipe.json", manifest)
	}
	if _, err := os.Stat(manifest); err != nil {
		t.Fatalf("manifest not created: %v", err)
	}

	loaded, problems := ListRecipes(root, map[string]bool{"bash": true})
	if len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	if len(loaded) != 1 || loaded[0].Name != "check_git_dirty" {
		t.Fatalf("loaded = %#v, want one recipe", loaded)
	}
	if len(loaded[0].Commands) != 1 || loaded[0].Commands[0].Tool != "bash" {
		t.Fatalf("recipe commands not round-tripped: %#v", loaded[0])
	}
}

func TestListRecipesSkipsCorruptManifest(t *testing.T) {
	root := filepath.Join(t.TempDir(), "learning")
	badDir := filepath.Join(RecipesPath(root), "broken")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badDir, RecipeFile), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, problems := ListRecipes(root, map[string]bool{"bash": true})
	if len(loaded) != 0 {
		t.Fatalf("loaded = %#v, want none", loaded)
	}
	if len(problems) != 1 {
		t.Fatalf("problems = %#v, want one parse error", problems)
	}
}

func TestSaveRecipesRejectsReservedName(t *testing.T) {
	root := filepath.Join(t.TempDir(), "learning")
	_, err := SaveRecipe(root, Recipe{Name: BasePromptID, Commands: []RecipeCommand{{ID: "a", Tool: "bash"}}}, map[string]bool{"bash": true})
	if err == nil {
		t.Fatal("saving a recipe named base_system_prompt should error")
	}
}

func TestListRecipesMissingDir(t *testing.T) {
	loaded, problems := ListRecipes(filepath.Join(t.TempDir(), "nope"), map[string]bool{"bash": true})
	if len(loaded) != 0 || len(problems) != 0 {
		t.Fatalf("missing dir should return empty, got loaded=%v problems=%v", loaded, problems)
	}
}

func TestLoadRecipeRoundTrip(t *testing.T) {
	root := filepath.Join(t.TempDir(), "learning")
	recipe := Recipe{Name: "r", Commands: []RecipeCommand{{ID: "c", Tool: "grep", Args: map[string]any{"pattern": "x", "path": "."}}}}
	manifest, err := SaveRecipe(root, recipe, map[string]bool{"grep": true})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadRecipe(manifest, map[string]bool{"grep": true})
	if err != nil {
		t.Fatalf("LoadRecipe: %v", err)
	}
	if loaded.Commands[0].Args["pattern"] != "x" {
		t.Fatalf("args not round-tripped: %#v", loaded.Commands[0].Args)
	}
}
