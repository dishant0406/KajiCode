package agents

import (
	"context"
	"fmt"
	"strings"

	"github.com/dishant0406/KajiCode/internal/tools"
)

// TaskToolName is the model-facing name of the delegation tool.
const TaskToolName = "Task"

// TaskTool launches a child agent. It runs the child IN-PROCESS (no subprocess)
// on a registry filtered to the tools the child's ruleset allows, with the
// parent's provider, sandbox, and permission policy.
type TaskTool struct {
	supervisor *Supervisor
}

// NewTaskTool builds the Task tool over a supervisor.
func NewTaskTool(supervisor *Supervisor) *TaskTool {
	return &TaskTool{supervisor: supervisor}
}

func (tool *TaskTool) Name() string { return TaskToolName }

func (tool *TaskTool) Description() string {
	base := "Launch a sub-agent to handle a focused, bounded task autonomously. " +
		"The sub-agent runs with its own context and tool permissions and returns a single result.\n\n" +
		"Delegate focused or read-heavy work to a sub-agent instead of doing it inline: it keeps large " +
		"tool output (searches, file dumps, multi-step exploration) out of your own context. When a task " +
		"matches a sub-agent's purpose, delegate to it proactively; for independent subtasks, launch several " +
		"in parallel. Handle small, direct edits yourself. The sub-agent does not see this conversation, so " +
		"give it the full context it needs in the prompt."
	if directory := tool.agentDirectory(); directory != "" {
		return base + "\n\n" + directory
	}
	return base
}

func (tool *TaskTool) agentDirectory() string {
	if tool.supervisor == nil {
		return ""
	}
	agents := tool.supervisor.List()
	if len(agents) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Available agent types:")
	for _, agent := range agents {
		description := strings.TrimSpace(agent.Description)
		if description == "" {
			description = "Focused helper agent."
		}
		b.WriteString("\n- " + agent.Name + ": " + description)
	}
	return b.String()
}

func (tool *TaskTool) Parameters() tools.Schema {
	return tools.Schema{
		Type: "object",
		Properties: map[string]tools.PropertySchema{
			"agent": {
				Type:        "string",
				Description: "Agent type to run, such as worker, explorer, or code-review.",
			},
			"prompt": {
				Type:        "string",
				Description: "The focused task for the agent. Include any context the agent needs; it does not see this conversation.",
			},
			"description": {
				Type:        "string",
				Description: "Short 3-5 word label for the task.",
			},
			"run_in_background": {
				Type:        "boolean",
				Description: "Run the agent in the background and return a task id immediately.",
				Default:     false,
			},
		},
		Required:             []string{"agent", "prompt"},
		AdditionalProperties: false,
	}
}

func (tool *TaskTool) Safety() tools.Safety {
	return tools.Safety{
		SideEffect: tools.SideEffectShell,
		Permission: tools.PermissionAllow,
		Reason:     "Runs a sub-agent whose own tool permissions bound what it can do.",
	}
}

// CapabilitiesForArgs implements tools.ArgsCapabilityProvider so the agent
// loop's parallel batcher classifies THIS call by the target agent's effect
// instead of the tool's static shell classification. A fresh delegation to a
// read-only agent (explorer, code-review) is a pure workspace read, so several
// of them in one turn run concurrently; a write-capable agent (worker,
// verifier) stays sequential because it mutates the workspace.
//
// Resume re-enters an existing session whose current toolset may differ from
// the definition, and background launches are already detached, so both fail
// closed to the sequential path. Unknown args and an unresolvable agent name
// also fail closed.
func (tool *TaskTool) CapabilitiesForArgs(args map[string]any) tools.ToolCapabilities {
	if tool.supervisor == nil || tool.supervisor.Base.Registry == nil {
		return tools.UnknownCapabilities()
	}
	agentName, err := stringArg(args, "agent")
	if err != nil || agentName == "" {
		return tools.UnknownCapabilities()
	}
	if background, err := boolArg(args, "run_in_background"); err != nil || background {
		return tools.UnknownCapabilities()
	}
	resolved, err := tool.supervisor.Runner.Resolve(agentName)
	if err != nil {
		return tools.UnknownCapabilities()
	}
	if !delegationIsReadOnly(tool.supervisor.Base.Registry, Rules(resolved.Tools, resolved.ExcludeTools)) {
		return tools.UnknownCapabilities()
	}
	return tools.ToolCapabilities{
		Effect:     tools.EffectReadOnly,
		ThreadSafe: true,
		ResourceKeys: func(map[string]any) []string {
			// One conflict key per agent name: two concurrent delegations to the
			// same read-only agent still serialize (they duplicate work on
			// identical context), while distinct agents run side by side.
			return []string{"agent:" + resolved.Name}
		},
	}
}

func (tool *TaskTool) Run(ctx context.Context, args map[string]any) tools.Result {
	return tool.RunWithOptions(ctx, args, tools.RunOptions{})
}

// RunWithOptions implements tools.optionsAwareTool so the child inherits the
// parent call's depth, session id, cwd, permission mode, and progress sink.
func (tool *TaskTool) RunWithOptions(ctx context.Context, args map[string]any, options tools.RunOptions) tools.Result {
	if tool.supervisor == nil {
		return errorResult("agent runtime is not available")
	}
	agentName, err := stringArg(args, "agent")
	if err != nil {
		return errorResult(err.Error())
	}
	prompt, err := stringArg(args, "prompt")
	if err != nil {
		return errorResult(err.Error())
	}
	if agentName == "" {
		return errorResult("Task requires an agent name")
	}
	if prompt == "" {
		return errorResult("Task requires a prompt")
	}
	description, _ := stringArg(args, "description")
	background, err := boolArg(args, "run_in_background")
	if err != nil {
		return errorResult(err.Error())
	}

	resolved, err := tool.supervisor.Runner.Resolve(agentName)
	if err != nil {
		return errorResult(err.Error())
	}

	runContext := tool.supervisor.Base
	runContext.ParentSessionID = strings.TrimSpace(options.SessionID)
	if options.Progress != nil {
		runContext.OnEvent = options.Progress
	}

	request := ChildRequest{
		Agent:       resolved,
		Prompt:      prompt,
		Description: description,
		Context:     runContext,
		Depth:       options.Depth,
	}

	if background {
		id, err := tool.supervisor.startBackground(ctx, request)
		if err != nil {
			return errorResult(err.Error())
		}
		output := fmt.Sprintf("Task launched in background.\ntask_id: %s\nagent: %s\n\nUse TaskOutput with task_id %q to check progress.", id, resolved.Name, id)
		return tools.Result{Status: tools.StatusOK, Output: output, Meta: map[string]string{"task_id": id, "session_id": id}}
	}

	result, err := tool.supervisor.runChild(ctx, request)
	if err != nil {
		text := strings.TrimSpace(result.Text)
		if text == "" {
			text = err.Error()
		}
		return tools.Result{Status: tools.StatusError, Output: "Error: " + text, Meta: sessionMeta(result.SessionID)}
	}
	return tools.Result{Status: tools.StatusOK, Output: renderResult(result), Meta: sessionMeta(result.SessionID)}
}

func renderResult(result ChildResult) string {
	text := strings.TrimSpace(result.Text)
	if text == "" {
		text = "(the sub-agent produced no text output)"
	}
	return fmt.Sprintf("<task_result agent=%q session=%q>\n%s\n</task_result>", result.AgentName, result.SessionID, text)
}

func sessionMeta(sessionID string) map[string]string {
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}
	return map[string]string{"session_id": sessionID}
}
