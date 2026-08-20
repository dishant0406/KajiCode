// when_to_use scoping: proactive path-match auto-loading.
//
// A skill can declare `when_to_use:` in its frontmatter as a space/comma list of
// glob patterns (e.g. `docs/** cmd/kajicode/**`). Patterns are relative to the
// project's git root (or the workspace root when no git root exists). When the
// runtime observes a path matching a pattern, the skill auto-loads so the model
// pulls in relevant guidance without being told its name first — mirroring
// opencode's built-in auto-skill and its project-scoped discovery.
package skills

import (
	"path/filepath"
	"strings"
)

// MatchWhenToUse reports whether the given absolute observed path falls under
// any of the skill's `when_to_use` patterns, interpreted relative to base (the
// git/workspace root the patterns are scoped to). It resolves the path relative
// to base; any path outside base cannot match. Patterns are slash-normalized and
// matched with ** support (zero or more path segments) plus single-* and ?
// wildcards within a segment. Case-sensitive like the underlying glob.
func MatchWhenToUse(base string, patterns []string, observedPath string) bool {
	baseRaw := strings.TrimSpace(base)
	if baseRaw == "" || len(patterns) == 0 {
		return false
	}
	base = filepath.Clean(baseRaw)
	observedRaw := strings.TrimSpace(observedPath)
	if observedRaw == "" {
		return false
	}
	observed := filepath.Clean(observedRaw)
	rel, err := filepath.Rel(base, observed)
	if err != nil {
		return false
	}
	// A path outside base (..), or exactly base itself ("."), cannot match a
	// scoped pattern (patterns describe entries under the root).
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return false
	}
	rel = filepath.ToSlash(rel)
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if matchPathGlob(filepath.ToSlash(pattern), rel) {
			return true
		}
	}
	return false
}

// matchPathGlob reports whether a slash-normalized relative path name matches a
// slash-normalized glob pattern supporting `**` (spanning zero or more path
// segments), `*` (within a single segment) and `?` (a single rune). It mirrors
// the glob semantics the tool layer uses, kept local to avoid an
// internal/skills -> internal/tools dependency cycle.
func matchPathGlob(pattern, name string) bool {
	pat := splitGlobTerms(pattern)
	nme := splitGlobTerms(name)
	return matchGlobTerms(pat, nme, 0, 0)
}

func splitGlobTerms(s string) []string {
	s = strings.Trim(s, "/")
	if s == "" {
		return nil
	}
	return strings.Split(s, "/")
}

func matchGlobTerms(pat, nme []string, pi, ni int) bool {
	for pi < len(pat) {
		if pat[pi] == "**" {
			if pi == len(pat)-1 {
				return true // trailing ** matches the rest
			}
			for k := ni; k <= len(nme); k++ {
				if matchGlobTerms(pat, nme, pi+1, k) {
					return true
				}
			}
			return false
		}
		if ni >= len(nme) {
			return false
		}
		if !segGlobMatch(pat[pi], nme[ni]) {
			return false
		}
		pi++
		ni++
	}
	return ni == len(nme)
}

// segGlobMatch matches a single path segment against a pattern term with * and
// ? wildcards using a backtracking star pointer.
func segGlobMatch(pattern, name string) bool {
	pi, ni, starP, starN := 0, 0, -1, 0
	for ni < len(name) {
		if pi < len(pattern) && (pattern[pi] == name[ni] || pattern[pi] == '?') {
			pi++
			ni++
		} else if pi < len(pattern) && pattern[pi] == '*' {
			starP, starN = pi, ni
			pi++
		} else if starP >= 0 {
			pi = starP + 1
			starN++
			ni = starN
		} else {
			return false
		}
	}
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
}
