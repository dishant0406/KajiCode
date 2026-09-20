package agents

import (
	"context"
	"strings"
	"sync"

	"github.com/dishant0406/KajiCode/internal/tools"
)

// Supervisor is the registry-facing owner of the agent runtime: it resolves
// agents, runs foreground and background children, and delivers finished
// background results to the parent run. One Supervisor is created per top-level
// run.
type Supervisor struct {
	Runner *Runner
	// Base is the parent run's runtime handles (provider, registry, sandbox,
	// permission mode, session store, ...). The Task tool fills in the per-call
	// fields (depth, parent session, progress sink) from tools.RunOptions.
	Base  ChildRunContext
	Queue *completionQueue
	// RunFunc overrides the child runner (tests inject a fake).
	RunFunc ChildRunFunc

	mu    sync.Mutex
	tasks map[string]*taskState
}

// NewSupervisor builds a supervisor for a run.
func NewSupervisor(runner *Runner, base ChildRunContext) *Supervisor {
	return &Supervisor{
		Runner: runner,
		Base:   base,
		Queue:  newCompletionQueue(),
		tasks:  map[string]*taskState{},
	}
}

// SetBase hydrates the supervisor's parent runtime handles once the CLI has
// resolved them (provider, sandbox, permission mode, session store, ...). The
// Task tools can be registered before those exist (so --list-tools and tool
// filter validation see them); this fills the base they run children with.
func (supervisor *Supervisor) SetBase(base ChildRunContext) {
	if supervisor == nil {
		return
	}
	if base.Registry == nil {
		base.Registry = supervisor.Base.Registry
	}
	supervisor.Base = base
}

// NotifyCompletions turns on push delivery of finished background results to the
// parent run. Interactive surfaces enable it; headless runs leave it off and use
// TaskOutput polling instead.
func (supervisor *Supervisor) NotifyCompletions(enabled bool) {
	if supervisor == nil || supervisor.Queue == nil {
		return
	}
	supervisor.Queue.setEnabled(enabled)
}

// DrainCompletedTasks implements agent.TaskCompletionSource.
func (supervisor *Supervisor) DrainCompletedTasks() []string {
	if supervisor == nil || supervisor.Queue == nil {
		return nil
	}
	completions := supervisor.Queue.drain()
	if len(completions) == 0 {
		return nil
	}
	blocks := make([]string, 0, len(completions))
	for _, completion := range completions {
		blocks = append(blocks, renderCompletion(completion))
	}
	return blocks
}

// List returns the delegatable agents for the Task tool's description.
func (supervisor *Supervisor) List() []Agent {
	if supervisor == nil || supervisor.Runner == nil {
		return nil
	}
	list, err := supervisor.Runner.List()
	if err != nil {
		return nil
	}
	return list
}

type taskState struct {
	mu          sync.Mutex
	id          string
	agentName   string
	description string
	status      string
	text        string
	err         error
	cancel      context.CancelFunc
	done        chan struct{}
}

func (task *taskState) finish(result ChildResult, err error) {
	task.mu.Lock()
	task.text = result.Text
	task.err = err
	if err != nil {
		task.status = statusError
	} else {
		task.status = statusCompleted
	}
	task.mu.Unlock()
	close(task.done)
}

func (task *taskState) snapshot() (status, text string, err error) {
	task.mu.Lock()
	defer task.mu.Unlock()
	return task.status, task.text, task.err
}

// newTaskID generates a child task id.
func newTaskID() string {
	return "task_" + randomToken()
}

// startBackground launches a child in a goroutine and returns immediately.
func (supervisor *Supervisor) startBackground(ctx context.Context, request ChildRequest) (string, error) {
	id := request.TaskID
	if id == "" {
		id = newTaskID()
		request.TaskID = id
	}
	runCtx, cancel := context.WithCancel(ctx)
	task := &taskState{id: id, agentName: request.Agent.Name, description: request.Description, status: "running", done: make(chan struct{}), cancel: cancel}

	supervisor.mu.Lock()
	supervisor.tasks[id] = task
	supervisor.mu.Unlock()

	go func() {
		result, err := supervisor.runChild(runCtx, request)
		task.finish(result, err)
		status := statusCompleted
		if err != nil {
			status = statusError
		}
		supervisor.Queue.push(ChildCompletion{
			TaskID:      id,
			AgentName:   request.Agent.Name,
			Description: request.Description,
			Status:      status,
			Summary:     result.Text,
		})
	}()
	return id, nil
}

func (supervisor *Supervisor) runChild(ctx context.Context, request ChildRequest) (ChildResult, error) {
	run := supervisor.RunFunc
	if run == nil {
		run = RunChildAgent
	}
	return supervisor.Runner.RunChild(ctx, request, run)
}

func (supervisor *Supervisor) getTask(id string) (*taskState, bool) {
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	task, ok := supervisor.tasks[id]
	return task, ok
}

// stringArg reads an optional string argument.
func stringArg(args map[string]any, key string) (string, error) {
	if args == nil {
		return "", nil
	}
	value, ok := args[key]
	if !ok || value == nil {
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", errArgType(key, "string")
	}
	return strings.TrimSpace(text), nil
}

func boolArg(args map[string]any, key string) (bool, error) {
	if args == nil {
		return false, nil
	}
	value, ok := args[key]
	if !ok || value == nil {
		return false, nil
	}
	flag, ok := value.(bool)
	if !ok {
		return false, errArgType(key, "boolean")
	}
	return flag, nil
}

func intArg(args map[string]any, key string) (int, error) {
	if args == nil {
		return 0, nil
	}
	value, ok := args[key]
	if !ok || value == nil {
		return 0, nil
	}
	switch typed := value.(type) {
	case int:
		return typed, nil
	case float64:
		return int(typed), nil
	default:
		return 0, errArgType(key, "integer")
	}
}

func errorResult(message string) tools.Result {
	return tools.Result{Status: tools.StatusError, Output: "Error: " + message}
}
