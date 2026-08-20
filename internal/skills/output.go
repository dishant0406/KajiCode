package skills

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// skillFilesLimit caps how many sibling files are listed in <skill_files> so a
// skill with many resources cannot blow the tool-output budget (matches
// opencode's FILE_LIMIT).
const skillFilesLimit = 10

// SkillOutput renders a loaded skill for the model as a self-describing block:
// a <skill_content name="..."> wrapper carrying the markdown body, the skill's
// base directory, and a sampled list of sibling files. The wrapper lets the
// agent's compaction preservation recognize a skill body reliably, and the
// sibling list (opencode's <skill_files>) reveals the scripts/reference files a
// skill may reference without the model having to search. Files are confined to
// the skill's own directory: symlinks that escape it are never listed (the
// loader already refuses to read such SKILL.md files).
func SkillOutput(skill Skill) string {
	var b strings.Builder
	b.WriteString(`<skill_content name="`)
	b.WriteString(skill.Name)
	b.WriteString(`">`)
	b.WriteString("\n# Skill: ")
	b.WriteString(skill.Name)
	b.WriteByte('\n')
	b.WriteByte('\n')
	b.WriteString(strings.TrimSpace(skill.Content))
	b.WriteByte('\n')
	b.WriteByte('\n')
	dir := filepath.Dir(skill.Path)
	b.WriteString("Base directory for this skill: ")
	b.WriteString(dir)
	b.WriteString("\nRelative paths in this skill (e.g., scripts/, reference/) are relative to this base directory.\nNote: file list is sampled.\n\n<skill_files>\n")
	for _, file := range skillSiblingFiles(dir) {
		b.WriteString("<file>")
		b.WriteString(file)
		b.WriteString("</file>\n")
	}
	b.WriteString("</skill_files>\n</skill_content>")
	return b.String()
}

// skillSiblingFiles returns the sorted, relative-path listing of regular files
// and directories directly inside dir (one level, mirroring opencode's glob of
// immediate skill resources), excluding SKILL.md, capped at skillFilesLimit
// entries in ./-prefix-free relative form. Symlinks escaping dir are skipped so
// the listing never discloses paths outside the skill root.
func skillSiblingFiles(dir string) []string {
	// Resolve dir through symlinks so confinement below (EvalSymlinks of each
	// sibling) compares real paths against a real root; otherwise every sibling
	// inside a symlinked temp path would look "outside" and be skipped.
	dirReal, err := filepath.EvalSymlinks(dir)
	if err != nil {
		dirReal = dir
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if strings.EqualFold(name, skillFileName) {
			continue
		}
		// Skip symlinks: resolve and only keep those that stay inside dir, so the
		// advertised paths are all readable without crossing the skill boundary.
		full := filepath.Join(dir, name)
		if real, rerr := filepath.EvalSymlinks(full); rerr == nil {
			rel, rerr2 := filepath.Rel(dirReal, real)
			if rerr2 != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
				continue
			}
		} else {
			// If the entry is not a symlink this is fine; if we cannot resolve it
			// (dangling symlink), skip to avoid listing an unreadable path.
			if e.Type()&os.ModeSymlink != 0 {
				continue
			}
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > skillFilesLimit {
		names = names[:skillFilesLimit]
	}
	return names
}
