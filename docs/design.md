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
