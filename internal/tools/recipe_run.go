package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/dishant0406/KajiCode/internal/harness"
)

// recipeRunTool executes a stored recipe by dispatching each command through the
// shared registry to the referenced registered KajiCode tool. It is the
// programmatic execution path for learned Go-native recipes (prime-agent's
// Python-callable replacement): recipes are just data describing already
// sandboxed/permissioned tool calls, so there is no new interpreter and every
// step flows through the existing permission, sandbox, redaction, and
// output-budget machinery.
type recipeRunTool struct {
	baseTool
	registry     *Registry
	learningRoot string
}

// NewRecipeRunTool builds the recipe_run tool. registry must be non-nil and
// already contain the tools recipes may reference; learningRoot is the global
// learning directory holding the recipes/ subdir.
func NewRecipeRunTool(registry *Registry, learningRoot string) Tool {
	return &recipeRunTool{
		registry:     registry,
		learningRoot: learningRoot,
		baseTool: baseTool{
			name: "recipe_run",
			description: "Run a learned Go-native recipe by dispatching its command chain through the registered " +
				"tools. Recipes are reusable procedures (e.g. a repeated build+test flow) saved by the learn tool or " +
				"a review pass. Call this when a matching recipe exists; unknown names list the available recipes.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"name": {Type: "string", Description: "The recipe name to run."},
				},
				Required:             []string{"name"},
				AdditionalProperties: false,
			},
			safety: Safety{
				SideEffect:      SideEffectRead,
				Permission:      PermissionAllow,
				Reason:          "Dispatches stored tool commands; each is gated by the same sandbox and permissions as a direct call.",
				AdvertiseInAuto: true,
			},
			capabilities: ToolCapabilities{Effect: EffectReadOnly, ThreadSafe: false},
		},
	}
}

func (tool *recipeRunTool) Run(ctx context.Context, args map[string]any) Result {
	name, err := stringArg(args, "name", "", true)
	if err != nil {
		return errorResult("Error: Invalid arguments for recipe_run: " + err.Error())
	}
	name = strings.TrimSpace(name)

	recipe, available := recipeForName(tool.registry, tool.learningRoot, name)
	if recipe == nil {
		if available != nil {
			names := make([]string, 0, len(available))
			for _, r := range available {
				names = append(names, r.Name)
			}
			return errorResult(fmt.Sprintf("Error: unknown recipe %q. Available recipes: %s.", name, strings.Join(names, ", ")))
		}
		return errorResult(fmt.Sprintf("Error: unknown recipe %q (no recipes available).", name))
	}

	var out strings.Builder
	for _, command := range recipe.Commands {
		res := tool.registry.RunWithOptions(ctx, command.Tool, command.Args, RunOptions{})
		fmt.Fprintf(&out, "[%s:%s]\n", command.ID, command.Tool)
		if res.Status != StatusOK {
			message := strings.TrimSpace(res.Output)
			fmt.Fprintf(&out, "ERROR (%s): %s\n", res.Status, message)
			return errorResult(strings.TrimSpace(out.String()))
		}
		out.WriteString(res.Output)
		out.WriteString("\n")
	}
	return okResult(strings.TrimSpace(out.String()))
}

// recipeForName loads available recipes and returns the one matching name. It
// validates each candidate against the currently-registered tool set so a recipe
// whose tool vanished after it was learned fails loudly instead of dispatching
// into the void. available is non-nil only when recipe is nil.
func recipeForName(registry *Registry, learningRoot, name string) (*harness.Recipe, []harness.Recipe) {
	if registry == nil {
		return nil, nil
	}
	toolNames := make(map[string]bool)
	for _, t := range registry.All() {
		toolNames[t.Name()] = true
	}
	recipes, _ := harness.ListRecipes(learningRoot, toolNames)
	if len(recipes) == 0 {
		return nil, nil
	}
	for i := range recipes {
		if recipes[i].Name == name {
			return &recipes[i], nil
		}
	}
	return nil, recipes
}
