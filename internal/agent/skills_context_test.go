package agent

import (
	"strconv"
	"strings"
	"testing"
)

// 60 skills with realistic trigger-rich descriptions must ALL be listed. The old
// 4096-byte budget summarized skills past the cap as "…and N more", making them
// invisible to the model — the catalog is the model's ONLY discovery surface, so
// it is deliberately unbudgeted (matching opencode's skill guidance).
func TestSkillsContextListsLargeSetInFull(t *testing.T) {
	longDesc := strings.Repeat("use when the request touches deployments, release notes, or verification; ", 2) + "number "
	skills := make([]SkillInfo, 0, 60)
	for i := 0; i < 60; i++ {
		n := strconv.Itoa(i)
		skills = append(skills, SkillInfo{Name: "skill-" + n, Description: longDesc + n})
	}
	got := skillsContext(Options{Skills: skills})
	if strings.Contains(got, "more (call skill") {
		t.Fatalf("overflow summary must not appear; every skill must be listed, got:\n%.400s", got)
	}
	for i := 0; i < 60; i++ {
		if !strings.Contains(got, "- skill-"+strconv.Itoa(i)+":") {
			t.Fatalf("skill-%d missing from the list", i)
		}
	}
}

// A realistic mid-size skill set (20 skills, trigger-rich descriptions) must be
// listed in FULL — no overflow summary. This pins the fix for the old 640-byte
// budget, under which skills past ~#6 were invisible to the model and therefore
// never triggered.
func TestSkillsContextListsRealisticSetInFull(t *testing.T) {
	desc := "Use when the user asks about deployments, release notes, or pre-merge verification runs."
	skills := make([]SkillInfo, 0, 20)
	for i := 0; i < 20; i++ {
		skills = append(skills, SkillInfo{Name: "skill-" + strconv.Itoa(i), Description: desc})
	}
	got := skillsContext(Options{Skills: skills})
	if strings.Contains(got, "more (call skill") {
		t.Fatalf("20 described skills must all be listed without overflow, got:\n%s", got)
	}
	for i := 0; i < 20; i++ {
		if !strings.Contains(got, "- skill-"+strconv.Itoa(i)+":") {
			t.Fatalf("skill-%d missing from the list:\n%s", i, got)
		}
	}
}

func TestSkillsContext(t *testing.T) {
	if got := skillsContext(Options{}); got != "" {
		t.Fatalf("no skills should yield an empty section, got %q", got)
	}
	got := skillsContext(Options{Skills: []SkillInfo{
		{Name: "commit-writer", Description: "Write a conventional-commit message."},
		{Name: "  ", Description: "nameless, should be skipped"},
		{Name: "reviewer"},
	}})
	if !strings.Contains(got, "<available_skills>") || !strings.Contains(got, "</available_skills>") {
		t.Fatalf("missing available_skills block: %q", got)
	}
	if !strings.Contains(got, "- commit-writer: Write a conventional-commit message.") {
		t.Fatalf("missing commit-writer line: %q", got)
	}
	if !strings.Contains(got, "- reviewer\n") {
		t.Fatalf("reviewer (no description) line missing: %q", got)
	}
	if strings.Contains(got, "nameless") {
		t.Fatalf("nameless entry should be skipped: %q", got)
	}
}

func TestSystemPromptIncludesSkillsOnlyWhenInstalled(t *testing.T) {
	with := buildSystemPrompt(Options{Skills: []SkillInfo{
		{Name: "commit-writer", Description: "Write a commit message."},
	}})
	if !strings.Contains(with, "<available_skills>") || !strings.Contains(with, "skill tool") {
		t.Fatalf("expected available_skills guidance in system prompt: %q", with)
	}
	// Default (no skills) must reproduce the prior prompt: no skills block.
	without := buildSystemPrompt(Options{})
	if strings.Contains(without, "<available_skills>") {
		t.Fatalf("available_skills block must not appear without skills")
	}
}

func TestSkillsContextSurfacesPermissionMarkers(t *testing.T) {
	skills := []SkillInfo{
		{Name: "guard-skill", Description: "needs care", Permission: "prompt"},
		{Name: "secret-skill", Description: "restricted", Permission: "deny"},
		{Name: "open-skill", Description: "fine", Permission: "allow"},
		{Name: "legacy-skill", Description: "no permission"},
	}
	got := skillsContext(Options{Skills: skills})
	if !strings.Contains(got, "guard-skill") || !strings.Contains(got, "[prompt]") {
		t.Errorf("prompt permission marker missing:\n%s", got)
	}
	if !strings.Contains(got, "secret-skill") || !strings.Contains(got, "[deny]") {
		t.Errorf("deny permission marker missing:\n%s", got)
	}
	if strings.Contains(got, "open-skill [") || strings.Contains(got, "legacy-skill [") {
		t.Errorf("allow/empty skills must carry no permission marker:\n%s", got)
	}
}
