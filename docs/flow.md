# Flows

Step-by-step walks through how things work: which file and function handles each
step, the edge cases, what the user sees, and how to recover. Add new flows at
the bottom.

## How a run ends (finish tool)

This is for runs in the app (TUI) and over ACP (the editor connection). Both set
`Options.RequireFinish` (`internal/cli/app.go`, `internal/acp/agent.go`).

### The normal path

1. The user sends a message. `agent.Run` in `internal/agent/loop.go` starts the
   loop. Each pass asks the model once.
2. At the start, `finishUsable` (`internal/agent/finish.go`) decides if the rule
   is on. It is on when `RequireFinish` is set, plan mode is off, and the
   `finish` tool is registered and not filtered out.
3. The system prompt gets an "Ending A Task" section (`finishContext`). The tool
   list includes `finish` (`partitionToolsCached` in
   `internal/agent/tool_partition.go`).
4. The model works: it calls tools, the loop runs them, and asks again.
5. When the model thinks the task is done, it calls `finish` with a `summary`.
6. The loop does not execute `finish` like a normal tool. `finishState.evaluate`
   checks the call first.
7. If the call is accepted, the loop sends the summary through `OnText` (so the
   app shows it and saves it like any final answer), sets `Result.FinalAnswer`
   to the summary, and returns. The run is over.

### The model replies with text and no tool call

This is the case the whole feature exists for. The check is in the no-tool
branch of the loop in `loop.go`.

- **The run used no tools and no todo items are open.** This is a plain answer
  to a plain question. The run ends with that text. One model call.
- **The run already used a tool, or todo items are open.** The loop adds
  `noToolReminder` as a user message and asks the model again. The reminder
  says: do the next step with a tool, or call `ask_user`, or call `finish`. If
  todo items are open, it says how many. The user does not see the reminder.
  They see the model keep working.
- **The model ignores the reminder.** It gets up to 3 reminders in a row
  (`maxNoToolReminders`). Any tool call sets the count back to 0.
- **The model ignores 3 reminders.** The run stops on the 4th text-only reply.
  The user sees that last reply, and under it: "Stopped: the model replied 4
  times in a row without calling a tool or finish. Send a message to continue."
  Recovery: send any message. Nothing is lost.

### `finish` is refused

A refused `finish` comes back to the model as a tool error. The run continues.
The user sees a failed `finish` row in the transcript.

- **Empty summary.** "finish needs a non-empty `summary`". The model calls it
  again with a summary.
- **`finish` next to another tool call in the same reply.** Refused, because the
  model has not seen the other result yet. The other tool still runs. The model
  calls `finish` alone in its next reply.
- **Todo items still open.** Refused once, with the number of open items. The
  model can finish them, or fix the list with `todo_write`, or call `finish`
  again. The second call is accepted even if items are still open.
- **The last edits broke something.** Before accepting, the loop waits for the
  diagnostics of this run's edits (`postEditDiagnostics.drainFinal`). If there
  is a new error, `finish` is refused and the error text goes to the model.

### The model needs the user

The model calls `ask_user`. This is unchanged: the app shows the question, the
loop waits, and the answer goes back to the model as the tool result. If the
model asks in plain text instead, it gets the reminder, which tells it to use
`ask_user`.

### The run hits the tool-turn limit

1. The loop runs out of turns (`maxTurns`).
2. It asks the model for a final summary with tools off
   (`finalAnswerAfterMaxTurns`).
3. `Result.StopNotice` is set to "Stopped: reached the tool-turn limit (N).
   Raise it with /turns <n>, or send a message to continue."
4. The TUI shows the notice as a system row under the summary
   (`internal/tui/model.go`). ACP sends it as a text chunk.

Recovery: type `/turns 1000` (the ceiling is 5000) or just send "continue". The
default limit is 300. It comes from `maxTurns` in the config file or
`KAJICODE_MAX_TURNS`.

### Runs where the rule is off

- **Headless `kajicode exec`.** It keeps its own completion gate
  (`RequireCompletionSignal`). `finish` is not in the tool list.
- **Sub-agents** (`internal/agents/childrun.go`). Their last text reply is the
  result, as before.
- **Plan mode.** The run ends with a plan in the reply, or with
  `exit_plan_mode`.
- **`finish` is disabled** with a tool filter. The rule turns itself off, so the
  model is never told to call a tool it cannot see.
- If a model calls `finish` in one of these runs anyway, the tool's own `Run`
  answers: "finish is not used in this run. Reply with your final answer as
  plain text to end it."

### Other stops that did not change

- The user cancels (Esc). The run stops at once. No reminder is sent.
- A provider error ends the run with the error.
- An empty reply (no text, no tool) has its own retry path and its own cap.
- The repeated-failure and repeated-identical-call guards still stop a run that
  loops on tool calls.
