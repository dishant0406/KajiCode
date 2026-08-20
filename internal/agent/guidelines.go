package agent

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

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
}

// newGuidelineTracker builds a tracker wired to a workspace root, used to cap the
// upward walk when a touched directory has no git metadata. It is always non-nil;
// every method is a no-op when the feature is inactive so callers can wire it
// unconditionally.
func newGuidelineTracker(workspaceRoot string) *guidelineTracker {
	return &guidelineTracker{
		workspaceRoot: workspaceRoot,
		observed:      map[string]bool{},
		known:         map[string]bool{},
		files:         map[string]bool{},
	}
}

// seedStartup records the project context files that were already injected into
// the boot system prompt (the chain from the git root down to the startup cwd) so
// they are never reported as "newly discovered". Callers invoke it once, right
// after constructing the tracker from the same cwd the system prompt was built
// from.
func (t *guidelineTracker) seedStartup(cwd string) {
	if t == nil {
		return
	}
	gitRoot := FindProjectGitRoot(cwd)
	for _, dir := range guidelineLookupDirs(cwd, gitRoot) {
		if match := findProjectContextFile(dir); match != "" {
			t.recordKnown(filepath.Clean(match))
		}
	}
}

func (t *guidelineTracker) recordKnown(path string) {
	t.known[path] = true
	delete(t.files, path)
}

// ObservePath implements tools.ProjectGuidelineObserver. It walks upward from
// absDir looking for project context files and records any not already known.
// Safe for concurrent calls from parallel read batches (mutex-guarded). Absolute
// paths only.
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
}

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
