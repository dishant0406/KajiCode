package skills

import "strings"

// MergeRoots returns the ordered skill roots a run should scan: the global roots
// (the primary skills dir, then ~/.agents/skills, then ~/.claude/skills), then
// the project roots governing the working directory, then plugin-contributed
// roots. LoadFromRoots resolves earlier roots first, so a globally installed
// skill wins a name clash against a project copy, which in turn wins against a
// plugin copy. Empty roots are omitted.
//
// This is the single ordering authority shared by the skill tool and the
// system-prompt catalog, so the two can never disagree about which skills exist
// or which copy of a duplicated name wins.
func MergeRoots(primary string, pluginRoots []string, projectRoots []string) []string {
	roots := GlobalRoots(primary)
	for _, root := range projectRoots {
		if root = strings.TrimSpace(root); root != "" {
			roots = append(roots, root)
		}
	}
	for _, root := range pluginRoots {
		if root = strings.TrimSpace(root); root != "" {
			roots = append(roots, root)
		}
	}
	return roots
}

// LoadMerged loads the merged skills for a run (see MergeRoots), keeping each
// skill's Content so a skill tool can return its body.
func LoadMerged(primary string, pluginRoots []string, projectRoots []string) ([]Skill, []DuplicateName, error) {
	return LoadFromRoots(MergeRoots(primary, pluginRoots, projectRoots))
}

// ListMerged is LoadMerged with each skill's Content stripped, for catalog and
// listing callers.
func ListMerged(primary string, pluginRoots []string, projectRoots []string) ([]Skill, []DuplicateName, error) {
	return ListFromRoots(MergeRoots(primary, pluginRoots, projectRoots))
}

// ProjectRootsForCwd returns the project skill roots governing cwd, the same
// set the guideline tracker observes at runtime. It is the conventional way to
// supply projectRoots to LoadMerged/ListMerged when only a working directory is
// known.
func ProjectRootsForCwd(cwd string) []string {
	return ProjectSkillRoots(cwd, FindProjectGitRoot(cwd))
}
