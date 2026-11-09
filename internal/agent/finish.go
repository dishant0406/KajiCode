package agent

import (
	"strconv"
	"strings"

	"github.com/dishant0406/KajiCode/internal/tools"
)

// Finish-tool completion (Options.RequireFinish). The model, not the harness,
// says when a task is done: it calls the finish tool. A text-only reply in the
// middle of a task gets a reminder instead of ending the run, so a model that
// writes "next I will do X" and then stops is pushed to do X.

// maxNoToolReminders bounds how many text-only replies IN A ROW get a reminder.
// Any tool call resets the count. Once spent, the run stops with a StopNotice
// instead of reminding forever.
const maxNoToolReminders = 3

// noToolReminderMarker is a stable substring for tests.
const noToolReminderMarker = "the run is still going"

// finishState is the small per-run state of the finish rule.
type finishState struct {
	noToolReplies int  // text-only replies in a row
	todoRefused   bool // finish was already refused once for open todo items
}

// finishRequired reports whether this run ends through the finish tool. Plan
// mode keeps its own ending (a plan in the reply, or exit_plan_mode).
func finishRequired(options Options) bool {
	return options.RequireFinish && !options.PlanMode
}

// finishUsable reports whether the model can actually call finish in this run.
// When the tool is missing or filtered out, the rule must be off, or the model
// would be reminded to call a tool it cannot see.
func finishUsable(registry *tools.Registry, options Options) bool {
	if !finishRequired(options) {
		return false
	}
	if _, ok := registry.Get(tools.FinishToolName); !ok {
		return false
	}
	return ToolAllowedByFilters(tools.FinishToolName, options.EnabledTools, options.DisabledTools)
}

// finishContext renders the system-prompt rules for the finish tool. It returns
// "" when the run does not require finish, keeping that prompt byte-identical.
func finishContext(options Options) string {
	if !finishRequired(options) {
		return ""
	}
	return "## Ending A Task\n\n" +
		"- A reply without a tool call does NOT end a task. While work remains, every reply must include a tool call.\n" +
		"- Never say you will do something and then stop. Do it with a tool call in the same reply.\n" +
		"- When the whole task is complete and checked, call " + tools.FinishToolName +
		" alone, with a summary for the user. This is the only way to end a task.\n" +
		"- If you need the user's input to continue, call ask_user. Do not ask in plain text and stop.\n" +
		"- A simple question that needs no tools can be answered directly in plain text."
}

// openTodoCount returns how many todo items are still pending or in progress,
// read from the todo_write tool's in-memory list. 0 when there is no list.
func openTodoCount(registry *tools.Registry) int {
	tool, ok := registry.Get(planToolName)
	if !ok {
		return 0
	}
	reader, ok := tool.(interface{ CurrentTodos() []tools.TodoItem })
	if !ok {
		return 0
	}
	open := 0
	for _, item := range reader.CurrentTodos() {
		if planStatusRemaining(item.Status) {
			open++
		}
	}
	return open
}

// noToolReminder is the automatic message sent after a text-only reply.
func noToolReminder(openTodos int) string {
	reminder := "You replied without calling a tool, so " + noToolReminderMarker + ". Do one of these now:\n" +
		"- If work remains, do the next step with a tool call.\n" +
		"- If you need the user's input, call ask_user.\n" +
		"- If the whole task is complete, call " + tools.FinishToolName + " with a summary.\n"
	if openTodos > 0 {
		reminder += "The todo list still has " + strconv.Itoa(openTodos) + " open item(s).\n"
	}
	return reminder + "(This is an automated message. Do not reply to it conversationally.)"
}

// noToolStopNotice is shown to the user when the reminders are spent.
func noToolStopNotice() string {
	return "Stopped: the model replied " + strconv.Itoa(maxNoToolReminders+1) +
		" times in a row without calling a tool or " + tools.FinishToolName + ". Send a message to continue."
}

// maxTurnsStopNotice is shown to the user when the run hits the tool-turn limit.
func maxTurnsStopNotice(maxTurns int) string {
	return "Stopped: reached the tool-turn limit (" + strconv.Itoa(maxTurns) +
		"). Raise it with /turns <n>, or send a message to continue."
}

// evaluate decides a finish call. It returns the tool result and, when the
// call is accepted, the non-empty summary that becomes the run's final answer.
// siblings is the number of tool calls in the same reply (finish included).
func (state *finishState) evaluate(call ToolCall, siblings int, openTodos int) (ToolResult, string) {
	var args struct {
		Summary string `json:"summary"`
	}
	_ = decodeToolArguments(call.Arguments, &args)
	summary := strings.TrimSpace(args.Summary)
	switch {
	case summary == "":
		return finishRefused(call, "finish needs a non-empty `summary` for the user."), ""
	case siblings > 1:
		return finishRefused(call, "finish must be the only tool call in a reply. "+
			"Read the results of your other calls first, then call finish alone."), ""
	case openTodos > 0 && !state.todoRefused:
		state.todoRefused = true
		return finishRefused(call, strconv.Itoa(openTodos)+" todo item(s) are still open. "+
			"Finish them, or update the list with todo_write if they are done or no longer needed. "+
			"Then call finish again."), ""
	}
	return ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
		Status:     tools.StatusOK,
		Output:     "Run finished.",
	}, summary
}

// finishRefused renders a refused finish call as a tool error the model can
// read and act on; the run continues.
func finishRefused(call ToolCall, reason string) ToolResult {
	return ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
		Status:     tools.StatusError,
		Output:     "Error: finish was not accepted: " + reason,
	}
}
