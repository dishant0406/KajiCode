package agent

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/dishant0406/KajiCode/internal/skills"
	"github.com/dishant0406/KajiCode/internal/tools"
)

// guidelineTracker is the agent-side consumer of tools.RunOptions.ProjectGuidelines.
// Tools report every absolute directory they resolve (a file's parent dir, a scan
// root, or a command's cwd) via ObservePath; the tracker discovers the
// AGENTS.md/KAJICODE.md files that govern that location and surfaces any that are
// NOT already in the conversation (see drain).
//
// Hierarchy matches the system-prompt builder: from the touching directory up to
// the git root, plus the per-user KAJICODE.md (see findProjectContextFile /
// projectGuidelineDirs). Because discovery keys off directories the tools actually
// touch, project rules stay correct when a shell command cds into a subdirectory
// mid-run or a read lands far below the startup cwd. Only newly-discovered files
// are injected; a file already carried in the boot system prompt (recorded via
// seedStartup) is never re-sent, so injection is bounded by the number of distinct
// AGENTS.md files the run actually touches.
type guidelineTracker struct {
	workspaceRoot string

	mu sync.Mutex
	// observed dedupes directory walk work across parallel report calls; it never
	// gates which files are considered discovered.
	observed map[string]bool
	known    map[string]bool // files already present in the boot system prompt; never drained
	files    map[string]bool // newly-discovered files awaiting a drain
	// skillRoots holds every project skill root discovered so far (see
	// skills.ProjectSkillRoots for each observed dir), so the skill tool and the
	// dynamic catalog can resolve the authoritative set for whatever the run has
	// touched. Seed start reflects the startup cwd chain; ObservePath extends it
	// as tools resolve deeper/subtree directories.
	skillRoots map[string]bool
	// catalogedRoots tracks which project skill roots are already reflected in the
	// catalog block the model last saw, so a newly-discovered subtree root triggers
	// exactly one updated <available_skills> injection (opencode's live catalog
	// refresh), not a re-emission each turn. Seeded from the startup chain.
	catalogedRoots map[string]bool
	// catalogedNames tracks the skill names the model last saw in the catalog, so
	// a reconcile diff can report newly-added names (supersede message) and names
	// that have disappeared (removal message). Seeded from the boot catalog.
	catalogedNames map[string]bool
	// catalogDirty is set when a project skill root is observed that is not yet
	// cataloged, signalling drain to re-render the catalog.
	catalogDirty bool
	// bootSkills is the catalog baked into the boot system prompt (global + plugin
	// + startup-cwd project skills). It is the baseline the dynamic render holds.
	bootSkills []SkillInfo
	// catalogLoader resolves the merged catalog (project roots + boot baseline)
	// into two values: the authoritative, de-duplicated full skill set (for name
	// inventory tracking) and the rendered <available_skills> block to inject.
	// nil disables the dynamic catalog (the loop does not set it in tests that
	// exercise guidelines only).
	catalogLoader func(projectRoots []string, boot []SkillInfo) ([]SkillInfo, string)
	// autoLoaded tracks skill names already auto-load-signalled this run, so the
	// same skill is never coached twice even when the run re-observes a matching
	// path.
	autoLoaded map[string]bool
	// autoLoadQueue holds path-matched skills awaiting a coach message drain (see
	// recordAutoLoadsFor / drainSkillAutoLoads).
	autoLoadQueue []skillAutoLoad
}

// newGuidelineTracker builds a tracker wired to a workspace root, used to cap the
// upward walk when a touched directory has no git metadata. It is always non-nil;
// every method is a no-op when the feature is inactive so callers can wire it
// unconditionally.
func newGuidelineTracker(workspaceRoot string) *guidelineTracker {
	return &guidelineTracker{
		workspaceRoot:  workspaceRoot,
		observed:       map[string]bool{},
		known:          map[string]bool{},
		files:          map[string]bool{},
		skillRoots:     map[string]bool{},
		catalogedRoots: map[string]bool{},
		catalogedNames: map[string]bool{},
		autoLoaded:     map[string]bool{},
	}
}

// skillAutoLoad is a path-matched skill awaiting a coach message. It carries the
// skill name, the observed path that matched its `when_to_use` scoping, and its
// human-readable scope phrase so the drain can explain why the skill applies.
type skillAutoLoad struct {
	SkillName string
	Path      string
	Scope     string
}

// setCatalog configures the dynamic skill catalog: boot is the baseline catalog
// already in the system prompt, and loader re-renders the merged catalog
// (project roots + boot) so a newly-discovered project skill can be surfaced.
// Seeding the startup root set here beforehand (via seedStartup) makes the boot
// state "already cataloged", so no spurious update fires for skills the prompt
// already lists.
func (t *guidelineTracker) setCatalog(boot []SkillInfo, loader func(projectRoots []string, boot []SkillInfo) ([]SkillInfo, string)) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.bootSkills = boot
	t.catalogLoader = loader
	for root := range t.skillRoots {
		t.catalogedRoots[root] = true
	}
	// Seed the name inventory from the boot catalog so the boot state is "already
	// cataloged": no spurious first-turn added/removed diff for skills the boot
	// system prompt already lists.
	t.catalogedNames = make(map[string]bool, len(boot))
	for _, info := range boot {
		if name := strings.TrimSpace(info.Name); name != "" {
			t.catalogedNames[name] = true
		}
	}
	// Every root known at setCatalog time is now reflected in the boot catalog, so
	// none is pending; clears any dirt seedStartup left behind (seed runs first and
	// walks the startup chain, marking those roots dirty before setCatalog covers
	// them). Without this, the loop would emit a spurious first-turn update for
	// skills the boot system prompt already lists.
	t.catalogDirty = false
}

// seedStartup records the project context files that were already injected into
// the boot system prompt (the chain from the git root down to the startup cwd) so
// they are never reported as "newly discovered". It also records the project skill
// roots along that same startup chain so the skill tool resolves them even before
// any tool reports a directory. Callers invoke it once, right after constructing
// the tracker from the same cwd the system prompt was built from.
func (t *guidelineTracker) seedStartup(cwd string) {
	if t == nil {
		return
	}
	gitRoot := FindProjectGitRoot(cwd)
	for _, dir := range guidelineLookupDirs(cwd, gitRoot) {
		if match := findProjectContextFile(dir); match != "" {
			t.recordKnown(filepath.Clean(match))
		}
		t.recordSkillRootsFor(dir)
	}
}

func (t *guidelineTracker) recordKnown(path string) {
	t.known[path] = true
	delete(t.files, path)
}

// recordSkillRootsFor adds every project skill root under dir (if any) to the
// tracked set, walking dir only (not upward — callers walk the chain). Caller
// must hold t.mu.
func (t *guidelineTracker) recordSkillRootsFor(dir string) {
	if root := projectSkillRootForDir(dir); root != "" {
		if !t.skillRoots[root] {
			t.skillRoots[root] = true
			// A root the boot catalog already covered (seeded before setCatalog) does
			// not mark the catalog dirty; only genuinely new roots do.
			if !t.catalogedRoots[root] {
				t.catalogDirty = true
			}
		}
	}
}

// projectSkillRootForDir returns the single project skill root found directly
// under dir (skills.ProjectDirSkillRoot), or "" when none exists.
func projectSkillRootForDir(dir string) string {
	return skills.ProjectDirSkillRoot(dir)
}

// ObservePath implements tools.ProjectGuidelineObserver. It walks upward from
// absDir looking for project context files and records any not already known. It
// also records the project skill roots it crosses. Safe for concurrent calls from
// parallel read batches (mutex-guarded). Absolute paths only.
func (t *guidelineTracker) ObservePath(absDir string) {
	if t == nil {
		return
	}
	absDir = filepath.Clean(absDir)
	if absDir == "" || absDir == "." {
		return
	}
	gitRoot := FindProjectGitRoot(absDir)
	if gitRoot == "" {
		gitRoot = t.workspaceRoot
	}
	if gitRoot == "" {
		gitRoot = absDir
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.observed[absDir] {
		return
	}
	t.observed[absDir] = true

	for _, dir := range guidelineLookupDirs(absDir, gitRoot) {
		t.recordSkillRootsFor(dir)
		match := findProjectContextFile(dir)
		if match == "" {
			continue
		}
		clean := filepath.Clean(match)
		if t.known[clean] {
			continue
		}
		t.files[clean] = true
	}
	// Proactive auto-load: after recording the newly-observed roots, check whether
	// any project skill declares a `when_to_use` glob matching the observed path.
	// A first match queues a coach message (see drainSkillAutoLoads) so the model
	// pulls in relevant guidance without being told the skill's name first.
	t.recordAutoLoadsFor(absDir, gitRoot)
}

// recordAutoLoadsFor matches the observed path against the built-in/synthesized
// skills and every project skill's `when_to_use` globs (resolved from the project
// skill roots discovered so far) and records a coach message for each skill whose
// scoping matches and has not already been auto-loaded. base is the git/workspace
// root the disk globs are grounded to. Caller must hold t.mu.
func (t *guidelineTracker) recordAutoLoadsFor(observedDir, base string) {
	// Built-in customize-kajicode auto-skill: when the observed path is under the
	// KajiCode repo's own extension/config surfaces, coach loading it.
	builtin := skills.BuiltinCustomizeKajicode()
	builtinBase := skills.ProjectBuiltinBase(observedDir)
	if builtinBase != "" && skills.MatchWhenToUse(builtinBase, builtin.WhenToUse, observedDir) {
		t.maybeQueueAutoLoad(observedDir, builtin.Name, builtin.Scope)
	}

	// Load project skills once per observed dir; project roots are small and the
	// match set is checked only when a root's why-to-use patterns could apply.
	roots := make([]string, 0, len(t.skillRoots))
	for root := range t.skillRoots {
		roots = append(roots, root)
	}
	if len(roots) == 0 {
		return
	}
	loaded, _, err := skills.ListFromRoots(roots)
	if err != nil {
		return
	}
	for _, skill := range loaded {
		name := strings.TrimSpace(skill.Name)
		if name == "" || len(skill.WhenToUse) == 0 {
			continue
		}
		if skills.MatchWhenToUse(base, skill.WhenToUse, observedDir) {
			t.maybeQueueAutoLoad(observedDir, name, skill.Scope)
		}
	}
}

// maybeQueueAutoLoad records a coach message for a path-matched skill unless it
// was already auto-loaded this run. Caller must hold t.mu.
func (t *guidelineTracker) maybeQueueAutoLoad(observedDir, name, scope string) {
	if name == "" || t.autoLoaded[name] {
		return
	}
	t.autoLoaded[name] = true
	t.autoLoadQueue = append(t.autoLoadQueue, skillAutoLoad{
		SkillName: name,
		Path:      observedDir,
		Scope:     strings.TrimSpace(scope),
	})
}

// drainSkillAutoLoads returns formatted coach messages for skills auto-matched
// by path since the last drain, clearing the queue. Each message names the skill
// and the matched path and nudges loading via the skill tool; it deliberately
// does NOT dump the skill body (the model should load it on demand), avoiding
// unbounded context growth from auto-loading.
func (t *guidelineTracker) drainSkillAutoLoads() []string {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	if len(t.autoLoadQueue) == 0 {
		t.mu.Unlock()
		return nil
	}
	queue := t.autoLoadQueue
	t.autoLoadQueue = nil
	t.mu.Unlock()

	messages := make([]string, 0, len(queue))
	for _, entry := range queue {
		line := "A skill applies to the location you are working in: " + entry.SkillName +
			" (matches " + entry.Path + ")."
		if entry.Scope != "" {
			line += " Use when: " + entry.Scope + "."
		}
		line += " Load it with the skill tool for guidance."
		messages = append(messages, line)
	}
	return messages
}

// currentProjectSkillRoots returns the deduplicated project skill roots observed
// so far, in stable sorted order. It forms the authoritative project-root set for
// the skill tool and the dynamic catalog. nil safe.
func (t *guidelineTracker) currentProjectSkillRoots() []string {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	roots := make([]string, 0, len(t.skillRoots))
	for root := range t.skillRoots {
		roots = append(roots, root)
	}
	t.mu.Unlock()
	sort.Strings(roots)
	return roots
}

// drainSkillsCatalog returns an updated <available_skills> block when the skill
// catalog changed since the last render — either a project skill root was
// discovered (new names appear) or a previously-cataloged name disappeared from
// the authoritative set — mirroring opencode's live skill-catalog refresh
// (SkillGuidance re-read per turn with a diff/supersede signal). The returned
// block is a user-role message whose unambiguous heading tells the model it
// SUPERSEDES the boot <available_skills> list. It is emitted at most once per
// catalog change, then both the root and name inventories are marked current so
// steady-state runs emit nothing. nil safe.
//
// The block is deliberately NOT wrapped in project instructions tags (it is not a
// guideline file); it travels as a plain user message like the boot catalog's
// siblings. The supersede heading is unique, so repeated injections (multiple new
// roots over a long run) are recognizable and do not merge badly with the boot
// prompt.
func (t *guidelineTracker) drainSkillsCatalog() string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	if !t.catalogDirty {
		t.mu.Unlock()
		return ""
	}
	roots := make([]string, 0, len(t.skillRoots))
	for root := range t.skillRoots {
		roots = append(roots, root)
	}
	boot := append([]SkillInfo{}, t.bootSkills...)
	loader := t.catalogLoader
	prevNames := make(map[string]bool, len(t.catalogedNames))
	for name := range t.catalogedNames {
		prevNames[name] = true
	}
	t.catalogedRoots = make(map[string]bool, len(t.skillRoots))
	for root := range t.skillRoots {
		t.catalogedRoots[root] = true
	}
	t.catalogDirty = false
	t.mu.Unlock()

	if loader == nil {
		// No catalog loader wired: nothing authoritative to render. Leave the
		// dirt cleared so we do not retry every turn with no effect.
		return ""
	}
	sort.Strings(roots)
	fullSet, rendered := loader(roots, boot)
	if len(fullSet) == 0 && strings.TrimSpace(rendered) == "" {
		return ""
	}

	// Reconcile the name inventory: report names added since the last render and
	// names that have disappeared, so the model's mental model of the discovery
	// surface stays correct as the run moves around the filesystem.
	currentNames := make(map[string]bool, len(fullSet))
	added := make([]string, 0, len(fullSet))
	for _, info := range fullSet {
		name := strings.TrimSpace(info.Name)
		if name == "" {
			continue
		}
		currentNames[name] = true
		if !prevNames[name] {
			added = append(added, name)
		}
	}
	removed := make([]string, 0)
	for name := range prevNames {
		if !currentNames[name] {
			removed = append(removed, name)
		}
	}
	t.mu.Lock()
	t.catalogedNames = currentNames
	t.mu.Unlock()

	var b strings.Builder
	b.WriteString(dynamicSkillsHeading)
	if len(added) > 0 || len(removed) > 0 {
		sort.Strings(added)
		sort.Strings(removed)
		b.WriteString("\n\n")
		if len(added) > 0 {
			b.WriteString("Newly available skills: " + strings.Join(added, ", ") + ".\n")
		}
		if len(removed) > 0 {
			b.WriteString("No longer available: " + strings.Join(removed, ", ") + " — these skills have left the discovery surface (e.g. the run moved out of their subtree) and should not be loaded.\n")
		}
	}
	if block := strings.TrimSpace(rendered); block != "" {
		b.WriteString("\n\n")
		b.WriteString(block)
	}
	return strings.TrimSpace(b.String())
}

// dynamicSkillsHeading introduces a dynamic <available_skills> update that
// supersedes the boot catalog's skill list.
const dynamicSkillsHeading = "# Updated skill catalog (supersedes the <available_skills> list above)"

// drain folds every newly discovered, non-trivial project context file into a
// slice of user-role guideline blocks, in a stable sorted path order, and clears
// the pending set (files enter the known set once drained, so a later touch of the
// same file is not re-sent). Returns nil when nothing new is ready.
//
// Each block uses the <INSTRUCTIONS> format that compaction_preserve.go already
// carries verbatim across compaction (see projectInstructionBlock /
// projectInstructionEntries): a "# <label> instructions for <dir>" heading with a
// wrapped <INSTRUCTIONS> body. So a subdirectory AGENTS.md injected mid-run is
// preserved into the compacted summary's preserved-state block instead of being
// paraphrased away when the turns that touched that directory are elided.
// Headings are per-file unique (label + directory), so repeated compactions merge
// by source rather than duplicating.
func (t *guidelineTracker) drain() []string {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	paths := make([]string, 0, len(t.files))
	for path := range t.files {
		paths = append(paths, path)
	}
	t.files = map[string]bool{}
	t.mu.Unlock()

	if len(paths) == 0 {
		return nil
	}
	sort.Strings(paths)

	blocks := make([]string, 0, len(paths))
	for _, path := range paths {
		content := readGuidelineFile(path)
		if content == "" {
			continue
		}
		blocks = append(blocks, formatGuidelineInstruction(path, content))
	}
	if len(blocks) == 0 {
		return nil
	}
	// Promote drained paths to known so a directory re-touched later is not
	// re-sent, even if its file read yielded nothing this time.
	t.mu.Lock()
	for _, path := range paths {
		t.known[path] = true
	}
	t.mu.Unlock()
	return blocks
}

// formatGuidelineInstruction renders a discovered project context file as an
// authoritative, compaction-preserved instruction block. path is the guideline
// file's absolute path; content is its already-truncated body. The heading must
// start with "# " and contain " instructions for " — and the body must be wrapped
// in <INSTRUCTIONS>…</INSTRUCTIONS> — for compaction_preserve.projectInstructionBlock
// to recognize and carry it across compaction. Directories keep distinct sources
// so root and subdirectory AGENTS.md files merge independently.
func formatGuidelineInstruction(path, content string) string {
	label := guidelineLabel(path)
	if label == "" {
		label = "project"
	}
	dir := filepath.Base(filepath.Dir(path))
	if dir == "" || dir == "." || dir == string(filepath.Separator) {
		dir = "workspace"
	}
	heading := "# " + label + projectInstructionsHeadingMarker + dir
	return heading + "\n\n" + projectInstructionsOpenTag + "\n" +
		strings.TrimSpace(content) + "\n" + projectInstructionsCloseTag
}

// guidelineLabel renders path as a short label relative to its git root.
func guidelineLabel(path string) string {
	if label := projectGuidelineLabel(path, FindProjectGitRoot(path)); label != "" {
		return label
	}
	return path
}

// readGuidelineFile reads and trims a project context file, capping it at the same
// per-file limit the system-prompt builder uses so a runaway AGENTS.md cannot blow
// the context budget.
func readGuidelineFile(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return ""
	}
	return truncateGuidelineContent(content, maxProjectContextBytes)
}

// guidelineLookupDirs is a pass-through to projectGuidelineDirs so the tracker's
// dependency on the shared hierarchical walk stays explicit.
func guidelineLookupDirs(absDir, gitRoot string) []string {
	return projectGuidelineDirs(absDir, gitRoot)
}

// reassertGuidelines rebuilds the authoritative project-context files for the
// current working directory as <INSTRUCTIONS> blocks and appends each one as a
// fresh user-role message. It is the compaction-safety net: called right after a
// compaction, it guarantees the AGENTS.md/KAJICODE.md rules are present verbatim
// in the compacted history even if a root-level AGENTS.md was edited mid-run (so
// it is not in the boot system prompt anymore) OR the summarizer paraphrased an
// instruction block away. It also re-applies the rules on the very next request,
// so the model cannot drift down-stream of a context reduction. Rebuilding uses
// the shared hierarchical lookup, so a cd'd working directory gets exactly the
// rules that now govern it. nil safe.
func (t *guidelineTracker) reassertGuidelines(cwd string) []string {
	if t == nil {
		return nil
	}
	gitRoot := FindProjectGitRoot(cwd)
	blocks := make([]string, 0, 4)
	seen := map[string]bool{}
	for _, dir := range guidelineLookupDirs(cwd, gitRoot) {
		match := findProjectContextFile(dir)
		if match == "" {
			continue
		}
		path := filepath.Clean(match)
		if seen[path] {
			continue
		}
		seen[path] = true
		content := readGuidelineFile(path)
		if content == "" {
			continue
		}
		blocks = append(blocks, formatGuidelineInstruction(path, content))
	}
	return blocks
}

// observer returns the tools.ProjectGuidelineObserver view of this tracker. Non-nil
// when the tracker is active; tools no-op when they receive a nil observer.
func (t *guidelineTracker) observer() tools.ProjectGuidelineObserver {
	if t == nil {
		return nil
	}
	return t
}

// projectSkillRoots returns the observed project skill roots the loop should
// forward to tools and the dynamic catalog. Non-nil when active.
func (t *guidelineTracker) projectSkillRoots() []string {
	if t == nil {
		return nil
	}
	return t.currentProjectSkillRoots()
}
