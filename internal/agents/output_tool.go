package agents

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dishant0406/KajiCode/internal/tools"
)

// TaskOutputToolName is the model-facing name of the background-task reader.
const TaskOutputToolName = "TaskOutput"

const defaultTaskOutputTimeout = 30 * time.Second

// OutputTool reads the status and result of a background Task.
type OutputTool struct {
	supervisor *Supervisor
}

// NewOutputTool builds the TaskOutput tool.
func NewOutputTool(supervisor *Supervisor) *OutputTool {
	return &OutputTool{supervisor: supervisor}
}

func (tool *OutputTool) Name() string { return TaskOutputToolName }

func (tool *OutputTool) Description() string {
	return "Read or wait for the result of a background sub-agent task."
}

func (tool *OutputTool) Parameters() tools.Schema {
	return tools.Schema{
		Type: "object",
		Properties: map[string]tools.PropertySchema{
			"task_id": {
				Type:        "string",
				Description: "Task id returned by Task with run_in_background.",
			},
			"block": {
				Type:        "boolean",
				Description: "Wait for the task to finish before returning, up to timeout.",
				Default:     false,
			},
			"timeout": {
				Type:        "integer",
				Description: "Maximum wait time in milliseconds when block is true.",
				Default:     30000,
			},
		},
		Required:             []string{"task_id"},
		AdditionalProperties: false,
	}
}

func (tool *OutputTool) Safety() tools.Safety {
	return tools.Safety{
		SideEffect: tools.SideEffectRead,
		Permission: tools.PermissionAllow,
		Reason:     "Reads a background sub-agent task result.",
	}
}

func (tool *OutputTool) Run(ctx context.Context, args map[string]any) tools.Result {
	if tool.supervisor == nil {
		return errorResult("agent runtime is not available")
	}
	taskID, err := stringArg(args, "task_id")
	if err != nil {
		return errorResult(err.Error())
	}
	if taskID == "" {
		return errorResult("TaskOutput requires a task_id")
	}
	task, ok := tool.supervisor.getTask(taskID)
	if !ok {
		return errorResult("background task not found: " + taskID)
	}
	block, _ := boolArg(args, "block")
	if block {
		timeoutMS, _ := intArg(args, "timeout")
		if timeoutMS <= 0 {
			timeoutMS = int(defaultTaskOutputTimeout / time.Millisecond)
		}
		timer := time.NewTimer(time.Duration(timeoutMS) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-task.done:
		case <-timer.C:
			status, _, _ := task.snapshot()
			return tools.Result{Status: tools.StatusOK, Output: fmt.Sprintf("Task %s is still %s after %s.", taskID, status, time.Duration(timeoutMS)*time.Millisecond)}
		case <-ctx.Done():
			return errorResult("TaskOutput canceled: " + ctx.Err().Error())
		}
	}
	return tool.render(taskID, task)
}

func (tool *OutputTool) render(taskID string, task *taskState) tools.Result {
	status, text, taskErr := task.snapshot()
	var b strings.Builder
	b.WriteString("task_id: " + taskID + "\n")
	b.WriteString("agent: " + task.agentName + "\n")
	b.WriteString("status: " + status + "\n")
	switch status {
	case "running":
		b.WriteString("\nThe task is still running. Use block: true to wait for it.")
	default:
		if trimmed := strings.TrimSpace(text); trimmed != "" {
			b.WriteString("\n" + trimmed)
		} else if taskErr != nil {
			b.WriteString("\n" + taskErr.Error())
		} else {
			b.WriteString("\n(the sub-agent produced no text output)")
		}
	}
	result := tools.Result{Status: tools.StatusOK, Output: b.String()}
	if status == statusError {
		result.Status = tools.StatusError
	}
	return result
}
