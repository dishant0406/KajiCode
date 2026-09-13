package mcp

import (
	"path"
	"strings"
)

// ToolFilter allow/deny-lists a server's tools by name glob. Names are matched
// against the tool's REMOTE name (as the server advertises it), not the
// synthesized `mcp_<server>_<tool>` name, because that is what the user sees in
// the server's own docs and the `kajicode mcp tools` output.
//
// Rules, kept deliberately simple:
//   - An empty filter allows every tool.
//   - If Allow is non-empty, a tool must match one Allow pattern to be exposed.
//   - Deny always removes matching tools, EXCEPT a name that also matches Allow,
//     so an explicit allow can carve a tool out of a broad deny.
type ToolFilter struct {
	Allow []string
	Deny  []string
}

// Empty reports whether the filter imposes no restriction.
func (filter ToolFilter) Empty() bool {
	return len(filter.Allow) == 0 && len(filter.Deny) == 0
}

// Allows reports whether a tool with the given remote name is exposed.
func (filter ToolFilter) Allows(toolName string) bool {
	if filter.Empty() {
		return true
	}
	// An explicit Allow beats a Deny, so a user can carve a tool out of a broad
	// deny (e.g. deny "*" but allow "search_*").
	allowed := matchesAny(filter.Allow, toolName)
	if matchesAny(filter.Deny, toolName) && !allowed {
		return false
	}
	if len(filter.Allow) > 0 && !allowed {
		return false
	}
	return true
}

func matchesAny(patterns []string, name string) bool {
	for _, pattern := range patterns {
		if matchGlob(pattern, name) {
			return true
		}
	}
	return false
}

// matchGlob matches a tool name against a glob pattern. Matching is
// case-insensitive and `*` spans any run of characters; because tool namespaces
// commonly use `/` (e.g. `slack/search`), the separator is NOT treated as a path
// boundary — `search/*` and `search*` behave the same. A pattern with no `*`
// matches its exact name. Matching is done on the lowercased value so a user
// does not have to remember the server's capitalization.
func matchGlob(pattern string, name string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	name = strings.ToLower(strings.TrimSpace(name))
	if pattern == "" {
		return false
	}
	if !strings.Contains(pattern, "*") {
		return pattern == name
	}
	// path.Match treats `*` as "any run except /", so translate the pattern into
	// one where `*` spans everything: replace `*` with a sentinel that path.Match
	// treats as an ordinary character, then do a manual wildcard match.
	ok, err := path.Match(pattern, name)
	if err == nil && ok {
		return true
	}
	return wildcardMatch(pattern, name)
}

// wildcardMatch is a tiny non-recursive `*` matcher: it walks the name once,
// remembering the last `*` so it can retry. It exists because path.Match refuses
// to let `*` cross `/`, and tool names may contain `/`.
func wildcardMatch(pattern string, name string) bool {
	p, n := 0, 0
	star, mark := -1, 0
	for n < len(name) {
		switch {
		case p < len(pattern) && pattern[p] == name[n]:
			p++
			n++
		case p < len(pattern) && pattern[p] == '*':
			star = p
			mark = n
			p++
		case star >= 0:
			p = star + 1
			mark++
			n = mark
		default:
			return false
		}
	}
	for p < len(pattern) && pattern[p] == '*' {
		p++
	}
	return p == len(pattern)
}
