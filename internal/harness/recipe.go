package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dishant0406/KajiCode/internal/fsutil"
)

// RecipeFile is the manifest file name inside a recipe subdirectory.
const RecipeFile = "recipe.json"

// ValidateRecipe checks the command contract of a Recipe: a name, at least one
// command, and each command referencing a known registered tool by the same
// name the registry dispatches with. toolNames may be nil to skip the
// registry-consistency check (e.g. offline construction before tools load).
func ValidateRecipe(recipe Recipe, toolNames map[string]bool) error {
	if strings.TrimSpace(recipe.Name) == "" {
		return errors.New("recipe name is required")
	}
	if len(recipe.Commands) == 0 {
		return errors.New("recipe must have at least one command")
	}
	for index, command := range recipe.Commands {
		if strings.TrimSpace(command.Tool) == "" {
			return fmt.Errorf("command %d has no tool", index)
		}
		if toolNames != nil && !toolNames[command.Tool] {
			return fmt.Errorf("command %d references unknown tool %q", index, command.Tool)
		}
	}
	return nil
}

// SaveRecipe writes a recipe manifest atomically into a dedicated subdirectory
// under <learningRoot>/recipes/<name>/recipe.json, keeping learned recipes out
// of internal/skills. It refuses to overwrite the reserved base prompt id and
// validates the command contract first.
func SaveRecipe(learningRoot string, recipe Recipe, toolNames map[string]bool) (string, error) {
	if err := ValidateRecipe(recipe, toolNames); err != nil {
		return "", err
	}
	if recipe.Name == BasePromptID {
		return "", fmt.Errorf("recipe name %q is reserved", BasePromptID)
	}
	dir := filepath.Join(learningRoot, RecipesDir, recipe.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create recipe dir: %w", err)
	}
	data, err := json.MarshalIndent(recipe, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode recipe: %w", err)
	}
	manifest := filepath.Join(dir, RecipeFile)
	if err := writeFileAtomic(manifest, data); err != nil {
		return "", fmt.Errorf("write recipe manifest: %w", err)
	}
	return manifest, nil
}

// LoadRecipe reads and validates a single recipe manifest. toolNames may be nil
// to skip registry-consistency validation.
func LoadRecipe(manifest string, toolNames map[string]bool) (Recipe, error) {
	data, err := os.ReadFile(manifest)
	if err != nil {
		return Recipe{}, err
	}
	var recipe Recipe
	if err := json.Unmarshal(data, &recipe); err != nil {
		return Recipe{}, fmt.Errorf("parse %s: %w", manifest, err)
	}
	if err := ValidateRecipe(recipe, toolNames); err != nil {
		return Recipe{}, fmt.Errorf("validate %s: %w", manifest, err)
	}
	return recipe, nil
}

// RecipesPath returns the recipes subdirectory of a learning root.
func RecipesPath(learningRoot string) string {
	return filepath.Join(learningRoot, RecipesDir)
}

// ListRecipes scans the recipes directory for all valid manifests, sorted by
// recipe name. Problems (unreadable/unparseable/invalid manifests) are returned
// separately so a corrupt recipe is reported without stranding valid ones.
func ListRecipes(learningRoot string, toolNames map[string]bool) ([]Recipe, []string) {
	root := RecipesPath(learningRoot)
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, []string{fmt.Sprintf("scan recipes dir: %v", err)}
	}
	var recipes []Recipe
	var problems []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		manifest := filepath.Join(root, entry.Name(), RecipeFile)
		recipe, err := LoadRecipe(manifest, toolNames)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				problems = append(problems, err.Error())
			}
			continue
		}
		recipes = append(recipes, recipe)
	}
	sort.Slice(recipes, func(i, j int) bool { return recipes[i].Name < recipes[j].Name })
	return recipes, problems
}

// writeFileAtomic writes data to path via temp + fsync + rename-with-retry so a
// reader never observes a partial file. It mirrors Store.saveLocked but targets
// an arbitrary file path rather than a harness state.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	cleanup := func() { _ = os.Remove(tempName) }
	if _, err := temp.Write(data); err != nil {
		cleanup()
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		cleanup()
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := fsutil.RenameWithRetry(tempName, path, nil); err != nil {
		cleanup()
		return err
	}
	return nil
}
