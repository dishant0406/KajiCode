// Project-scoped skill discovery.
//
// opencode discovers project skills by walking up from the working directory to
// the git worktree and loading every matching skills/<name>/SKILL.md along the
// way (.opencode/skills, .claude/skills, .agents/skills). KajiCode mirrors that
// with two project skill roots per directory: <dir>/.skills and <dir>/.agents/skills
// (the shared multi-agent root already used for global skills). This keeps repo
// skills inherently path-scoped (only active in the subtree they live under) and
// auto-discovered, without requiring an install step.
package skills

import (
	"os"
	"path/filepath"
	"strings"
)

// projectSkillDirNames are the per-directory project skill root names, matched
// case-insensitively like the project guideline loader. `.skills` is the
// project-local root; `.agents/skills` is the shared multi-agent root. Earlier
// entries win when both exist in the same directory.
var projectSkillDirNames = []string{".skills", ".agents/skills"}

// ProjectDirSkillRoot checks dir for each project skill root name (case
// insensitive) and returns the on-disk path of the first that exists and is a
// directory, or "" when none do.
func ProjectDirSkillRoot(dir string) string {
	parent := filepath.Clean(dir)
	for _, name := range projectSkillDirNames {
		match := resolveCaseInsensitiveDir(parent, name)
		if match != "" {
			return match
		}
	}
	return ""
}

// FindProjectGitRoot returns the nearest ancestor of cwd that contains a .git
// entry (file or directory). Returns "" when none is found so callers fall back
// to cwd-only project skill discovery. Mirrors the agent's git-root walk so
// project skill scoping and rule scoping agree.
func FindProjectGitRoot(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ""
	}
	cur := filepath.Clean(cwd)
	for {
		gitPath := filepath.Join(cur, ".git")
		if info, err := os.Stat(gitPath); err == nil && info.IsDir() {
			if _, headErr := os.Stat(filepath.Join(gitPath, "HEAD")); headErr == nil {
				return cur
			}
		} else if err == nil && info.Mode().IsRegular() {
			// A file .git is a worktree/submodule pointer; treat as a root.
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

// ProjectSkillRoots walks the directory chain from gitRoot to cwd (root-to-leaf,
// inclusive) and returns every project skill root found, deduplicated, in
// root-to-leaf order. The nearest (cwd-side) root governs a name clash, so the
// returned order is general-to-specific. When gitRoot is empty or unreachable
// from cwd, only cwd is considered. It never creates directories (missing
// dirs yield no roots).
func ProjectSkillRoots(cwd, gitRoot string) []string {
	dirs := projectDirChain(cwd, gitRoot)
	roots := make([]string, 0, len(dirs))
	seen := map[string]bool{}
	for _, dir := range dirs {
		root := ProjectDirSkillRoot(dir)
		if root == "" || seen[root] {
			continue
		}
		seen[root] = true
		roots = append(roots, root)
	}
	return roots
}

// projectDirChain returns the directory chain from gitRoot to cwd (inclusive)
// in root-to-leaf order, collapsing to [cwd] when gitRoot is empty or
// unreachable. It intentionally mirrors the agent's projectGuidelineDirs walk so
// skill scoping and rule scoping agree.
func projectDirChain(cwd, gitRoot string) []string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return nil
	}
	gitRoot = strings.TrimSpace(gitRoot)
	if gitRoot == "" {
		return []string{filepath.Clean(cwd)}
	}
	rel, err := filepath.Rel(gitRoot, cwd)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return []string{filepath.Clean(cwd)}
	}
	if rel == "." {
		return []string{filepath.Clean(gitRoot)}
	}
	dirs := []string{filepath.Clean(gitRoot)}
	cur := filepath.Clean(gitRoot)
	for _, seg := range strings.Split(rel, string(filepath.Separator)) {
		if seg == "" || seg == "." {
			continue
		}
		cur = filepath.Join(cur, seg)
		dirs = append(dirs, cur)
	}
	return dirs
}

// resolveCaseInsensitiveDir returns the on-disk path of the directory entry
// under parent whose relative name matches rel case-insensitively, walking each
// path segment. Returns "" when any segment is missing or not a directory. This
// mirrors findProjectContextFile's directory resolution so a git-tracked
// ".skills" resolves even when the filesystem is case-sensitive.
func resolveCaseInsensitiveDir(parent, rel string) string {
	if parent == "" {
		return ""
	}
	parts := strings.Split(filepath.Clean(rel), string(filepath.Separator))
	cur := filepath.Clean(parent)
	for _, p := range parts {
		if p == "" || p == "." {
			continue
		}
		entries, err := os.ReadDir(cur)
		if err != nil {
			return ""
		}
		found := false
		for _, e := range entries {
			if e.IsDir() && strings.EqualFold(e.Name(), p) {
				cur = filepath.Join(cur, e.Name())
				found = true
				break
			}
		}
		if !found {
			return ""
		}
	}
	return cur
}

// LoadForCwd merges the project skills governing cwd with the global roots and
// returns the merged, name-deduplicated load. Project roots are appended in
// root-to-leaf order; within the whole list, global roots precede project roots
// so a global skill wins a name clash (matching the preference that globally
// installed skills override repo copies). Roots are traversed in order and
// earlier entries win. Missing roots are treated as empty. A broken primary
// root surfaces as an error; optional roots (agents, project) fail open.
func LoadForCwd(cwd, gitRoot string, globalRoots []string) ([]Skill, []DuplicateName, error) {
	projectRoots := ProjectSkillRoots(cwd, gitRoot)
	roots := make([]string, 0, len(globalRoots)+len(projectRoots))
	seen := map[string]bool{}
	addRoot := func(root string) {
		root = strings.TrimSpace(root)
		if root == "" || seen[root] {
			return
		}
		seen[root] = true
		roots = append(roots, root)
	}
	for _, root := range globalRoots {
		addRoot(root)
	}
	for _, root := range projectRoots {
		addRoot(root)
	}
	if len(roots) == 0 {
		return nil, nil, nil
	}
	return LoadFromRoots(roots)
}

// ListForCwd is LoadForCwd with each skill's Content stripped (like List), for
// catalog/listing callers.
func ListForCwd(cwd, gitRoot string, globalRoots []string) ([]Skill, []DuplicateName, error) {
	loaded, dups, err := LoadForCwd(cwd, gitRoot, globalRoots)
	if err != nil {
		return nil, dups, err
	}
	listed := make([]Skill, 0, len(loaded))
	for _, skill := range loaded {
		skill.Content = ""
		listed = append(listed, skill)
	}
	return listed, dups, nil
}
