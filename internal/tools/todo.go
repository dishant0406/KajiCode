package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// TodoStatus values mirror opencode's todo states.
const (
	TodoStatusPending    = "pending"
	TodoStatusInProgress = "in_progress"
	TodoStatusCompleted  = "completed"
	TodoStatusCancelled  = "cancelled"
)

// TodoItem is one entry in a session's todo list.
type TodoItem struct {
	Content  string `json:"content"`
	Status   string `json:"status"`
	Priority string `json:"priority,omitempty"`
	Notes    string `json:"notes,omitempty"`
}

// stateTodoKey is the session-state key holding a session's todo list.
const stateTodoKey = "todo"

// NormalizeTodoStatus coerces loose status spellings to the canonical set,
// defaulting unknown values to pending.
func NormalizeTodoStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "complete", "done", "finished", "✓", "x", "[x]":
		return TodoStatusCompleted
	case "in_progress", "in-progress", "inprogress", "active", "doing", "started", "wip", "ongoing":
		return TodoStatusInProgress
	case "cancelled", "canceled", "abandoned", "skipped", "blocked":
		return TodoStatusCancelled
	default:
		return TodoStatusPending
	}
}

// EnforceSingleInProgress keeps at most one todo in_progress, demoting earlier
// ones to completed (the same contract update_plan uses for plans).
func EnforceSingleInProgress(items []TodoItem) []TodoItem {
	last := -1
	count := 0
	for i := range items {
		if items[i].Status == TodoStatusInProgress {
			count++
			last = i
		}
	}
	if count <= 1 {
		return items
	}
	out := append([]TodoItem(nil), items...)
	for i := range out {
		if i != last && out[i].Status == TodoStatusInProgress {
			out[i].Status = TodoStatusCompleted
		}
	}
	return out
}

// formatTodoList renders the todo list for tool output.
func formatTodoList(items []TodoItem) string {
	if len(items) == 0 {
		return "Todo list is empty."
	}
	lines := make([]string, 0, len(items))
	for index, item := range items {
		line := fmt.Sprintf("%d. [%s] %s", index+1, item.Status, item.Content)
		if item.Priority != "" {
			line += fmt.Sprintf(" (priority: %s)", item.Priority)
		}
		if item.Notes != "" {
			line += "\n   Notes: " + item.Notes
		}
		lines = append(lines, line)
	}
	return "Current Todo:\n" + strings.Join(lines, "\n")
}

// ===== persistence =====

// inProcessSessionStore is the fallback SessionStore when the CLI does not wire
// a real one. It is keyed by session ID and lives for the process lifetime.
type inProcessSessionStore struct {
	mu    sync.Mutex
	store map[string]map[string]any
}

func newInProcessSessionStore() *inProcessSessionStore {
	return &inProcessSessionStore{store: make(map[string]map[string]any)}
}

func (s *inProcessSessionStore) ReadMetadata(id string) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := make(map[string]any, len(s.store[id]))
	for k, v := range s.store[id] {
		copy[k] = v
	}
	return copy, nil
}

func (s *inProcessSessionStore) WriteMetadata(id string, meta map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := make(map[string]any, len(meta))
	for k, v := range meta {
		copy[k] = v
	}
	s.store[id] = copy
	return nil
}

var fallbackSessionStore SessionStore = newInProcessSessionStore()

func sessionStoreFor(options RunOptions) SessionStore {
	if options.SessionStore != nil {
		return options.SessionStore
	}
	return fallbackSessionStore
}

func readTodos(options RunOptions, sessionID string) ([]TodoItem, error) {
	if sessionID == "" {
		return nil, nil
	}
	store := sessionStoreFor(options)
	meta, err := store.ReadMetadata(sessionID)
	if err != nil {
		return nil, err
	}
	raw, ok := meta[stateTodoKey]
	if !ok || raw == nil {
		return nil, nil
	}
	var items []TodoItem
	if bytes, isBytes := raw.([]byte); isBytes {
		if err := json.Unmarshal(bytes, &items); err != nil {
			return nil, err
		}
	} else {
		reencoded, err := json.Marshal(raw)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(reencoded, &items); err != nil {
			return nil, err
		}
	}
	return items, nil
}

func writeTodos(options RunOptions, sessionID string, items []TodoItem) error {
	if sessionID == "" {
		return nil
	}
	store := sessionStoreFor(options)
	meta, err := store.ReadMetadata(sessionID)
	if err != nil {
		return err
	}
	if meta == nil {
		meta = map[string]any{}
	}
	meta[stateTodoKey] = items
	return store.WriteMetadata(sessionID, meta)
}

// ===== tools =====

// NewTodoReadTool builds the todo_read tool: read the session's todo list.
func NewTodoReadTool() Tool {
	return todoReadTool{baseTool: baseTool{
		name:         "todo_read",
		description:  "Read the current session todo list. Use proactively to track multi-step work.",
		parameters:   SpecsToSchema(nil),
		safety:       readOnlySafety("Reads session todo list state only."),
		capabilities: ToolCapabilities{Effect: EffectReadOnly, ThreadSafe: true, ResourceKeys: sessionResourceKeys},
	}}
}

type todoReadTool struct {
	baseTool
}

func (tool todoReadTool) Run(ctx context.Context, args map[string]any) Result {
	return tool.RunWithOptions(ctx, args, RunOptions{})
}

func (tool todoReadTool) RunWithOptions(_ context.Context, args map[string]any, options RunOptions) Result {
	if options.SessionID == "" {
		return errorResult("Error: todo_read requires a session context")
	}
	items, err := readTodos(options, options.SessionID)
	if err != nil {
		return errorResult("Error reading todos: " + err.Error())
	}
	return okResult(formatTodoList(items))
}

func todoWriteSpecs() []*ArgSpec {
	return []*ArgSpec{
		{
			Name:        "todos",
			Kind:        ArgObjectSlice,
			Required:    true,
			Description: "Full replacement todo list.",
			Items: &ArgSpec{
				Kind: ArgObject,
				Properties: []*ArgSpec{
					{Name: "content", Kind: ArgString, Required: true, Description: "The task description."},
					{Name: "status", Kind: ArgString, Enum: []string{TodoStatusPending, TodoStatusInProgress, TodoStatusCompleted, TodoStatusCancelled}, Description: "Status of the item."},
					{Name: "priority", Kind: ArgString, Description: "Optional priority label."},
					{Name: "notes", Kind: ArgString, Description: "Optional notes."},
				},
			},
		},
	}
}

// NewTodoWriteTool builds the todo_write tool: replace the session's todo list.
func NewTodoWriteTool() Tool {
	return todoWriteTool{baseTool: baseTool{
		name:         "todo_write",
		description:  "Replace the session todo list with a new one. Each item needs content; status defaults to pending; at most one item may be in_progress.",
		parameters:   SpecsToSchema(todoWriteSpecs()),
		safety:       promptSafety(SideEffectWrite, "Updates the session todo list."),
		capabilities: ToolCapabilities{Effect: EffectInteractive, ThreadSafe: false, ResourceKeys: sessionResourceKeys},
	}}
}

type todoWriteTool struct {
	baseTool
}

func (tool todoWriteTool) Run(ctx context.Context, args map[string]any) Result {
	return tool.RunWithOptions(ctx, args, RunOptions{})
}

func (tool todoWriteTool) RunWithOptions(_ context.Context, args map[string]any, options RunOptions) Result {
	if options.SessionID == "" {
		return errorResult("Error: todo_write requires a session context")
	}
	parsed, err := ParseArgs(todoWriteSpecs(), args)
	if err != nil {
		return errorResult("Error: Invalid arguments for todo_write: " + err.Error())
	}
	rawList := parsed["todos"].([]map[string]any)
	items := make([]TodoItem, 0, len(rawList))
	for _, object := range rawList {
		item := TodoItem{
			Content:  object["content"].(string),
			Status:   firstStringKey(object, "status"),
			Priority: firstStringKey(object, "priority"),
			Notes:    firstStringKey(object, "notes"),
		}
		items = append(items, item)
	}
	for i := range items {
		items[i].Status = NormalizeTodoStatus(items[i].Status)
	}
	items = EnforceSingleInProgress(items)
	if err := writeTodos(options, options.SessionID, items); err != nil {
		return errorResult("Error writing todos: " + err.Error())
	}
	return okResult(formatTodoList(items))
}
