package tools

import "context"

// FinishToolName is the model-facing name of the tool that ends a run. In runs
// that require it (agent.Options.RequireFinish) the agent loop intercepts the
// call and ends the run; a text-only reply no longer ends such a run.
const FinishToolName = "finish"

// finishTool is a control tool: it performs no read, write, shell, or network
// effect. It only tells the agent loop that the model considers the task done.
type finishTool struct {
	baseTool
}

// NewFinishTool builds the finish tool. It is advertised only in runs that
// require it (see agent.partitionToolsCached).
func NewFinishTool() Tool {
	return finishTool{
		baseTool: baseTool{
			name: FinishToolName,
			description: "End this run and hand control back to the user. This is the ONLY way to finish a task: " +
				"a reply without a tool call does not end the run. Call it alone, after you have seen the results " +
				"of your other tool calls and the whole task is complete and checked. " +
				"Do not call it to announce a next step; do the step instead. " +
				"If you need the user's input to continue, call ask_user instead.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"summary": {
						Type:        "string",
						Description: "Final message for the user: what was done, what was verified, and anything left open.",
						MinLength:   intPtr(1),
					},
				},
				Required:             []string{"summary"},
				AdditionalProperties: false,
			},
			safety: Safety{
				SideEffect: SideEffectNone,
				Permission: PermissionAllow,
				Reason:     "Ends the run; performs no read, write, shell, or network effect.",
			},
			capabilities: ToolCapabilities{Effect: EffectInteractive, ThreadSafe: false},
		},
	}
}

// Run is the fallback path, reached only when a run that does not require
// finish receives the call anyway. Such a run ends with a plain text reply.
func (tool finishTool) Run(context.Context, map[string]any) Result {
	return Result{
		Status: StatusOK,
		Output: "finish is not used in this run. Reply with your final answer as plain text to end it.",
	}
}
