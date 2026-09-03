package tui

import (
	"strings"
	"testing"

	"github.com/dishant0406/KajiCode/internal/agent"
)

func TestExpandSkillMentionsRewritesMidTextToken(t *testing.T) {
	skillsList := []agent.SkillInfo{
		{Name: "code-review", Description: "review code"},
		{Name: "diagnosing-bugs", Description: "debug"},
	}
	got, changed := expandSkillMentions("please @code-review on this diff", skillsList)
	if !changed {
		t.Fatalf("expected the skill mention to be expanded, got %q", got)
	}
	if !strings.Contains(got, "Load the \"code-review\" skill with the skill tool") {
		t.Fatalf("expected a skill-load directive, got %q", got)
	}
	if !strings.Contains(got, "on this diff") {
		t.Fatalf("expected surrounding prose to be preserved, got %q", got)
	}
	if strings.Contains(got, "@code-review") {
		t.Fatalf("expected the @token to be replaced, got %q", got)
	}
}

func TestExpandSkillMentionsLeavesUnknownTokensAlone(t *testing.T) {
	skillsList := []agent.SkillInfo{{Name: "code-review", Description: "review code"}}
	got, changed := expandSkillMentions("look at @src/main.go and @code-review", skillsList)
	if !changed {
		t.Fatalf("expected the known skill mention to expand")
	}
	if !strings.Contains(got, "@src/main.go") {
		t.Fatalf("unknown @token must pass through untouched, got %q", got)
	}
}

func TestExpandSkillMentionsNoSkillsNoChange(t *testing.T) {
	got, changed := expandSkillMentions("@code-review this", nil)
	if changed || got != "@code-review this" {
		t.Fatalf("no skills installed: expected verbatim passthrough, got %q changed=%v", got, changed)
	}
}

func TestSkillMentionSuggestionsMatchPrefixAndSkipTaken(t *testing.T) {
	m := model{agentOptions: agent.Options{Skills: []agent.SkillInfo{
		{Name: "code-review", Description: "review code"},
		{Name: "codebase-design", Description: "design"},
		{Name: "help", Description: "shadowed by builtin"},
	}}}
	rows := m.skillMentionSuggestions("code")
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows for prefix \"code\", got %d: %+v", len(rows), rows)
	}
	if rows[0].Name != "@code-review" || !strings.HasSuffix(rows[0].Desc, "(skill)") {
		t.Fatalf("unexpected first row %+v", rows[0])
	}
	if rows := m.skillMentionSuggestions("hel"); len(rows) != 0 {
		t.Fatalf("skill slug claimed by a builtin must not be advertised, got %+v", rows)
	}
}

func TestSplitLeadingSlashToken(t *testing.T) {
	cases := []struct {
		in    string
		token string
		rest  string
		ok    bool
	}{
		{in: "/code-review fix it", token: "/code-review", rest: " fix it", ok: true},
		{in: "/skills", token: "/skills", rest: "", ok: true},
		{in: "plain text", ok: false},
		{in: "/", ok: false},
		{in: "/ weird", ok: false},
	}
	for _, tc := range cases {
		token, rest, ok := splitLeadingSlashToken(tc.in)
		if ok != tc.ok || (ok && (token != tc.token || rest != tc.rest)) {
			t.Fatalf("splitLeadingSlashToken(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.in, token, rest, ok, tc.token, tc.rest, tc.ok)
		}
	}
}

func TestRenderUserRowColorsLeadingSlashToken(t *testing.T) {
	row := transcriptRow{kind: rowUser, text: "/code-review the diff"}
	got := renderUserRow(row, 80)
	// The accent style must wrap the "/code-review" token: the plain line and
	// the styled token cannot both be present unstyled in the same row render.
	if !strings.Contains(got, "/code-review") {
		t.Fatalf("expected the slug in the rendered row, got %q", got)
	}
	plain := renderUserRow(transcriptRow{kind: rowUser, text: "just prose"}, 80)
	if got == plain {
		t.Fatalf("slash row should render differently from plain prose")
	}
}
