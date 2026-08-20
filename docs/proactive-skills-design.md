# Proactive Skills: Production Design

Goal: skills are used *proactively* by the agent, not merely discoverable. This
closes the gap analysis vs opencode (eager project-scoped discovery, reconcile
delta catalog with removal signaling, instructional tool description, auto-skill
preload, per-skill permission gating).

Current state (verified in this work): boot catalog already description-only
(`renderSkillsContext`, 4096-budget, coaching prose, no bodies). Dynamic catalog
re-renders the full list when a *new project root* is observed. Skill tool is
cwd-aware with project roots. Compaction preserves loaded skill bodies.

## Gap 1 — Instructional tool description
`skillTool.Description()` is passive ("Call this when a relevant skill exists").
Rewrite to coach proactive loading, mirroring opencode's skill.txt: "Load a
specialized skill when the task at hand matches…".

## Gap 2 — Reconcile delta catalog with removal signaling
Today `drainSkillsCatalog` re-renders the entire merged list under a fixed
"supersedes" heading. It cannot tell the model a previously-listed project skill
*disappeared* (e.g. cwd moved into a subtree whose parent skill vanished).

Change the renderer + tracker to produce a **reconcile diff**:
- Track `catalogedNames map[string]bool` (names the model last saw) alongside
  `catalogedRoots`.
- On drain, compute `added []SkillInfo` (in new full set but not cataloged) and
  `removed []string` (cataloged but not in current full set).
- Emit a heading + added-list block when `added` non-empty; emit a distinct
  "no longer available" message listing `removed` names when `removed` non-empty.
- Update `catalogedNames` to the current set after drain.
- Boot set is seeded as cataloged so no spurious first-turn emit.

This keeps the model's mental model of available skills correct as the run
moves around the filesystem.

## Gap 3 — Auto-load on path match
Add optional frontmatter-driven scoping so a skill auto-loads when the runtime
observes a matching path, and even when it is never named.
- Frontmatter: `when_to_use:` (glob relative to the skill's repo/git root, e.g.
  `docs/**`, `cmd/kajicode/**`) and `scope:` (short human trigger phrase).
- Tracker keeps an observed-path set; on ObservePath, match each observed dir
  against every project skill's globs; when a match first appears, push the
  matching skill's *name + scope* into a pending auto-load queue that the loop
  drains as a short user message: `Skill "<name>" matches <path>; load it with
  the skill tool for guidance.` (coach, don't dump full body).
- Built-in **`customize-kajicode`** auto-skill: Description says "Use ONLY when
  editing KajiCode's own config/extensions". It is synthesized in the boot
  catalog and auto-loads when an observed path is under the KajiCode repo's
  `internal/` or `docs/` owning config/extensions. Modeld after opencode's
  `customize-opencode`.

## Gap 4 — Per-skill permission gating
opencode gates `skill:<name>` (allow/ask/deny). Add frontmatter `permission:`
per skill (allow|prompt|deny), defaulting to allow; surfaced into the boot
catalog line (a `[deny]`/`[prompt]` marker) and enforced by the skill tool /
TUI before loading the body:
- `deny`: skill tool returns error, never loads body, removed from command
  completion. **Linked to the shared bypass-all setting**: under bypass-all, the
  in-tool deny guard relaxes and the deny skill's body loads, exactly as
  bypass-all already does for every other tool via `profilePermission`.
- `prompt`: requires escalation-style approval before returning the body.
- `allow` (default): unchanged.

## Files touched
- `internal/skills/skills.go` — parse `when_to_use`/`scope`/`permission`; new
  `Skill` fields; `Skill.Permission()` helper.
- `internal/skills/project.go` — glob match helper (`MatchScopedPaths`) on a
  resolved git root.
- `internal/agent/guidelines.go` — reconcile diff catalog; auto-load queue;
  observed-path matching; catalog + catalogedNames tracking.
- `internal/agent/system_prompt.go` — permission markers in catalog lines;
  custom `customize-kajicode` synthesized entry.
- `internal/tools/skill.go` + `internal/plugins/activate.go` — coaching
  description; permission enforcement in `run`.
- `internal/agent/loop.go` — drain auto-load queue.
- `internal/config/types.go` — optional per-skill permission config override.
- Tests: `internal/skills/*_test.go`, `internal/agent/guidelines_dynamic_test.go`,
  `internal/agent/skills_loop_integration_test.go`, `internal/tools/skill_test.go`.

## Precedence
Config/permission applies to a skill by name when present; else frontmatter
permission; else allow. Built-in `customize-kajicode` is lowest precedence and
never shadows a real skill of the same name.
