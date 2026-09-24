package acp

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/dishant0406/KajiCode/internal/agent"
	"github.com/dishant0406/KajiCode/internal/tools"
)

// translate.go maps KAJICODE's agent events onto ACP session/update payloads. The
// mapping functions are pure (no I/O) so they can be unit-tested directly; the
// notifier wires them to a live JSON-RPC connection.

func agentMessageChunk(delta string) ContentChunk {
	return ContentChunk{SessionUpdate: UpdateAgentMessageChunk, Content: TextBlock(delta)}
}

func agentThoughtChunk(delta string) ContentChunk {
	return ContentChunk{SessionUpdate: UpdateAgentThoughtChunk, Content: TextBlock(delta)}
}

// userMessageChunk replays a stored user turn during session/load.
func userMessageChunk(text string) ContentChunk {
	return ContentChunk{SessionUpdate: UpdateUserMessageChunk, Content: TextBlock(text)}
}

// toolKindFor maps a KAJICODE tool name to the closest ACP ToolKind so editors can
// pick an icon/affordance. Unknown tools fall back to "other".
func toolKindFor(name string) string {
	switch name {
	case "read_file", "read_minified_file", "list_directory":
		return ToolKindRead
	case "glob", "grep":
		return ToolKindSearch
	case "write_file", "edit_file", "apply_patch":
		return ToolKindEdit
	case "bash", "exec_command", "write_stdin":
		return ToolKindExecute
	case "web_fetch", "web_search":
		return ToolKindFetch
	case "todo_write":
		return ToolKindThink
	default:
		return ToolKindOther
	}
}

// toolTitle builds a concise human title, e.g. "read_file src/main.go".
func toolTitle(name, rawArgs string) string {
	if hint := primaryArgHint(rawArgs); hint != "" {
		return name + " " + hint
	}
	return name
}

// primaryArgHint extracts the most relevant argument (path/pattern/command) from
// raw JSON arguments. Best-effort; returns "" when it can't parse.
func primaryArgHint(rawArgs string) string {
	if strings.TrimSpace(rawArgs) == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(rawArgs), &m); err != nil {
		return ""
	}
	for _, key := range []string{"path", "file_path", "pattern", "query", "command", "url", "cwd"} {
		if v, ok := m[key].(string); ok && strings.TrimSpace(v) != "" {
			return truncateHint(v)
		}
	}
	return ""
}

func truncateHint(s string) string {
	s = strings.TrimSpace(s)
	const max = 60
	if utf8.RuneCountInString(s) > max {
		return string([]rune(s)[:max]) + "…"
	}
	return s
}

// rawInput returns the tool arguments as a raw JSON object when they parse, else
// nil (so a malformed/empty arg string never produces invalid JSON on the wire).
func rawInput(args string) json.RawMessage {
	if strings.TrimSpace(args) == "" || !json.Valid([]byte(args)) {
		return nil
	}
	return json.RawMessage(args)
}

// toolCallStart maps an advertised KAJICODE tool call to the initial ACP "tool_call"
// update (status in_progress — KAJICODE executes immediately after advertising).
func toolCallStart(call agent.ToolCall) ToolCallUpdate {
	return ToolCallUpdate{
		SessionUpdate: UpdateToolCall,
		ToolCallID:    call.ID,
		Title:         toolTitle(call.Name, call.Arguments),
		Kind:          toolKindFor(call.Name),
		Status:        ToolStatusInProgress,
		RawInput:      rawInput(call.Arguments),
	}
}

// toolCallResult maps a finished KAJICODE tool result to a "tool_call_update".
func toolCallResult(result agent.ToolResult) ToolCallUpdate {
	status := ToolStatusCompleted
	if result.Status == tools.StatusError {
		status = ToolStatusFailed
	}
	upd := ToolCallUpdate{
		SessionUpdate: UpdateToolCallUpdate,
		ToolCallID:    result.ToolCallID,
		Status:        status,
	}
	if content := toolResultContent(result); len(content) > 0 {
		upd.Content = content
	}
	if locs := toolResultLocations(result); len(locs) > 0 {
		upd.Locations = locs
	}
	return upd
}

func toolResultContent(result agent.ToolResult) []ToolCallContent {
	text := strings.TrimRight(result.Output, "\n")
	if text == "" {
		text = result.Display.Summary
	}
	if text == "" {
		return nil
	}
	return []ToolCallContent{ToolContent(TextBlock(text))}
}

func toolResultLocations(result agent.ToolResult) []ToolCallLocation {
	locs := make([]ToolCallLocation, 0, len(result.ChangedFiles))
	for _, f := range result.ChangedFiles {
		if strings.TrimSpace(f) == "" {
			continue
		}
		locs = append(locs, ToolCallLocation{Path: f})
	}
	return locs
}

// planUpdate maps KAJICODE's plan items to an ACP "plan" update.
func planUpdate(items []tools.PlanItem) PlanUpdate {
	entries := make([]PlanEntry, 0, len(items))
	for _, it := range items {
		entries = append(entries, PlanEntry{
			Content:  it.Content,
			Priority: PlanPriorityMedium,
			Status:   planStatusToACP(it.Status),
		})
	}
	return PlanUpdate{SessionUpdate: UpdatePlan, Entries: entries}
}

// planStatusToACP maps KAJICODE's plan status (pending/in_progress/completed/failed)
// to ACP's PlanEntryStatus (which has no "failed"; a failed step is terminal, so
// it maps to completed).
func planStatusToACP(s string) string {
	switch s {
	case "in_progress":
		return PlanStatusInProgress
	case "completed", "failed":
		return PlanStatusCompleted
	default:
		return PlanStatusPending
	}
}

// promptText concatenates the text of an inbound prompt. Baseline text and
// resource_link blocks are supported; an embedded resource contributes its URI
// and any inline text, so a context-only prompt is not silently dropped.
func promptText(blocks []ContentBlock) string {
	var b strings.Builder
	for _, blk := range blocks {
		switch blk.Type {
		case "text":
			b.WriteString(blk.Text)
		case "resource_link":
			writeResourceText(&b, blk.URI, "")
		case "resource":
			uri, text := resourceFields(blk.Resource)
			writeResourceText(&b, uri, text)
		}
	}
	return b.String()
}

func writeResourceText(b *strings.Builder, uri, text string) {
	if uri == "" && text == "" {
		return
	}
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	if uri != "" {
		b.WriteString("<resource>")
		b.WriteString(uri)
		b.WriteString("</resource>")
	}
	if text != "" {
		if uri != "" {
			b.WriteString("\n")
		}
		b.WriteString(text)
	}
}

// resourceFields extracts the uri and inline text from an embedded resource
// block. Unknown or malformed shapes yield empty strings.
func resourceFields(raw json.RawMessage) (string, string) {
	if len(raw) == 0 {
		return "", ""
	}
	var res struct {
		URI  string `json:"uri"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return "", ""
	}
	return res.URI, res.Text
}

// notifier sends translated updates over a connection for one session.
type notifier struct {
	conn      *Conn
	sessionID string
}

func (n *notifier) send(update any) {
	_ = n.conn.Notify(MethodSessionUpdate, SessionNotification{SessionID: n.sessionID, Update: update})
}

func (n *notifier) text(delta string) {
	if delta != "" {
		n.send(agentMessageChunk(delta))
	}
}

func (n *notifier) thought(delta string) {
	if delta != "" {
		n.send(agentThoughtChunk(delta))
	}
}

// userText replays a stored user message chunk (session/load history).
func (n *notifier) userText(text string) {
	if text != "" {
		n.send(userMessageChunk(text))
	}
}

func (n *notifier) toolCall(call agent.ToolCall)       { n.send(toolCallStart(call)) }
func (n *notifier) toolResult(result agent.ToolResult) { n.send(toolCallResult(result)) }

func (n *notifier) plan(items []tools.PlanItem) {
	if len(items) > 0 {
		n.send(planUpdate(items))
	}
}

func (n *notifier) currentMode(modeID string) {
	n.send(CurrentModeUpdate{SessionUpdate: UpdateCurrentMode, CurrentModeID: modeID})
}

func (n *notifier) configOptions(options []SessionConfigOption) {
	if len(options) > 0 {
		n.send(ConfigOptionUpdate{SessionUpdate: UpdateConfigOption, ConfigOptions: options})
	}
}

func (n *notifier) availableCommands(commands []AvailableCommand) {
	n.send(AvailableCommandsUpdate{SessionUpdate: UpdateAvailableCommands, AvailableCommands: commands})
}

func (n *notifier) sessionInfo(title string) {
	if title != "" {
		n.send(SessionInfoUpdate{SessionUpdate: UpdateSessionInfo, Title: title})
	}
}

func (n *notifier) usage(used, size int) {
	n.send(UsageUpdate{SessionUpdate: UpdateUsage, Used: used, Size: size})
}
