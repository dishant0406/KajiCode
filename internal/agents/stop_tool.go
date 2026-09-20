package agents

import (
	"context"
	"strings"

	"github.com/dishant0406/KajiCode/internal/tools"
)

// TaskStopToolName is the model-facing name of the background-task stopper.
const TaskStopToolName = "TaskStop"

// StopTool cancels a running background child.
type StopTool struct {
	supervisor *Supervisor
}

// NewStopTool builds the TaskStop tool.
func NewStopTool(supervisor *Supervisor) *StopTool {
	return &StopTool{supervisor: supervisor}
}

func (tool *StopTool) Name() string { return TaskStopToolName }

func (tool *StopTool) Description() string {
	return "Stop a running background sub-agent task."
}

func (tool *StopTool) Parameters() tools.Schema {
	return tools.Schema{
		Type: "object",
		Properties: map[string]tools.PropertySchema{
			"task_id": {
				Type:        "string",
				Description: "Task id returned by Task with run_in_background.",
			},
		},
		Required:             []string{"task_id"},
		AdditionalProperties: false,
	}
}

func (tool *StopTool) Safety() tools.Safety {
	return tools.Safety{
		SideEffect: tools.SideEffectLocalControl,
		Permission: tools.PermissionAllow,
		Reason:     "Cancels a background sub-agent task.",
	}
}

func (tool *StopTool) Run(_ context.Context, args map[string]any) tools.Result {
	if tool.supervisor == nil {
		return errorResult("agent runtime is not available")
	}
	taskID, err := stringArg(args, "task_id")
	if err != nil {
		return errorResult(err.Error())
	}
	if taskID == "" {
		return errorResult("TaskStop requires a task_id")
	}
	task, ok := tool.supervisor.getTask(taskID)
	if !ok {
		return errorResult("background task not found: " + taskID)
	}
	status, _, _ := task.snapshot()
	if status != "running" {
		return tools.Result{Status: tools.StatusOK, Output: "Task " + taskID + " is already " + status + "."}
	}
	task.cancel()
	return tools.Result{Status: tools.StatusOK, Output: strings.TrimSpace("Task " + taskID + " canceled.")}
}
