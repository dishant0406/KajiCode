package skills

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestMatchWhenToUse(t *testing.T) {
	base := filepath.Join(strings.ReplaceAll(t.TempDir(), "\\", "/"))

	cases := []struct {
		name     string
		patterns []string
		path     string
		want     bool
	}{
		{"empty patterns", nil, "internal/agent/loop.go", false},
		{"no base", []string{"**"}, "", false},
		{"direct glob match", []string{"docs/**"}, "docs/architecture.md", true},
		{"nested glob", []string{"docs/**"}, "docs/sub/deep/readme.md", true},
		{"single star segment", []string{"internal/*/loop.go"}, "internal/agent/loop.go", true},
		{"single star wrong seg", []string{"internal/*/loop.go"}, "internal/agent/sub/loop.go", false},
		{"multi pattern hits second", []string{"cmd/**", "internal/agent/**"}, "internal/agent/types.go", true},
		{"bare filename glob", []string{"README.md"}, "README.md", true},
		{"scoped to subdir-not-matched", []string{"docs/**"}, "internal/tools/skill.go", false},
		{"outside base", []string{"docs/**"}, filepath.Join(filepath.Dir(base), "other.txt"), false},
		{"exact base cannot match", []string{"docs/**"}, base, false},
		{"question wildcard", []string{"docs/readme?.md"}, "docs/readme1.md", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			observed := tc.path
			if !filepath.IsAbs(observed) {
				observed = filepath.Join(base, observed)
			}
			if got := MatchWhenToUse(base, tc.patterns, observed); got != tc.want {
				t.Errorf("MatchWhenToUse(%q) = %v, want %v", tc.patterns, got, tc.want)
			}
		})
	}
}

func TestMatchPathGlob(t *testing.T) {
	cases := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"**/*.go", "foo.go", true},
		{"**/*.go", "a/b/foo.go", true},
		{"**/*.go", "a/b/foo.txt", false},
		{"docs/**", "docs", true},
		{"docs/**", "docs/x", true},
		{"**", "anything/at/all", true},
		{"a/?c", "a/bc", true},
		{"a/?c", "a/abc", false},
		{"a/**/b", "a/b", true},
		{"a/**/b", "a/x/y/b", true},
		{"a/**/b", "a/x/b", true},
		{"a/**/b", "a/x/c", false},
	}
	for _, tc := range cases {
		if got := matchPathGlob(tc.pattern, tc.name); got != tc.want {
			t.Errorf("matchPathGlob(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}
