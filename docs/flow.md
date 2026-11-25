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

- **Any text-only reply, including the first reply of a run.** The loop adds
  `noToolReminder` as a user message and asks the model again. The reminder
  says: do the next step with a tool, or call `ask_user`, or call `finish`. If
  todo items are open, it says how many. The user does not see the reminder.
  They see the model keep working. The loop does not look at what the reply
  says, and it does not look at how many tools this run has used: every
  "continue" starts a new run with zero tools, so that number proves nothing.
- **A plain question.** The prompt tells the model to put its answer in the
  `finish` summary, so the run ends in one model call. If it answers in text
  first, it gets one reminder and then calls `finish`.
- **An ACP client that cannot show questions** (`OnAskUser` is nil). `ask_user`
  would only answer "assume something", so the reminder and the prompt tell the
  model to put a real question in the `finish` summary instead.
- **Open todo items** are read by `openTodoCount` (`internal/agent/finish.go`)
  from the newest `todo_write` call in the conversation history. After a restart
  or resume the history is replayed, so the items are still seen. If the history
  has none (compacted, or the run never planned), it uses the `todo_write`
  tool's in-memory list.
- **The model ignores the reminder.** It gets up to 3 reminders in a row
  (`maxNoToolReminders`). Any tool call sets the count back to 0.
- **The model ignores 2 reminders.** The next request asks the provider to
  require a tool call (`CompletionRequest.RequireToolCall`, set in `loop.go` when
  `finishState.wantsForcedToolCall` is true). OpenAI-style APIs get
  `tool_choice: "required"`, Anthropic gets `{"type":"any"}` (skipped while
  extended thinking is on), Gemini gets mode `ANY`. `finish` is a tool, so the
  model can still end the task. If the endpoint rejects it with an error, the
  loop asks again without it and does not force again in that run
  (`forceRejected`). Azure and the Codex Responses transport ignore the flag.
- **Every reminder is logged.** The loop calls `Options.OnHarnessNote`; the TUI
  writes a `harness_note` event to `events.jsonl` (`sessions.EventHarnessNote`).
  It is a log only. The history replay skips it, so a later run never sees it as
  a user message. ACP does not record notes.
- **The model alternates text with a repeated tool call.** The in-a-row count
  resets only on a tool turn that did new work (`noOp.Count == 0` in `loop.go`),
  so a repeated `todo_read` does not reset it. Text, repeat, text, repeat, text,
  repeat, text ends with the usual notice.
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

## `kajicode upgrade` for an npm install

1. `update.Apply` (`internal/update/apply.go`) finds the running binary and runs
   `DetectInstallMethod`. A binary under `node_modules` is an npm install.
2. For a plain npm install, `usesNpmRegistry` is true and `applyNpm`
   (`internal/update/npm_update.go`) takes over. It does not call the GitHub API.
   It runs `npm view @dishant0406/kajicode version --prefer-online` with the npm
   that belongs to the same Node version as the install, and reads the newest
   version from the answer.
3. If that version is not newer than the running one, the user sees
   "already up to date". Nothing is installed.
4. Otherwise `applyNpmUpdate` runs
   `npm install -g --prefix <prefix> @dishant0406/kajicode@latest`.
5. `verifyNpmUpdate` runs the installed binary with `--version` and reads the
   last word of the output.
6. If that word is the expected version, the user sees
   "[kajicode] updated via npm to X (old -> X)".

Other installs, and npm installs where the caller sets its own release source
(`--endpoint`, `--repository`, or `KAJICODE_UPDATE_RELEASE_URL`), use `Check`
against the GitHub releases API, then download, verify and replace the binary
(standalone) or run the same npm install (npm).

Edge cases:

- **GitHub is rate limited or down.** An npm install does not notice, because it
  never calls GitHub. A standalone install still fails with
  "github release check failed (403 Forbidden): GitHub API rate limit (limit 60)
  reached; retry in about 13m47s". Recovery: wait, or set
  `KAJICODE_UPDATE_RELEASE_URL`.
- **npm cannot answer the lookup.** The user sees
  "npm view @dishant0406/kajicode version: <error>". Nothing is installed.
  Recovery: `npm install -g @dishant0406/kajicode@latest`.
- **npm installed the old version again.** This happens for a few minutes after
  a release, while npm is not serving the new packages yet. The user sees
  "npm installed 0.8.5, not 0.8.6: the npm registry may not be serving 0.8.6
  yet, so run `kajicode upgrade` again in a few minutes". Recovery: wait and run
  it again.
- **The binary is gone or cannot run after the install.** The user sees
  "npm finished, but <path> could not be run to check its version". Recovery:
  `npm install -g @dishant0406/kajicode@latest`.
- **npm itself fails.** The user sees npm's own output and
  "npm install -g ...@latest: <error>".

### `/goal`: keep going across runs

Files: `internal/tui/goal.go`, `internal/tui/goal_judge.go`, the run-completion
handler in `internal/tui/model.go`, `cancelRun`, and `session_resume.go`.

1. The user types `/goal <objective>`. `handleGoalCommand` stores the goal and
   starts a run with the objective and the rule "call finish when done".
   `/goal` shows the status. `/goal stop` clears it. While a run is active it
   says to wait. In plan mode it refuses, because plan mode has no `finish` tool
   and the goal could never end.
2. Every change of the goal is saved as a `goal` event in the session
   (`recordGoal`). The first one is written after the run starts, because that is
   when the session is created.
3. When any run ends, the handler calls `advanceGoal` with the result. The line it
   returns is shown after the run's own rows, so the answer comes first.
4. The run ended with an error: the goal is cleared and the user sees how to
   start it again. If plan mode was turned on meanwhile, the goal is stopped for
   the same reason as step 1.
5. The run was not the goal's (a `/loop` iteration), the plan was approved, or the
   run only waits for the user (ask_user, a permission prompt): nothing changes.
6. The model called `finish` (`Result.Finished`): the reviewer checks it
   (`judgeGoalCmd`, one extra model call, 60 s limit). The user sees the answer,
   then the result arrives as a `goalJudgedMsg`:
   - "done": "Goal complete."
   - "continue": the user sees "Reviewer: the goal is not met yet: <reason>". It
     counts as a restart (step 7) and the next run carries the reason.
   - the reviewer fails or does not answer in JSON: the goal stays. The user sees
     why and can send `/goal resume` to try again or `/goal stop` to accept the
     work.
   - the user stopped or replaced the goal meanwhile: the result is ignored (it
     carries the goal it was for).
7. Otherwise the harness ended the run. The restart count goes up. A run that did
   no new work (`Result.MadeProgress` is false) counts as a stall. At 3 stalls in a
   row, or 50 restarts, the goal is stopped (cleared) and the user sees why. Else
   the next run is queued with "Continue working toward the goal".
8. A message the user already queued goes first. The goal restarts after it.
9. Esc while a run is active (`cancelRun`) clears the goal, unless the run is a
   `/loop` iteration. Ctrl+C while idle does not. `/new` clears it in the window.
   A failed `/resume` leaves it alone.
10. `/resume` reads the last `goal` event of the session (`goalFromEvents`) and
    shows "Goal restored: ...". It does not start a run. `/goal resume` starts the
    next one, and the restart count continues from where it was.

## Listing OpenCode models (Zen, Go)

1. The picker or `kajicode providers models` calls `DiscoverCatalog`
   (`internal/providermodeldiscovery/discovery.go`).
2. It downloads the models.dev catalog (`FetchModelsDev` in
   `internal/providermodelcatalog/remote.go`). The file is about 7 MB. The cap is
   64 MB.
3. It also asks the provider for its live list (`/models`, needs the API key).
4. `mergeLiveModels` keeps live models that are also in the catalog and are
   coding models. With `ShowAllModels` on, `unionLiveModels` keeps all of them.
5. At run time, `modelProtocol` in `internal/providers/factory.go` reads the
   models.dev `npm` value. Anthropic gives `/messages`, OpenAI gives
   `/responses`, and the rest use chat completions. Every OpenCode request also
   carries the `x-opencode-session` header.

Edge cases:

- The catalog download fails or is cut off: the picker uses a name guess on the
  live list. Some models are hidden. Turn on `ShowAllModels` to see all of them.
- No API key: the live list is skipped and the catalog list is shown.
- Gemini models on OpenCode (`interactions` API): hidden from the catalog,
  because KajiCode has no client for that API. `ShowAllModels` can still list
  them, and a request to them fails.
