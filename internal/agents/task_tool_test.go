package agents

import (
	"context"
	"strings"
	"testing"

	"github.com/dishant0406/KajiCode/internal/tools"
)

func fakeRunner() *Runner {
	runner := NewRunner(Paths{}, DefaultMaxDepth)
	runner.Load = func(Paths) (LoadResult, error) {
		return LoadResult{Agents: Builtins()}, nil
	}
	return runner
}

func TestTaskToolRunsChildInProcess(t *testing.T) {
	registry := tools.NewRegistry()
	runner := fakeRunner()
	supervisor := NewSupervisor(runner, ChildRunContext{Registry: registry})
	supervisor.RunFunc = func(_ context.Context, request ChildRequest) (ChildResult, error) {
		return ChildResult{AgentName: request.Agent.Name, SessionID: "child_1", Text: "found 3 files"}, nil
	}
	tool := NewTaskTool(supervisor)

	result := tool.Run(context.Background(), map[string]any{
		"agent":  "explorer",
		"prompt": "find the config loader",
	})
	if result.Status != tools.StatusOK {
		t.Fatalf("status = %q, output=%s", result.Status, result.Output)
	}
	if !strings.Contains(result.Output, "found 3 files") {
		t.Fatalf("output missing child text: %s", result.Output)
	}
	if result.Meta["session_id"] != "child_1" {
		t.Fatalf("session_id meta = %q", result.Meta["session_id"])
	}
}

func TestTaskToolUnknownAgentListsOptions(t *testing.T) {
	supervisor := NewSupervisor(fakeRunner(), ChildRunContext{Registry: tools.NewRegistry()})
	tool := NewTaskTool(supervisor)
	result := tool.Run(context.Background(), map[string]any{"agent": "nope", "prompt": "x"})
	if result.Status != tools.StatusError {
		t.Fatalf("expected error, got %q", result.Status)
	}
	if !strings.Contains(result.Output, "explorer") {
		t.Fatalf("error should list available agents: %s", result.Output)
	}
}

func TestDepthCapRejectsNested(t *testing.T) {
	supervisor := NewSupervisor(fakeRunner(), ChildRunContext{Registry: tools.NewRegistry()})
	supervisor.RunFunc = func(_ context.Context, request ChildRequest) (ChildResult, error) {
		return ChildResult{AgentName: request.Agent.Name, Text: "ok"}, nil
	}
	tool := NewTaskTool(supervisor)
	result := tool.RunWithOptions(context.Background(), map[string]any{"agent": "explorer", "prompt": "x"}, tools.RunOptions{Depth: DefaultMaxDepth})
	if result.Status != tools.StatusError {
		t.Fatalf("expected depth error, got %q: %s", result.Status, result.Output)
	}
	if !strings.Contains(result.Output, "nesting depth") {
		t.Fatalf("error should mention nesting depth: %s", result.Output)
	}
}

func TestFilterRegistryKeepsMCPTools(t *testing.T) {
	base := tools.NewRegistry()
	base.Register(stubTool{name: "read_file"})
	base.Register(stubTool{name: "bash"})
	base.Register(stubTool{name: "mcp_github_create_issue"})

	rules := Rules([]string{"read", "mcp_github_*"}, nil)
	filtered := FilterRegistry(base, rules)
	if _, ok := filtered.Get("read_file"); !ok {
		t.Fatalf("read_file should survive")
	}
	if _, ok := filtered.Get("bash"); ok {
		t.Fatalf("bash should be filtered out")
	}
	if _, ok := filtered.Get("mcp_github_create_issue"); !ok {
		t.Fatalf("MCP tool should survive when allowed")
	}
}

func TestFilterRegistryNeverExceedsParent(t *testing.T) {
	base := tools.NewRegistry()
	base.Register(stubTool{name: "read_file"})
	// The agent asks for bash, but the parent registry has no bash: the child
	// still cannot reach it (base is the ceiling).
	filtered := FilterRegistry(base, Rules([]string{"read", "execute"}, nil))
	if _, ok := filtered.Get("bash"); ok {
		t.Fatalf("child must not gain a tool the parent lacked")
	}
}

type stubTool struct{ name string }

func (s stubTool) Name() string             { return s.name }
func (s stubTool) Description() string      { return "stub" }
func (s stubTool) Parameters() tools.Schema { return tools.Schema{Type: "object"} }
func (s stubTool) Safety() tools.Safety {
	return tools.Safety{SideEffect: tools.SideEffectRead, Permission: tools.PermissionAllow}
}
func (s stubTool) Run(context.Context, map[string]any) tools.Result {
	return tools.Result{Status: tools.StatusOK}
}
