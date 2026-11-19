# Design decisions

Each entry says what we chose, why, what we rejected, and what it costs. Add new
entries at the bottom. Don't rewrite old ones.

## 1. A run ends when the model calls `finish`, not when it stops calling tools

**The problem.** The agent loop used to end a run the moment the model replied
with text and no tool call. Weak models do this all the time in the middle of a
task. They write "Next step: build the six stations. Doing that now." and then
call nothing. The loop saw "text, no tool" and stopped. The user had to type
"continue" again and again. In session `kaji_20261109083127_73d2115f` this
happened 10 times, and every time the todo list still had open items.

**What we chose.** Basically, the model has to say "I am done" on purpose. In
the app (TUI) and in ACP (the editor connection), a run ends in one of two ways:

- the model calls `finish` with a summary, or
- the model calls `ask_user` because it needs the user.

A text-only reply in the middle of a task does not end the run. The loop adds an
automatic message ("you did not call a tool: act, ask, or finish") and asks the
model again.

**Why.** We cloned and read the real code of Cline, Roo Code, SWE-agent,
opencode, Codex, Gemini CLI and Goose. The pattern that works is the same
everywhere: the working model declares completion with a tool, and a text-only
reply gets a short automatic reminder with a small cap. Roo Code
(`attempt_completion`), SWE-agent (`submit`), Cline's yolo mode
(`submit_and_exit`), Gemini CLI sub-agents (`complete_task`) and Goose recipes
(`final_output`) all do this.

**What we rejected.**

- *Reading the wording of the last sentence* ("next I will", "doing that now").
  It is a hard-coded guess. It misses new phrasings and can fire on a normal
  answer. Nobody ships it.
- *A second model as a judge.* Gemini CLI had one for normal chat and turned it
  off by default: the model started "talking to itself", and it caused infinite
  loops in ACP mode. opencode and OpenHands only use a judge inside an opt-in
  `/goal` mode. It also costs an extra model call on a big context.
- *Only a stronger prompt* (Codex style). Nothing enforces it.

**Trade-offs.**

- The model can still call `finish` too early. The tool stops "I forgot to keep
  going". It does not stop "I say done but I am not".
- A model that never learns to call `finish` costs up to 3 extra model calls
  before the run stops.

## 2. A plain question still ends with a plain answer

**What we chose.** If the run used no tools and no todo items are open, a text
reply ends the run like before.

**Why.** "What does this function do?" should cost one model call. Roo Code
forces even this through its finish tool and pays an extra call. Cline answers
simple questions directly. We follow Cline here.

**Trade-off.** If the user types "continue", the todo list is empty, and the
model answers "ok, continuing" without a tool call, the run ends. The check
reads only two facts (tool calls in this run, open todo items), so it cannot see
that case.

**Update:** this exception was removed. See entry 8.

## 3. Reminder cap: 3 in a row, reset by any tool call

**What we chose.** `maxNoToolReminders = 3`. Any tool call resets the count.
After the cap the run stops and the user sees why (`Result.StopNotice`).

**Why.** Roo Code and SWE-agent both use 3. Cline's yolo mode and Goose's
`final_output` have no cap of their own and can spin until the turn limit. A
count per run (the old headless gate) would run out on a long task, so the count
resets when the model does real work.

## 4. `finish` is checked before it is accepted, without reading any wording

**What we chose.** `finish` is refused when:

- the summary is empty (Gemini CLI does this),
- it is in the same reply as another tool call (Gemini CLI and Goose do this:
  the model must see its other results first),
- todo items are still open, but only the first time (Roo Code has this as a
  setting),
- the last edits introduced a new error in diagnostics.

**Why "only the first time" for todo items.** A todo list can be stale. The
model gets one push to finish or update the list. If it calls `finish` again, we
accept it. The model has the last word.

## 5. Only interactive runs use the rule

**What we chose.** `Options.RequireFinish` is set by the TUI and by ACP only.
Headless `kajicode exec`, sub-agents and plan mode keep their old endings.
`finish` is not even shown to the model in those runs.

**Why.** Headless `exec` already has its own completion gate with exit codes
that scripts depend on. Sub-agents return their last text as the result. Plan
mode ends with a plan or `exit_plan_mode`. Changing all of them at once is a
bigger and riskier change.

**Trade-off.** Two completion rules live in the loop for now: the finish rule
(interactive) and the completion gate (headless).

## 6. Bigger turn limits

**What we chose.** The default tool-turn limit went from 80 to 300. The ceiling
went from 500 to 5000 (`internal/config/resolver.go`).

**Why.** Long build-and-verify tasks hit 80 and 100 turns in the middle of the
work. When a run does hit the limit, the user now sees a line that says so and
how to raise it.

## 7. `kajicode upgrade` checks what npm really installed

**The problem.** Right after the 0.8.6 release, `kajicode upgrade` printed
"updated via npm to 0.8.6", but `kajicode -v` still said 0.8.5. npm was not
serving the new packages yet, so `npm install -g ...@latest` installed 0.8.5
again and exited 0. The upgrade command printed the version it had looked up,
not the version that got installed.

**What we chose.** After `npm install`, the upgrade runs the installed binary
with `--version` and compares the answer with the version it expected
(`verifyNpmUpdate` in `internal/update/npm_update.go`). If they differ, the
upgrade fails with a message that names both versions and says to try again in
a few minutes.

**Why run the binary** instead of reading `package.json`. The binary is the
thing the user runs. The check also catches the case where npm installed the new
wrapper package but not the platform binary.

**Trade-off.** One extra process start per upgrade. It takes a few milliseconds.

## 8. The plain-chat exception is removed; todo items come from the history

**The problem.** Entry 2 let a text-only reply end the run when the run had used
no tools and no todo items were open. The point was to keep "what does this
function do?" at one model call. But it broke the real case. Every "continue"
starts a new run, and a new run has used zero tools. So when the model started a
run with "I'll fix both, then run the tests." and nothing else, the loop saw
"no tools used" and ended the run. That is exactly the stop we were trying to
remove.

We saw it in session `kaji_20261119041549_1e956dfa`. Three runs stopped this way.
Each made one model request and ended. The finish rule was live: an earlier run
in the same session ended correctly through `finish`.

The second check had a hole too. Open todo items were read from the todo_write
tool's in-memory copy. A restarted kajicode has an empty copy, and nothing
restores it on resume. The session had 3 of 5 items open and the rule could not
see them. (We also found that the TUI does not wire a durable todo store, so
"read the saved list" would not have worked there. The conversation history does
carry the list.)

**What we chose.**

- A text-only reply never ends an interactive run. The first reply of a run gets
  the reminder like any other.
- Open todo items are read from the newest `todo_write` call in the conversation
  history (`openTodoCount`). The in-memory list is only a fallback.

**Why.** Basically, "how many tools did this run use" and "does the reply sound
unfinished" are both guesses. The model's own `finish` call is the one real
signal. The history is the one source that is the same before and after a
restart.

**What we rejected.**

- *Keep the exception but count tools across the whole session.* It still guesses.
  A session whose last task is done would be treated as unfinished, or the other
  way round, depending on what the count says.
- *Wire a durable todo store into the TUI.* It is a bigger change in two
  packages, and the history already has the list.

**Trade-offs.**

- A plain question costs one extra model call at worst. It can answer inside
  `finish` and stay at one call; the prompt says so. If the model answers in
  text first, the user may see the answer twice (the text, then the summary).
  We did not tell the model to shrink the summary to one line when it already
  answered in text: ACP saves `Result.FinalAnswer` as the assistant turn in its
  history, so a one-line summary would replace the real answer there.
- In an ACP client that cannot show questions, `ask_user` only answers "assume
  something". There the reminder and the prompt send a real question out in the
  `finish` summary, so the user can answer it.
- The loop does not check whether the model can call tools. A model without tool
  support would be reminded 3 times and then stopped with the notice. Such a
  model could not do coding work anyway.
- After a compaction, the history may no longer hold the `todo_write` call. Then
  the check falls back to the in-memory list, which a restart empties. The
  reminder still fires, because it no longer depends on the todo count. Only the
  "refuse `finish` once while items are open" push is lost.
