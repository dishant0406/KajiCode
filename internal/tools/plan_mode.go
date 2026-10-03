package tools

import "context"

// ExitPlanModeToolName is the model-facing name of the tool that asks the user
// to approve a plan and leave plan mode. The agent loop intercepts it and routes
// the question to an interactive front-end; this tool's Run() is the fallback
// used when nothing intercepts the call (headless runs), where it declines.
const ExitPlanModeToolName = "exit_plan_mode"

// exitPlanModeTool asks the user to approve the plan and switch to execution.
// It is a control tool: it performs no read, write, shell, or network effect.
type exitPlanModeTool struct {
	baseTool
}

// NewExitPlanModeTool builds the exit_plan_mode tool. The agent loop intercepts
// exit_plan_mode calls and asks the user through the same channel ask_user uses;
// this tool's Run() is the fallback used when nothing intercepts the call.
func NewExitPlanModeTool() Tool {
	return exitPlanModeTool{
		baseTool: baseTool{
			name: ExitPlanModeToolName,
			description: "Propose leaving plan mode and starting execution. Call this ONLY in plan mode, " +
				"after you have recorded the plan with todo_write and presented it in your reply. " +
				"The user is asked to approve; on approval plan mode ends and the next turn executes the plan. " +
				"This tool does not modify anything itself.",
			parameters: Schema{
				Type:                 "object",
				Properties:           map[string]PropertySchema{},
				Required:             []string{},
				AdditionalProperties: false,
			},
			safety: Safety{
				SideEffect: SideEffectNone,
				Permission: PermissionAllow,
				Reason:     "Asks the user to approve a plan; performs no read, write, shell, or network effect.",
			},
			capabilities: ToolCapabilities{Effect: EffectInteractive, ThreadSafe: false},
		},
	}
}

// Run is the fallback path, reached only when nothing intercepts the call (no
// interactive user). It declines rather than fabricating approval, so a headless
// plan-mode run stays read-only and ends with the plan.
func (tool exitPlanModeTool) Run(context.Context, map[string]any) Result {
	return Result{
		Status: StatusOK,
		Output: "No interactive user is available to approve the plan; keep planning and finish with the plan in your reply.",
	}
}
