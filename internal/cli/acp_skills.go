package cli

import (
	"strings"

	"github.com/dishant0406/KajiCode/internal/plugins"
	"github.com/dishant0406/KajiCode/internal/skills"
)

// Wiring for ACP skill invocation: installed skills are advertised as /name
// commands and, when invoked, expand to the skill body plus the request — the
// same behavior the TUI's skill dispatch has.

// acpSkills returns the workspace's installed skills (global + project + plugin
// roots), the same merged set the model's skill catalog and /skills use. It
// mirrors that catalog's builtin gating: the built-in skill is prepended only
// when at least one on-disk skill exists, so a skill-less workspace has no
// skills here either. nil on any load error.
func acpSkills(workspaceRoot string, deps appDeps) []skills.Skill {
	primary := ""
	if deps.skillsDir != nil {
		primary = deps.skillsDir()
	}
	loaded, _, err := skills.LoadMerged(primary, acpPluginSkillRoots(workspaceRoot, deps), skills.ProjectRootsForCwd(workspaceRoot))
	if err != nil || len(loaded) == 0 {
		return nil
	}
	return skills.PrependBuiltin(loaded)
}

// acpPluginSkillRoots resolves the plugin-contributed skill roots for a
// workspace, mirroring activatePlugins (same trust gate) but without a tool
// registry — only the roots matter here. Returns nil when plugins cannot load so
// plugin skills are simply absent.
func acpPluginSkillRoots(workspaceRoot string, deps appDeps) []string {
	if deps.loadPlugins == nil {
		return nil
	}
	excludeProject, _ := resolveTrust(workspaceRoot)
	loaded, err := deps.loadPlugins(plugins.LoadOptions{Cwd: workspaceRoot, ExcludeProject: excludeProject})
	if err != nil {
		return nil
	}
	return plugins.Activate(nil, loaded.Plugins, plugins.ActivateOptions{Cwd: workspaceRoot}).SkillRoots
}

// acpSkillNames returns the slash names of the workspace skills that are
// invocable: a slash-shaped name (typeable) with a non-empty body (expandable).
// A name with spaces has no typeable form, and a bodyless skill could never
// expand, so advertising it would be a dead end. Keyed by slash name.
func acpSkillNames(workspaceRoot string, deps appDeps) map[string]skills.Skill {
	loaded := acpSkills(workspaceRoot, deps)
	out := make(map[string]skills.Skill, len(loaded))
	for _, skill := range loaded {
		name := skills.SlashName(skill.Name)
		if name == "" || strings.TrimSpace(skill.Content) == "" {
			continue
		}
		if _, exists := out[name]; !exists {
			out[name] = skill
		}
	}
	return out
}

// acpSkillClaimed reports whether a slash name is already owned by a builtin/ACP
// command or a saved prompt snippet, so a skill of the same name yields to it
// (precedence: builtin > user command > skill, matching the TUI).
func acpSkillClaimed(workspaceRoot, name string, deps appDeps) bool {
	if _, isBuiltin := lookupACPCommand(name); isBuiltin {
		return true
	}
	for _, snippet := range acpUserCommandSnippets(deps, workspaceRoot) {
		if snippet.Name == name {
			return true
		}
	}
	return false
}

// acpExpandSkill resolves "/name args" against the workspace's installed skills,
// returning the prompt to run (the skill body plus the request). ok=false when no
// invocable skill matches, so the caller falls through.
func acpExpandSkill(deps appDeps) func(workspaceRoot, name, args string) (string, bool) {
	return func(workspaceRoot, name, args string) (string, bool) {
		want := skills.SlashName(name)
		if want == "" || acpSkillClaimed(workspaceRoot, want, deps) {
			return "", false
		}
		skill, ok := acpSkillNames(workspaceRoot, deps)[want]
		if !ok {
			return "", false
		}
		return skills.InvocationPrompt(skill.Content, args), true
	}
}
