package agent

import (
	"context"
	"strings"

	"github.com/dishant0406/KajiCode/internal/tools"
)

// Plan mode is a read-only planning phase that is orthogonal to PermissionMode:
// the model may inspect the workspace, ask the user, and record a plan, but must
// not change anything until the plan is approved. Two layers enforce it — the
// planModeContext prompt section steers the model, and the planModeDenied gate
// in executeToolCall is the hard backstop that cannot be talked around.

// PlanExecutionPrompt is the synthetic user turn a surface submits after the
// user approves a plan. It carries the model's own plan (recorded with
// todo_write) into a fresh, non-plan-mode run so execution starts without the
// user retyping.
const PlanExecutionPrompt = "The plan above was approved. Execute it now, following the recorded todo list step by step."

// planModeAllowedTools are the non-read-only tools that stay available in plan
// mode. todo_write records the plan; ask_user and request_permissions gather
// input; escalate_model and exit_plan_mode are control signals. Every other
// non-read-only tool is denied.
var planModeAllowedTools = map[string]bool{
	"todo_write":               true,
	"ask_user":                 true,
	"request_permissions":      true,
	"escalate_model":           true,
	tools.ExitPlanModeToolName: true,
}

// planModeBlockedDispatchers are tools that declare a read-only effect but
// re-enter the tool registry with arbitrary sub-tools (batch, recipe_run). Their
// declared effect is not the real effect, so plan mode blocks them outright; a
// genuinely read-only sub-call can be issued directly instead.
var planModeBlockedDispatchers = map[string]bool{
	"batch":      true,
	"recipe_run": true,
}

// planModeDenied reports whether a tool call must be blocked while plan mode is
// active. Read-only tools are allowed; everything else is denied, including
// tools whose effect is unknown (MCP, plugin, and sub-agent tools) so they fail
// closed rather than silently mutating during planning.
func planModeDenied(name string, tool tools.Tool) bool {
	if planModeAllowedTools[name] {
		return false
	}
	if planModeBlockedDispatchers[name] {
		return true
	}
	if tool == nil {
		return true
	}
	return tools.CapabilitiesOf(tool).Effect != tools.EffectReadOnly
}

// planModeDeniedResult renders the tool result for a call blocked by plan mode.
// It is a DenialFiltered error the model can read and act on (record the plan
// and call exit_plan_mode) rather than a silent no-op.
func planModeDeniedResult(call ToolCall) ToolResult {
	return ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
		Status:     tools.StatusError,
		Output: "Error: tool \"" + call.Name + "\" is disabled while plan mode is active. " +
			"Investigate read-only, record the plan with todo_write, then call " +
			tools.ExitPlanModeToolName + " to propose leaving plan mode.",
		DenialReason: DenialFiltered,
	}
}

// planModeContext renders the plan-mode system-prompt directive. It returns ""
// when plan mode is off, keeping the prompt byte-identical to a normal run.
func planModeContext(options Options) string {
	if !options.PlanMode {
		return ""
	}
	return "PLAN MODE IS ACTIVE — you are planning, not executing.\n" +
		"- Do NOT modify anything: no file writes or edits, no shell commands, no sub-agents.\n" +
		"- Investigate read-only with read_file, grep, glob, ls, and the other read tools.\n" +
		"- Record the plan as an ordered todo_write list: each step, the files it touches, and how it is verified.\n" +
		"- Use ask_user only for a genuinely blocking decision; otherwise state your assumptions.\n" +
		"- Present the plan in your reply, then call " + tools.ExitPlanModeToolName +
		" to ask the user to approve it and switch to execution."
}

// executeExitPlanMode handles the exit_plan_mode tool. It asks the user — through
// the same interactive channel ask_user uses — whether to leave plan mode and
// start executing, and carries the decision back on Meta["plan_exit"] so the
// surface can clear plan mode and begin execution. Without an interactive user
// it declines, keeping the run read-only.
func executeExitPlanMode(ctx context.Context, call ToolCall, options Options) (ToolResult, error) {
	if !options.PlanMode {
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Status:     tools.StatusOK,
			Output:     "exit_plan_mode has no effect: plan mode is not active.",
		}, nil
	}
	if options.OnAskUser == nil {
		return planExitResult(call, false,
			"No interactive user is available to approve the plan; keep planning and finish with the plan in your reply."), nil
	}
	response, err := options.OnAskUser(ctx, AskUserRequest{
		ToolCallID: call.ID,
		Header:     "Plan ready",
		Questions: []AskUserQuestion{{
			Question:    "The plan is ready. Leave plan mode and execute it?",
			Options:     []string{"Yes, execute the plan", "No, keep planning"},
			Recommended: "Yes, execute the plan",
		}},
	})
	if err != nil {
		// A canceled / timed-out prompt must abort the run, exactly like ask_user.
		return ToolResult{}, err
	}
	if len(response.Answers) > 0 && strings.HasPrefix(strings.ToLower(strings.TrimSpace(response.Answers[0])), "yes") {
		return planExitResult(call, true,
			"Plan approved. Plan mode ends and execution begins on the next turn; do not attempt edits in this turn."), nil
	}
	return planExitResult(call, false,
		"The user chose to keep planning. Refine the plan and call exit_plan_mode again when it is ready."), nil
}

// planExitResult builds the exit_plan_mode tool result, stamping Meta["plan_exit"]
// with "approved" or "declined" so a surface can react without parsing Output.
func planExitResult(call ToolCall, approved bool, output string) ToolResult {
	decision := "declined"
	if approved {
		decision = "approved"
	}
	return ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
		Status:     tools.StatusOK,
		Output:     output,
		Meta:       map[string]string{"plan_exit": decision},
	}
}
