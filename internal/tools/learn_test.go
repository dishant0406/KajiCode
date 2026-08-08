package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dishant0406/KajiCode/internal/harness"
)

// stubTool is a minimal registered tool for recipe_run dispatch tests.
type stubTool struct {
	baseTool
	name   string
	output string
}

func newStubTool(name, output string) *stubTool {
	return &stubTool{
		name:   name,
		output: output,
		baseTool: baseTool{
			safety: readOnlySafety("stub for tests"),
		},
	}
}

func (tool *stubTool) Name() string { return tool.name }

func (tool *stubTool) Run(_ context.Context, _ map[string]any) Result {
	return okResult(tool.output)
}

func TestRecipeRunToolDispatchesCommands(t *testing.T) {
	registry := NewRegistry()
	registry.Register(newStubTool("echo", "hello"))

	root := filepath.Join(t.TempDir(), "learning")
	recipe := harness.Recipe{
		Name:        "greet",
		Description: "Say hello",
		Commands:    []harness.RecipeCommand{{ID: "run", Tool: "echo", Args: map[string]any{}}},
	}
	if _, err := harness.SaveRecipe(root, recipe, map[string]bool{"echo": true}); err != nil {
		t.Fatalf("SaveRecipe: %v", err)
	}

	runTool := NewRecipeRunTool(registry, root)
	result := runTool.Run(context.Background(), map[string]any{"name": "greet"})
	if result.Status != StatusOK {
		t.Fatalf("recipe_run = %#v, want ok", result)
	}
	if !strings.Contains(result.Output, "hello") {
		t.Fatalf("output = %q, want hello", result.Output)
	}
	if !strings.Contains(result.Output, "run:echo") {
		t.Fatalf("output should mark the command id, got %q", result.Output)
	}
}

func TestRecipeRunToolUnknownRecipe(t *testing.T) {
	registry := NewRegistry()
	registry.Register(newStubTool("echo", "x"))
	runTool := NewRecipeRunTool(registry, filepath.Join(t.TempDir(), "learning"))

	result := runTool.Run(context.Background(), map[string]any{"name": "missing"})
	if result.Status != StatusError || !strings.Contains(result.Output, "unknown recipe") {
		t.Fatalf("result = %#v, want unknown-recipe error", result)
	}
}

func TestRecipeRunToolMissingNameArg(t *testing.T) {
	registry := NewRegistry()
	runTool := NewRecipeRunTool(registry, t.TempDir())
	result := runTool.Run(context.Background(), map[string]any{})
	if result.Status != StatusError || !strings.Contains(result.Output, "name is required") {
		t.Fatalf("result = %#v, want name-required error", result)
	}
}

func testLearnTool(t *testing.T) (Tool, string) {
	root := filepath.Join(t.TempDir(), "learning")
	return NewLearnTool(root), root
}

func TestLearnToolCreateAndStatus(t *testing.T) {
	learn, _ := testLearnTool(t)
	result := learn.Run(context.Background(), map[string]any{
		"action":  "create",
		"kind":    "memory",
		"title":   "C build",
		"content": "Use cmake --build .",
	})
	if result.Status != StatusOK {
		t.Fatalf("create = %#v", result)
	}

	status := learn.Run(context.Background(), map[string]any{"action": "status"})
	if status.Status != StatusOK {
		t.Fatalf("status = %#v", status)
	}
	out := status.Output
	if !strings.Contains(out, "c_build") || !strings.Contains(out, "cmake") {
		t.Fatalf("status output missing entry: %q", out)
	}
}

func TestLearnToolCreateRecipe(t *testing.T) {
	learn, _ := testLearnTool(t)
	result := learn.Run(context.Background(), map[string]any{
		"action": "create",
		"kind":   "recipe",
		"id":     "build",
		"title":  "Build project",
		"recipe": `{"name":"build","commands":[{"id":"build","tool":"bash","args":{"command":"make build"}}]}`,
	})
	if result.Status != StatusOK {
		t.Fatalf("create recipe = %#v", result)
	}

	status := learn.Run(context.Background(), map[string]any{"action": "status"})
	if !strings.Contains(status.Output, "build") || !strings.Contains(status.Output, "recipe") {
		t.Fatalf("status missing recipe: %q", status.Output)
	}
}

func TestLearnToolCreateRejectsDuplicate(t *testing.T) {
	learn, _ := testLearnTool(t)
	args := map[string]any{"action": "create", "kind": "memory", "id": "fact", "title": "F", "content": "x"}
	if res := learn.Run(context.Background(), args); res.Status != StatusOK {
		t.Fatalf("first create = %#v", res)
	}
	if res := learn.Run(context.Background(), args); res.Status != StatusError {
		t.Fatalf("duplicate create should error, got %#v", res)
	}
}

func TestLearnToolDeleteAndUpdate(t *testing.T) {
	learn, _ := testLearnTool(t)
	create := map[string]any{"action": "create", "kind": "memory", "id": "fact", "title": "F", "content": "v1"}
	if res := learn.Run(context.Background(), create); res.Status != StatusOK {
		t.Fatalf("create = %#v", res)
	}
	update := map[string]any{"action": "update", "kind": "memory", "id": "fact", "content": "v2"}
	res := learn.Run(context.Background(), update)
	if res.Status != StatusOK {
		t.Fatalf("update = %#v", res)
	}
	dels := learn.Run(context.Background(), map[string]any{"action": "delete", "kind": "memory", "id": "fact"})
	if dels.Status != StatusOK {
		t.Fatalf("delete = %#v", dels)
	}
	status := learn.Run(context.Background(), map[string]any{"action": "status"})
	if strings.Contains(status.Output, "fact") {
		t.Fatalf("deleted entry still present: %q", status.Output)
	}
}

func TestLearnToolScheduleSetsMetaFlag(t *testing.T) {
	learn, _ := testLearnTool(t)
	result := learn.Run(context.Background(), map[string]any{"action": "run"})
	if result.Status != StatusOK {
		t.Fatalf("schedule = %#v", result)
	}
	if result.Meta[LearnRequestMeta] != "true" {
		t.Fatalf("schedule should set Meta[%s], got %#v", LearnRequestMeta, result.Meta)
	}
}

func TestLearnToolBadActions(t *testing.T) {
	learn, _ := testLearnTool(t)
	for _, action := range []string{"bogus", "reset -i", ""} {
		if res := learn.Run(context.Background(), map[string]any{"action": action}); res.Status != StatusError {
			t.Fatalf("action %q should error, got %#v", action, res)
		}
	}
}

func TestLearnToolImmutableBasePrompt(t *testing.T) {
	learn, _ := testLearnTool(t)
	res := learn.Run(context.Background(), map[string]any{"action": "create", "kind": "prompt", "id": harness.BasePromptID, "title": "x"})
	if res.Status != StatusError {
		t.Fatalf("creating base prompt id should error, got %#v", res)
	}
}

func TestLearnToolBadRecipeJSON(t *testing.T) {
	learn, _ := testLearnTool(t)
	res := learn.Run(context.Background(), map[string]any{
		"action": "create",
		"kind":   "recipe",
		"id":     "r",
		"title":  "R",
		"recipe": "{not json",
	})
	if res.Status != StatusError {
		t.Fatalf("bad recipe JSON should error, got %#v", res)
	}
}
