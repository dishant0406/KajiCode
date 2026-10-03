package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/tools"
)

// planModeRegistry registers one representative tool per effect class so the
// gate can be exercised without wiring the whole builtin catalog.
func planModeRegistry(root string) *tools.Registry {
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(root))
	registry.Register(tools.NewGrepTool(root))
	registry.Register(tools.NewWriteFileTool(root))
	registry.Register(tools.NewEditFileTool(root))
	registry.Register(tools.NewApplyPatchTool(root))
	registry.Register(tools.NewMultiEditTool(root))
	registry.Register(tools.NewBashTool(root))
	registry.Register(tools.NewTodoWriteTool())
	registry.Register(tools.NewTodoReadTool())
	registry.Register(tools.NewAskUserTool())
	registry.Register(tools.NewRequestPermissionsTool())
	registry.Register(tools.NewExitPlanModeTool())
	registry.Register(tools.NewBatchTool(registry))
	return registry
}

// TestPlanModeDeniedClassifiesTools is the gate's contract: every mutating or
// unknown-effect tool is denied, and only the read-only + control allowlist
// passes. TaskTool has no static Capabilities(), so it resolves to the
// fail-closed EffectUnknown and is denied — a sub-agent could otherwise write.
func TestPlanModeDeniedClassifiesTools(t *testing.T) {
	registry := planModeRegistry(t.TempDir())
	cases := []struct {
		name string
		want bool // want == denied
	}{
		{"read_file", false},
		{"grep", false},
		{"todo_read", false},
		{"todo_write", false},
		{"ask_user", false},
		{"request_permissions", false},
		{"exit_plan_mode", false},
		{"write_file", true},
		{"edit_file", true},
		{"apply_patch", true},
		{"multi_edit", true},
		{"bash", true},
		{"batch", true},
		{"recipe_run", true},
	}
	for _, tc := range cases {
		tool, _ := registry.Get(tc.name)
		if got := planModeDenied(tc.name, tool); got != tc.want {
			t.Errorf("planModeDenied(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestPlanModeDeniedUnknownToolFailsClosed covers a call whose tool is not even
// registered: the gate must deny rather than pass an unknown call through.
func TestPlanModeDeniedUnknownToolFailsClosed(t *testing.T) {
	if !planModeDenied("some_mcp_tool", nil) {
		t.Fatal("an unregistered tool must be denied in plan mode")
	}
}

// TestPlanModeBlocksMutatingToolAndAllowsReadOnly drives the real loop: in plan
// mode a write_file call returns a filtered denial the model can read, while a
// read_file call executes normally.
func TestPlanModeBlocksMutatingToolAndAllowsReadOnly(t *testing.T) {
	root := t.TempDir()
	registry := planModeRegistry(root)
	provider := &mockProvider{turns: [][]kajicoderuntime.StreamEvent{
		{
			{Type: kajicoderuntime.StreamEventToolCallStart, ToolCallID: "c1", ToolName: "write_file"},
			{Type: kajicoderuntime.StreamEventToolCallDelta, ToolCallID: "c1", ArgumentsFragment: `{"path":"a.txt","content":"x"}`},
			{Type: kajicoderuntime.StreamEventToolCallEnd, ToolCallID: "c1"},
			{Type: kajicoderuntime.StreamEventDone},
		},
		{
			{Type: kajicoderuntime.StreamEventText, Content: "planned"},
			{Type: kajicoderuntime.StreamEventDone},
		},
	}}

	var results []ToolResult
	_, err := Run(context.Background(), "plan a change", provider, Options{
		Registry: registry,
		PlanMode: true,
		OnToolResult: func(result ToolResult) {
			results = append(results, result)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one tool result, got %d", len(results))
	}
	if results[0].DenialReason != DenialFiltered {
		t.Fatalf("write_file DenialReason = %q, want %q", results[0].DenialReason, DenialFiltered)
	}
	if !strings.Contains(results[0].Output, "plan mode") {
		t.Fatalf("denial output should explain plan mode, got %q", results[0].Output)
	}
}

// TestPlanModeOffAllowsWrites is the byte-identical-default guard at the loop
// level: with PlanMode false the same write_file call is not denied by the gate.
func TestPlanModeOffAllowsWrites(t *testing.T) {
	root := t.TempDir()
	registry := planModeRegistry(root)
	provider := &mockProvider{turns: [][]kajicoderuntime.StreamEvent{
		{
			{Type: kajicoderuntime.StreamEventToolCallStart, ToolCallID: "c1", ToolName: "write_file"},
			{Type: kajicoderuntime.StreamEventToolCallDelta, ToolCallID: "c1", ArgumentsFragment: `{"path":"a.txt","content":"x"}`},
			{Type: kajicoderuntime.StreamEventToolCallEnd, ToolCallID: "c1"},
			{Type: kajicoderuntime.StreamEventDone},
		},
		{
			{Type: kajicoderuntime.StreamEventText, Content: "written"},
			{Type: kajicoderuntime.StreamEventDone},
		},
	}}

	var results []ToolResult
	_, err := Run(context.Background(), "write a file", provider, Options{
		Registry: registry,
		OnToolResult: func(result ToolResult) {
			results = append(results, result)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one tool result, got %d", len(results))
	}
	if results[0].DenialReason == DenialFiltered {
		t.Fatalf("write_file must not be plan-mode-filtered when PlanMode is off: %#v", results[0])
	}
}

// TestPlanModePromptSection asserts the steering prompt is present only in plan
// mode, so a normal run's system prompt is unchanged.
func TestPlanModePromptSection(t *testing.T) {
	on := buildSystemPrompt(Options{PlanMode: true})
	if !strings.Contains(on, "PLAN MODE IS ACTIVE") {
		t.Fatalf("plan-mode prompt missing its directive: %q", on)
	}
	if !strings.Contains(on, tools.ExitPlanModeToolName) {
		t.Fatalf("plan-mode prompt should name %s", tools.ExitPlanModeToolName)
	}
	off := buildSystemPrompt(Options{})
	if strings.Contains(off, "PLAN MODE IS ACTIVE") {
		t.Fatal("plan-mode directive leaked into a non-plan run")
	}
}

// TestPlanModeExitApprovedEndsRun covers the approval handoff: exit_plan_mode
// answered "yes" ends the run with PlanApproved set, without a second model turn.
func TestPlanModeExitApprovedEndsRun(t *testing.T) {
	registry := planModeRegistry(t.TempDir())
	provider := exitPlanModeProvider()
	result, err := Run(context.Background(), "plan", provider, Options{
		Registry: registry,
		PlanMode: true,
		OnAskUser: func(_ context.Context, _ AskUserRequest) (AskUserResponse, error) {
			return AskUserResponse{Answers: []string{"Yes, execute the plan"}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.PlanApproved {
		t.Fatal("approving the plan must set Result.PlanApproved")
	}
	if len(provider.requests) != 1 {
		t.Fatalf("approval must end the run after one turn, got %d turns", len(provider.requests))
	}
}

// TestPlanModeExitDeclinedContinues covers the "keep planning" answer: the run
// stays in plan mode and proceeds to the next model turn.
func TestPlanModeExitDeclinedContinues(t *testing.T) {
	registry := planModeRegistry(t.TempDir())
	provider := exitPlanModeProvider()
	result, err := Run(context.Background(), "plan", provider, Options{
		Registry: registry,
		PlanMode: true,
		OnAskUser: func(_ context.Context, _ AskUserRequest) (AskUserResponse, error) {
			return AskUserResponse{Answers: []string{"No, keep planning"}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.PlanApproved {
		t.Fatal("a declined plan must not set Result.PlanApproved")
	}
	if result.FinalAnswer != "refined" {
		t.Fatalf("expected the run to continue to the next turn, got %q", result.FinalAnswer)
	}
}

// TestPlanModeExitWithoutHandlerDeclines keeps a headless plan-mode run
// read-only: with no interactive user the exit tool declines instead of
// fabricating approval.
func TestPlanModeExitWithoutHandlerDeclines(t *testing.T) {
	registry := planModeRegistry(t.TempDir())
	provider := exitPlanModeProvider()
	result, err := Run(context.Background(), "plan", provider, Options{
		Registry: registry,
		PlanMode: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.PlanApproved {
		t.Fatal("no interactive user must not approve the plan")
	}
	if result.FinalAnswer != "refined" {
		t.Fatalf("expected the run to continue, got %q", result.FinalAnswer)
	}
}

// TestPlanModeExitOutsidePlanModeIsInert guards the tool's non-plan behavior: it
// reports no effect rather than ending a normal run.
func TestPlanModeExitOutsidePlanModeIsInert(t *testing.T) {
	result, err := executeExitPlanMode(context.Background(), ToolCall{ID: "c1", Name: tools.ExitPlanModeToolName}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Meta["plan_exit"] != "" {
		t.Fatalf("exit_plan_mode must be inert outside plan mode, got meta %#v", result.Meta)
	}
}

// exitPlanModeProvider returns a provider whose first turn calls exit_plan_mode
// and whose second turn (reached only when the plan is declined) returns text.
func exitPlanModeProvider() *mockProvider {
	return &mockProvider{turns: [][]kajicoderuntime.StreamEvent{
		{
			{Type: kajicoderuntime.StreamEventToolCallStart, ToolCallID: "c1", ToolName: tools.ExitPlanModeToolName},
			{Type: kajicoderuntime.StreamEventToolCallEnd, ToolCallID: "c1"},
			{Type: kajicoderuntime.StreamEventDone},
		},
		{
			{Type: kajicoderuntime.StreamEventText, Content: "refined"},
			{Type: kajicoderuntime.StreamEventDone},
		},
	}}
}
