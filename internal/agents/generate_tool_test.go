package agents

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dishant0406/KajiCode/internal/tools"
)

func TestGenerateAgentCreateAndOverwrite(t *testing.T) {
	dir := t.TempDir()
	tool := NewGenerateTool(dir)
	args := map[string]any{
		"name":          "api-review",
		"description":   "Reviews API changes",
		"system_prompt": "Review API changes.",
		"tools":         []any{"read"},
	}
	result := tool.Run(context.Background(), args)
	if result.Status != tools.StatusOK {
		t.Fatalf("create: %s", result.Output)
	}
	path := filepath.Join(dir, "api-review.md")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("agent file not written: %v", err)
	}

	// A second create without overwrite is refused.
	again := tool.Run(context.Background(), args)
	if again.Status != tools.StatusError {
		t.Fatalf("expected refusal, got %s", again.Output)
	}

	// With overwrite it succeeds and round-trips through the loader.
	args["overwrite"] = true
	args["description"] = "Updated description"
	if overwrite := tool.Run(context.Background(), args); overwrite.Status != tools.StatusOK {
		t.Fatalf("overwrite: %s", overwrite.Output)
	}
	result2, err := Load(Paths{UserDir: dir})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	agent, ok := Find(result2, "api-review")
	if !ok || agent.Description != "Updated description" {
		t.Fatalf("overwritten agent not loaded: %#v", agent)
	}
}

func TestGenerateAgentRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "target.md")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(dir, "linked.md")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	tool := NewGenerateTool(dir)
	result := tool.Run(context.Background(), map[string]any{
		"name":          "linked",
		"description":   "x",
		"system_prompt": "x",
		"overwrite":     true,
	})
	if result.Status != tools.StatusError || !strings.Contains(result.Output, "symlink") {
		t.Fatalf("expected symlink refusal, got %s", result.Output)
	}
}
