// Built-in convenience skills.
//
// These are synthesized (not read from disk) and synthesized into the boot
// catalog and auto-load matching. They follow the pattern opencode uses for its
// built-in customize-opencode auto-skill: present from the start, self-scoping on
// the domain they govern, so the model pulls in the right guidance without being
// told the name first.
package skills

import (
	"os"
	"path/filepath"
	"strings"
)

// BuiltinCustomizeKajicodeName is the built-in skill that teaches editing
// KajiCode's own config, extensions, and packaging.
const BuiltinCustomizeKajicodeName = "customize-kajicode"

// BuiltinCustomizeKajicode synthesizes the built-in customize-kajicode skill.
// Its when_to_use globs scope it to directories/functions that own KajiCode's
// config, extension surfaces, and release packaging; matching one auto-loads it.
// It is deliberately low-precedence: a real on-disk skill of the same name
// shadows it (see PrependBuiltin).
func BuiltinCustomizeKajicode() Skill {
	return Skill{
		Name:        BuiltinCustomizeKajicodeName,
		Description: "Guidance for editing KajiCode's own config (kajicode.json), extension surfaces (plugins, skills, hooks, MCP), and release/npm packaging. Use ONLY when editing KajiCode's own internals — not when writing user code.",
		Scope:       "Use ONLY when editing KajiCode's own config, extensions, or packaging",
		// Match the extension/question surfaces plus release packaging, relative
		// to the repo root (these paths exist in the KajiCode source tree).
		WhenToUse:  []string{"internal/plugins/**", "internal/skills/**", "internal/hooks/**", "internal/specialist/**", "internal/mcp/**"},
		Permission: PermissionAllow,
		Path:       "builtin",
		Content:    "<INSTRUCTIONS>\nEditing KajiCode's own internals? Follow the repository's rules in AGENTS.md first (build/validation, architecture ownership, code rules). Keep config and extension changes in the package that owns the concern (see docs/architecture.md); do not add hardcoded checks in unrelated layers when a registry/interface exists. Validate with go test ./... and the documented release smoke before shipping.\n</INSTRUCTIONS>",
	}
}

// PrependBuiltinBudget returns a copy of skills with the built-in customize-kajicode
// skill synthesized at the front when no real skill already uses that name, and
// WITHIN the copy so callers get a stable slice. Covered here in one place so boot
// catalog and auto-load matching agree on the same builtin identity.
func PrependBuiltin(skills []Skill) []Skill {
	for _, existing := range skills {
		if strings.EqualFold(strings.TrimSpace(existing.Name), BuiltinCustomizeKajicodeName) {
			return skills // a real skill shadows the builtin
		}
	}
	return append([]Skill{BuiltinCustomizeKajicode()}, skills...)
}

// ProjectBuiltinBase returns the git/workspace base the builtin's when_to_use
// globs are grounded to for a given observed path: the nearest ancestor that looks
// like the KajiCode repo (has an internal/ dir), or the git root of the path. This
// lets auto-load match the builtin only when the run actually touches KajiCode's
// own internals. base defaults to cwd when nothing resolves.
func ProjectBuiltinBase(observedDir string) string {
	if observed := strings.TrimSpace(observedDir); observed != "" {
		dir := filepath.Clean(observed)
		// Walk ancestors looking for a directory containing internal/ (the signal
		// this is the KajiCode source tree).
		cur := dir
		for {
			if isDir(filepath.Join(cur, "internal")) {
				return cur
			}
			parent := filepath.Dir(cur)
			if parent == cur {
				break
			}
			cur = parent
		}
		if gitRoot := FindProjectGitRoot(dir); gitRoot != "" {
			return gitRoot
		}
		return dir
	}
	return ""
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}
