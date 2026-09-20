package agents

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/dishant0406/KajiCode/internal/agent"
	"github.com/dishant0406/KajiCode/internal/hooks"
	"github.com/dishant0406/KajiCode/internal/sandbox"
	"github.com/dishant0406/KajiCode/internal/sessions"
	"github.com/dishant0406/KajiCode/internal/streamjson"
	"github.com/dishant0406/KajiCode/internal/tools"
)

// DefaultMaxDepth bounds nested Task delegation when the operator has not
// configured one. It exists purely to stop a runaway delegation chain.
const DefaultMaxDepth = 4

// Runner resolves agent definitions and starts child runs. It holds only the
// paths it loads from and a depth cap; every runtime handle a child needs comes
// from the per-call ChildRunContext, so the runner has no dependency on CLI
// wiring.
type Runner struct {
	Paths    Paths
	MaxDepth int
	// Load overrides the on-disk load (tests inject a fixed set).
	Load func(Paths) (LoadResult, error)
}

// NewRunner builds a runner over the given paths and depth cap.
func NewRunner(paths Paths, maxDepth int) *Runner {
	if maxDepth <= 0 {
		maxDepth = DefaultMaxDepth
	}
	return &Runner{Paths: paths, MaxDepth: maxDepth}
}

func (runner *Runner) depthCap() int {
	if runner.MaxDepth > 0 {
		return runner.MaxDepth
	}
	return DefaultMaxDepth
}

// LoadAgents returns the merged agent set.
func (runner *Runner) LoadAgents() (LoadResult, error) {
	if runner.Load != nil {
		return runner.Load(runner.Paths)
	}
	return Load(runner.Paths)
}

// Resolve returns the agent for a name or alias, with a corrective error that
// lists the registered agents when the name is unknown (models often invent
// names and would otherwise retry blindly).
func (runner *Runner) Resolve(name string) (Agent, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Agent{}, errAgentNameRequired
	}
	result, err := runner.LoadAgents()
	if err != nil {
		return Agent{}, err
	}
	resolved, ok := Find(result, name)
	if !ok {
		return Agent{}, errUnknownAgent(name, Names(result))
	}
	return resolved, nil
}

// List returns the agents a Task caller may delegate to.
func (runner *Runner) List() ([]Agent, error) {
	result, err := runner.LoadAgents()
	if err != nil {
		return nil, err
	}
	return Spawnable(result), nil
}

// ChildRunContext carries the parent run's runtime handles a child reuses. The
// zero value is not usable: Registry and Provider are required.
type ChildRunContext struct {
	// Registry is the PARENT's live registry. The child's registry is always a
	// filtered subset of it, so a child can never reach a tool the parent lacked.
	Registry *tools.Registry
	// Provider is the already-built parent provider, reused when the child does
	// not override the model.
	Provider agent.Provider
	// ResolveModel builds a provider for a model id (the child's model override).
	// nil means model overrides are ignored and the parent provider is reused.
	ResolveModel func(ctx context.Context, model string) (agent.Provider, error)

	// ParentSessionID links the child session to the run that spawned it.
	ParentSessionID string

	PermissionMode agent.PermissionMode
	Autonomy       string
	Cwd            string
	Sandbox        *sandbox.Engine
	FileTracker    *tools.FileTracker
	SessionStore   tools.SessionStore
	Store          *sessions.Store
	Hooks          *hooks.Dispatcher
	MaxTurns       int
	ContextWindow  int
	Model          string
	Reasoning      string
	// SystemPromptPrefix is the parent's own system prompt, prepended to the
	// child's agent prompt so project guidelines and harness rules still apply.
	SystemPromptPrefix string

	// OnEvent, when set, receives synthesized events for a surface to render
	// live child progress. nil is a no-op.
	OnEvent func(streamjson.Event)
}

// ChildRequest is one child run.
type ChildRequest struct {
	Agent       Agent
	Prompt      string
	Description string
	Context     ChildRunContext
	Depth       int
	TaskID      string
}

// ChildResult is the outcome of one child run.
type ChildResult struct {
	AgentName string
	SessionID string
	Text      string
	Status    string // "completed" | "error" | "killed"
	Turns     int
}

// ChildRunFunc runs one child and returns its result. The default is
// RunChildAgent; tests inject a fake so the Task tool is exercised without a
// provider.
type ChildRunFunc func(ctx context.Context, request ChildRequest) (ChildResult, error)

// RunChild enforces the depth cap and delegates to run (or RunChildAgent).
func (runner *Runner) RunChild(ctx context.Context, request ChildRequest, run ChildRunFunc) (ChildResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(request.Prompt) == "" {
		return ChildResult{AgentName: request.Agent.Name}, errPromptRequired
	}
	if request.Depth >= runner.depthCap() {
		return ChildResult{AgentName: request.Agent.Name}, errDepthExceeded(request.Depth, runner.depthCap())
	}
	request.Agent.Rules = Rules(request.Agent.Tools, request.Agent.ExcludeTools)
	if run == nil {
		run = RunChildAgent
	}
	result, err := run(ctx, request)
	if result.AgentName == "" {
		result.AgentName = request.Agent.Name
	}
	return result, err
}

// ChildCompletion is one finished background child, delivered to the parent run.
type ChildCompletion struct {
	TaskID      string
	AgentName   string
	Description string
	Status      string
	Summary     string
	FinishedAt  time.Time
}

// completionQueue is the bounded queue of finished background children. It
// implements agent.TaskCompletionSource so the parent loop can drain results
// without the model polling TaskOutput.
type completionQueue struct {
	mu      sync.Mutex
	enabled bool
	pending []ChildCompletion
	seen    map[string]bool
}

func newCompletionQueue() *completionQueue {
	return &completionQueue{seen: map[string]bool{}}
}

func (queue *completionQueue) setEnabled(enabled bool) {
	queue.mu.Lock()
	queue.enabled = enabled
	queue.mu.Unlock()
}

func (queue *completionQueue) push(completion ChildCompletion) {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if !queue.enabled || queue.seen[completion.TaskID] {
		return
	}
	queue.seen[completion.TaskID] = true
	queue.pending = append(queue.pending, completion)
}

func (queue *completionQueue) drain() []ChildCompletion {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if len(queue.pending) == 0 {
		return nil
	}
	out := queue.pending
	queue.pending = nil
	return out
}

const (
	statusCompleted = "completed"
	statusError     = "error"

	// maxCompletionSummaryBytes bounds how much of a finished child's final text
	// rides back into the parent context.
	maxCompletionSummaryBytes = 8 * 1024
)

func renderCompletion(completion ChildCompletion) string {
	state := "error"
	if completion.Status == statusCompleted {
		state = "completed"
	}
	var b strings.Builder
	b.WriteString("Background sub-agent task finished (result below was produced by the sub-agent, not verified by you):\n")
	b.WriteString("<task_result id=\"" + completion.TaskID + "\" state=\"" + state + "\">\n")
	b.WriteString("<summary>agent " + completion.AgentName)
	if trimmed := strings.TrimSpace(completion.Description); trimmed != "" {
		b.WriteString(": " + trimmed)
	}
	b.WriteString("</summary>\n")
	summary := strings.TrimSpace(completion.Summary)
	if summary == "" {
		summary = "(the sub-agent produced no text output)"
	}
	if len(summary) > maxCompletionSummaryBytes {
		summary = summary[:maxCompletionSummaryBytes] + "\n…(truncated)"
	}
	b.WriteString(summary)
	b.WriteString("\n</task_result>")
	return b.String()
}
