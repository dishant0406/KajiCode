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

// effectStubTool is a stub whose declared effect drives the read-only gate.
type effectStubTool struct {
	name   string
	effect tools.EffectClass
}

func (s effectStubTool) Name() string             { return s.name }
func (s effectStubTool) Description() string      { return "stub" }
func (s effectStubTool) Parameters() tools.Schema { return tools.Schema{Type: "object"} }
func (s effectStubTool) Safety() tools.Safety {
	return tools.Safety{SideEffect: tools.SideEffectRead, Permission: tools.PermissionAllow}
}
func (s effectStubTool) Capabilities() tools.ToolCapabilities {
	return tools.ToolCapabilities{Effect: s.effect}
}
func (s effectStubTool) Run(context.Context, map[string]any) tools.Result {
	return tools.Result{Status: tools.StatusOK}
}

// parentRegistry registers the read and mutating tools a parent run actually
// exposes, so the gate has to classify the agent's allowed toolset rather than
// the whole registry.
func parentRegistry() *tools.Registry {
	registry := tools.NewRegistry()
	registry.Register(effectStubTool{name: "read_file", effect: tools.EffectReadOnly})
	registry.Register(effectStubTool{name: "write_file", effect: tools.EffectWorkspaceWrite})
	registry.Register(effectStubTool{name: "exec_command", effect: tools.EffectWorkspaceWrite})
	return registry
}

func taskCaps(t *testing.T, tool *TaskTool, args map[string]any) tools.ToolCapabilities {
	t.Helper()
	return tools.CapabilitiesForArgsOf(tool, args)
}

// TestTaskToolReadOnlyAgentIsParallelSafe is the regression guard for the
// defect where Task delegations were never batch-eligible: the tool must
// classify a read-only agent as EffectReadOnly + ThreadSafe so the loop's
// parallel batcher runs several delegations at once.
func TestTaskToolReadOnlyAgentIsParallelSafe(t *testing.T) {
	supervisor := NewSupervisor(fakeRunner(), ChildRunContext{Registry: parentRegistry()})
	tool := NewTaskTool(supervisor)

	caps := taskCaps(t, tool, map[string]any{"agent": "explorer", "prompt": "x"})
	if caps.Effect != tools.EffectReadOnly || !caps.ThreadSafe {
		t.Fatalf("read-only explorer must be parallel-safe, got %+v", caps)
	}
	if keys := caps.ResourceKeys(map[string]any{"agent": "explorer"}); len(keys) != 1 || keys[0] != "agent:explorer" {
		t.Fatalf("expected per-agent conflict key, got %v", keys)
	}
}

// A write-capable agent must stay sequential, and fail-closed paths (unknown
// agent, background, missing arg) must never be batch-eligible.
func TestTaskToolNonReadOnlyAndFailClosed(t *testing.T) {
	supervisor := NewSupervisor(fakeRunner(), ChildRunContext{Registry: parentRegistry()})
	tool := NewTaskTool(supervisor)

	cases := []struct {
		name string
		args map[string]any
	}{
		{"write-capable worker", map[string]any{"agent": "worker", "prompt": "x"}},
		{"verifier runs commands", map[string]any{"agent": "verifier", "prompt": "x"}},
		{"unknown agent", map[string]any{"agent": "nope", "prompt": "x"}},
		{"background", map[string]any{"agent": "explorer", "prompt": "x", "run_in_background": true}},
		{"missing agent", map[string]any{"prompt": "x"}},
	}
	for _, testCase := range cases {
		if caps := taskCaps(t, tool, testCase.args); caps.Effect == tools.EffectReadOnly && caps.ThreadSafe {
			t.Fatalf("%s must not be parallel-safe, got %+v", testCase.name, caps)
		}
	}
}

// A read-only agent is only safe relative to what the parent registry can
// actually see; without a registry the gate fails closed.
func TestTaskToolFailsClosedWithoutRegistry(t *testing.T) {
	supervisor := NewSupervisor(fakeRunner(), ChildRunContext{})
	tool := NewTaskTool(supervisor)
	if caps := taskCaps(t, tool, map[string]any{"agent": "explorer", "prompt": "x"}); caps.Effect == tools.EffectReadOnly {
		t.Fatal("no parent registry must fail closed")
	}
}
