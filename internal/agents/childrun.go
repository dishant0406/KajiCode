package agents

import (
	"context"
	"fmt"
	"strings"

	"github.com/dishant0406/KajiCode/internal/agent"
	"github.com/dishant0406/KajiCode/internal/sessions"
	"github.com/dishant0406/KajiCode/internal/streamjson"
)

// RunChildAgent is the default child runner: it runs a child agent IN-PROCESS
// with the parent's provider, sandbox, and permission policy, on a registry
// filtered to the tools the child's ruleset allows. There is no child process:
// the child shares the parent's provider session and the same MCP/plugin tools,
// which is why a child can use MCP and plugin tools the old subprocess model
// could never grant.
func RunChildAgent(ctx context.Context, request ChildRequest) (ChildResult, error) {
	run := request.Context
	if run.Provider == nil {
		return ChildResult{AgentName: request.Agent.Name}, fmt.Errorf("no provider available for child agent")
	}

	provider := run.Provider
	if model := strings.TrimSpace(request.Agent.Model); model != "" && run.ResolveModel != nil {
		resolved, err := run.ResolveModel(ctx, model)
		if err != nil {
			return ChildResult{AgentName: request.Agent.Name}, fmt.Errorf("resolve child model %q: %w", model, err)
		}
		if resolved != nil {
			provider = resolved
		}
	}

	// The child session is a real, parent-linked child session so it is
	// drillable and resumable and its transcript persists. When the parent run
	// has no durable session (e.g. a stateless headless run), the child runs
	// without one and its result is returned inline.
	sessionID := ""
	var store *sessions.Store
	if run.Store != nil && run.ParentSessionID != "" {
		child, err := run.Store.CreateChild(run.ParentSessionID, sessions.ChildInput{
			Title:     childSessionTitle(request),
			Cwd:       run.Cwd,
			ModelID:   request.Agent.Model,
			Tag:       sessionTagAgent,
			Depth:     request.Depth + 1,
			AgentName: request.Agent.Name,
			TaskID:    request.TaskID,
			Prompt:    request.Prompt,
		})
		if err != nil {
			return ChildResult{AgentName: request.Agent.Name}, fmt.Errorf("create child session: %w", err)
		}
		sessionID = child.SessionID
		store = run.Store
	}
	request.Context.emitRunStart(sessionID, request.Agent.Name)
	recorder := &childRecorder{sessionID: sessionID, store: store, emit: run.OnEvent}
	recorder.append(sessions.EventSpecialistStart, map[string]any{
		"specialist":     request.Agent.Name,
		"childSessionId": sessionID,
		"description":    request.Description,
		"status":         "running",
	})

	registry := FilterRegistry(run.Registry, Rules(request.Agent.Tools, request.Agent.ExcludeTools))
	childPrompt := buildChildPrompt(run.SystemPromptPrefix, request.Agent, request.Prompt)

	recorder.append(sessions.EventMessage, map[string]any{"role": "user", "content": request.Prompt})

	result, err := agent.Run(ctx, childPrompt, provider, agent.Options{
		MaxTurns:        firstPositive(request.Agent.Steps, run.MaxTurns),
		ContextWindow:   run.ContextWindow,
		Depth:           request.Depth + 1,
		SessionID:       sessionID,
		Cwd:             run.Cwd,
		SystemPrompt:    childPrompt,
		Model:           firstNonEmpty(request.Agent.Model, run.Model),
		ReasoningEffort: firstNonEmpty(request.Agent.Thinking, run.Reasoning),
		Registry:        registry,
		PermissionMode:  run.PermissionMode,
		Autonomy:        run.Autonomy,
		Sandbox:         run.Sandbox,
		FileTracker:     run.FileTracker,
		SessionStore:    run.SessionStore,
		Hooks:           run.Hooks,
		OnToolCall:      recorder.onToolCall,
		OnToolResult:    recorder.onToolResult,
		OnText:          recorder.onText,
		OnUsage:         recorder.onUsage,
	})

	status := statusCompleted
	if err != nil {
		status = statusError
	}
	childResult := ChildResult{
		AgentName: request.Agent.Name,
		SessionID: sessionID,
		Text:      strings.TrimSpace(result.FinalAnswer),
		Status:    status,
		Turns:     result.Turns,
	}
	recorder.append(sessions.EventSpecialistStop, map[string]any{
		"specialist":     request.Agent.Name,
		"childSessionId": sessionID,
		"description":    request.Description,
		"status":         status,
		"turns":          result.Turns,
		"error":          errorText(err),
	})
	request.Context.emitRunEnd(sessionID, request.Agent.Name, status)
	if err != nil {
		return childResult, err
	}
	return childResult, nil
}

const sessionTagAgent = "agent"

func childSessionTitle(request ChildRequest) string {
	name := strings.TrimSpace(request.Agent.Name)
	if description := strings.TrimSpace(request.Description); description != "" {
		return name + ": " + description
	}
	return name
}

// buildChildPrompt wraps the agent's own system prompt with a delegated-task
// contract so the child knows it is a hidden helper: it does not inherit the
// parent conversation, does not perform final user delivery, and returns its
// result to the parent. The parent's own system prompt is prepended so project
// guidelines and harness rules still apply.
func buildChildPrompt(parentPrompt string, agent Agent, prompt string) string {
	var b strings.Builder
	if trimmed := strings.TrimSpace(parentPrompt); trimmed != "" {
		b.WriteString(trimmed)
		b.WriteString("\n\n")
	}
	b.WriteString("You are ")
	b.WriteString(agent.Name)
	b.WriteString(", a delegated sub-agent.\n")
	if body := strings.TrimSpace(agent.SystemPrompt); body != "" {
		b.WriteString(body)
		b.WriteString("\n\n")
	}
	b.WriteString(delegatedTaskContract)
	b.WriteString("\n\nTask:\n")
	b.WriteString(strings.TrimSpace(prompt))
	return b.String()
}

const delegatedTaskContract = `## Delegated task contract

You are a hidden child agent working for a parent agent. You do not inherit the
parent's conversation; this task is your full context. Do not delegate further
unless your tools include Task. When you finish, return a concise result the
parent can act on: what you did or found, concrete evidence (file paths, line
references, command output), and any blockers. The parent owns final delivery to
the user, so do not write a user-facing summary.`

// firstPositive returns a when a > 0, else b.
func firstPositive(a, b int) int {
	if a > 0 {
		return a
	}
	return b
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (ctx ChildRunContext) emitRunStart(sessionID, name string) {
	if ctx.OnEvent == nil {
		return
	}
	ctx.OnEvent(streamjson.Event{Type: streamjson.EventRunStart, SessionID: sessionID, Name: name})
}

func (ctx ChildRunContext) emitRunEnd(sessionID, name, status string) {
	if ctx.OnEvent == nil {
		return
	}
	ctx.OnEvent(streamjson.Event{Type: streamjson.EventRunEnd, SessionID: sessionID, Name: name, Action: status})
}
