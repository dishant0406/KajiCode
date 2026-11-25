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

## 9. An npm install asks npm, not GitHub, which version is newest

**The problem.** `kajicode upgrade` failed with "github release check failed (403
Forbidden): GitHub API rate limit (limit 60) reached; retry in about 13m47s".
The command asked the GitHub releases API for the newest version before it ran
`npm install -g`. That API allows 60 requests an hour per network address when
you are not logged in, and something else on the machine had used them up. An npm
install does not need GitHub at all.

**What we chose.** For a plain npm install, `applyNpm` runs
`npm view @dishant0406/kajicode version --prefer-online` and compares the answer
with the running version. It installs only when npm's version is newer, then
runs the check from entry 7. Standalone installs still use the GitHub API,
because they download the release archive from GitHub anyway.

**Why npm's answer.** The install is done by npm, so npm is the one source that
agrees with what the install will get. If GitHub says 0.8.7 exists but the npm
registry has not caught up, the user now sees "already up to date" for what npm
can install, not an install of the old version with a "newer exists" message.
`--prefer-online` skips npm's cached package info, which can be minutes old right
after a release.

**What we rejected.**

- *Send a GitHub token with the request.* It would raise the limit to 5,000 an
  hour, but it needs a token on every machine, and the GitHub answer still does
  not match what npm will install.
- *Run `npm install -g @latest` first and compare after.* It touches the install
  on every run, even when it is already current.

**Trade-offs.**

- A caller that sets its own release source (`--endpoint`, `--repository`, or
  `KAJICODE_UPDATE_RELEASE_URL`) keeps the GitHub path, so those options still
  mean what they said.
- `kajicode update --check` still uses the GitHub API, so it can still hit the
  rate limit. We did not change it.
- The lookup starts one extra npm process, which takes about a second.

## 10. After two ignored reminders, require a tool call

**What we chose.** The third request after two text-only replies carries
`RequireToolCall`. Each provider maps it to its own "must call a tool" setting.
If the endpoint rejects it, the loop retries once without it and stops forcing
for that run. Every reminder is also written to the session log as a
`harness_note` event.

**Why.** Session `kaji_20261119041549_1e956dfa` ended with the model
(`fireworks/kimi_k3.1_pro` through a gateway) answering all three reminders with
another "Next I will..." sentence. Reminders ask. A required tool call does not
leave the model a choice. The model can still call `finish`, so the rule that the
model, not the harness, decides when the task is done still holds. We read no
wording to decide any of this.

**What we rejected.**

- *Force a tool call from the first text-only reply.* Most models obey the first
  reminder. Forcing early would cost compatibility for no gain.
- *Check the reply for "next step" wording to decide when to force.* The user
  ruled out wording checks.

**Trade-offs.**

- Some gateways and thinking models reject the setting. That costs one failed
  request, then the run goes on unforced. A model that cannot call tools at all
  still ends with the "Stopped" notice after 3 reminders.
- Anthropic with extended thinking, Azure, and the Codex Responses transport do
  not get the setting.
- `harness_note` events add a few lines to `events.jsonl`. They are never sent
  back to the model.

## 11. Only new work clears the reminder count

**What we chose.** The "text-only replies in a row" count (`finishState.noToolReplies`)
used to reset on any tool call. Now it resets only on a tool turn that did new
work: a call the run has not made before, or a call that changed a file. That is
the existing no-op guard's answer (`observeNoOpToolCalls`, `Count == 0`). A call
now also counts as a repeat when only its argument order differs (`toolCallKey`).

**Why.** Session `kaji_20261119041549_1e956dfa` had runs with 54 and 18
reminders and no `finish`. The model said "I'll do X", called `todo_read` again,
said it again, and so on. The cheap call reset the count every time, so the
3-in-a-row cap never fired. Now a repeated `todo_read` or a re-read of the same
file range does not reset it, and the run stops with the usual notice. The same
fix keeps the forced tool call (decision 10) alive, because the count no longer
falls back to 0 after one cheap call.

**What we rejected.**

- *A cap on the total reminders per run.* We built it first. It would also stop a
  run that makes real edits while narrating a lot, which is the run we want.
- *Treat a read with a different line range as a repeat.* Paging through a long
  file would trip the guard.
- *Judge whether a call "made progress".* That is a guess about meaning.

**Trade-offs.**

- A model that varies its arguments a little (a new range every time) still
  counts as doing new work. Only the guard thresholds and `maxTurns` bound that.
- The count still resets when the user sends a message. A new message is a new
  decision by the user.

## 12. The reply that earned a reminder is not replayed

**What we chose.** In the TUI, the text-only reply that triggers a reminder is
saved inside the `harness_note` event (`reply`), not as an assistant message.
`ModelMessagesFromEvents` skips notes, so the next run does not see it.

**Why.** On the next "continue" the history had many "I'll do X" assistant
messages and none of the corrections, so the model saw itself narrating and never
being corrected. Keeping the reply in the note still lets us read what the model
said when we debug.

**Trade-offs.** A resumed session shows fewer chatty lines. If a model puts a real
answer in text and then calls `finish`, the answer lives in the finish summary,
which is saved as usual. ACP does not record notes, so it is unchanged.

## 13. `/goal`: the harness restarts a stopped run

**What we chose.** `/goal <objective>` starts a run. When a run ends and the model
did not call `finish` (`Result.Finished` is false), `advanceGoal`
(`internal/tui/goal.go`) queues the next run through `queuedMessage`. The goal
ends on `finish` (after the reviewer agrees, see 14), an error, a cancelled run,
`/goal stop`, 3 restarts in a row where the run did no new work
(`Result.MadeProgress`, the same signal that clears the reminder count), or 50
restarts.

**Why.** A user should not have to type "continue" again and again. The DeepSeek
Harness (goal driver on idle) and opencode (`/goal` plugin) both do this and
neither ends a run on a guess about the reply. The model ends the goal itself
with `finish`.

**What we rejected.**

- *The existing self-paced `/loop`.* It ends on a text line (`LOOP: DONE`), which
  is a wording check, and it is built for polling.
- *A `blocked` tool.* `ask_user` and `finish` already cover it.

**Trade-offs.** `/new`, `/resume` and plan mode end the goal in the window where it
runs (the saved state, see 14, is what `/resume` brings back).

## 14. A reviewer checks `finish` during a goal, and the goal is saved

**What we chose.** Two things.

- *Reviewer.* When the model calls `finish` during a goal, `judgeGoalCmd`
  (`internal/tui/goal_judge.go`) makes one separate model call, with the same
  provider. It gets the objective, the closing summary, and the last 30 tool
  results across all the goal's runs (the first 60 and the last 100 characters of
  each, because a test prints its verdict last). It answers one line of JSON,
  `{"verdict":"done"|"continue","reason":...}`. "done" completes the goal.
  "continue" counts as a restart and sends the model back with the reason. The
  reviewer's prompt says the model's claim is not evidence and to answer
  "continue" when in doubt.
- *Saving.* Every change of the goal is written to the session as a `goal` event
  (`recordGoal`). `/resume` reads the last one (`goalFromEvents`), shows "Goal
  restored", and waits. `/goal resume` starts the next run.

**Why.** A weak model can call `finish` too early, and one early `finish` ended
the goal for good. A reviewer that sees the real tool calls is the "judge on
idle" that opencode's `/goal` plugin uses, and the user asked for the model, not a
wording check, to judge. A goal that lives only in memory is lost when the app
closes, and the long runs a goal exists for are the ones that get interrupted.

**What we rejected.**

- *Refuse the first `finish` and accept the second.* A weak model just calls it
  twice. It checks nothing.
- *Restart the run on resume.* A model run costs money, so the user chooses when.
- *Keep the reviewer on for plain runs.* Only goals ask for "don't stop until
  done". A plain run ends on the user's next message anyway.

**Trade-offs.**

- Each `finish` in a goal costs one more request. The reviewer is also a model and
  can be wrong. It can reject good work, so rejections count toward the stall and
  round limits.
- If the reviewer fails (a rate limit, a timeout) or does not answer in JSON, the
  goal is not completed. It stays, the user sees why, and `/goal resume` tries
  again or `/goal stop` accepts the work. We first accepted the `finish` on a
  failure, but then one 429 would end an unattended goal, which is the early stop
  the reviewer exists to prevent. A reviewer that never answers in JSON can trap a
  goal until the user steps in.
- The reviewer sees only a short slice of tool output, not the files. The slice is
  kept in memory, so after a `/resume` the first check sees only the new runs.
- A verdict belongs to one goal (`goalJudgedMsg.goal`). If the goal was stopped or
  replaced while the reviewer worked, the verdict is dropped.
- A goal that hits a limit is stopped, not paused: it is cleared and saved as
  cleared. The notice says to start it again with `/goal <objective>`.

## Model catalog download limit (OpenCode models went missing)

**What we chose.** `fetchJSON` in `internal/providermodelcatalog/remote.go` now
reads up to 64 MB (`maxCatalogBytes`). It used to stop at 4 MB.

**Why.** The models.dev `api.json` file grew to about 7 MB. The old cap cut it in
the middle, so the JSON did not parse. The error was "unexpected end of JSON
input". The curated catalog came back empty for every provider. Then the picker
fell back to a name guess (`LooksLikeCodingModelID`) on the live list. Many
OpenCode ids (`big-pickle`, `kimi-k3`, `beam-free`) fail that guess, so models
vanished. `modelsource` (the other models.dev reader) already had a 64 MB cap, so
the two readers now agree.

**What we checked first.** We compared the live OpenCode `/models` lists with
models.dev. Zen has 83 chat models live, Go has 31. Every one is in models.dev,
and the protocol in models.dev (`npm`) matches the live `recommended_api` for
all of them. So the wire routing in `internal/providers` was already right. We
did not touch it.

**Also changed.** The Zen default model is now `deepseek-v4.1-flash`. The old
default, `deepseek-v4-flash`, is no longer in the live Zen list.

**Rejected.** Reading `type`, `recommended_api` and `api.*.base_url` from the
live OpenCode list. It is more exact, but the data we need is already in
models.dev, and it would add a network call to provider setup.

**Trade-offs.** A bigger cap means a bad server could make us read 64 MB. That is
the same guard `modelsource` already uses.

**Gemini models.** OpenCode serves Gemini on its `interactions` API. KajiCode has
no client for it, so a request would fail. `servedOnUnsupportedAPI` in
`remote.go` drops these models (models.dev `npm` is `@ai-sdk/google`) from the
OpenCode catalog. The trade-off: with `ShowAllModels` on, the live list can still
show them.
