package agent

import (
	"strings"
	"testing"
)

func TestSystemPromptPartsUseNamedSections(t *testing.T) {
	parts := buildSystemPromptParts(Options{
		SystemPrompt: "core instructions",
		Skills: []SkillInfo{{
			Name:        "repo-audit",
			Description: "Audit repository structure.",
		}},
	})

	if parts.prompt == "" {
		t.Fatal("prompt must not be empty")
	}
	if parts.baseInstructions != "core instructions" {
		t.Fatalf("baseInstructions = %q", parts.baseInstructions)
	}
	if !strings.Contains(parts.skills, "repo-audit") {
		t.Fatalf("skills diagnostic missing skill list: %q", parts.skills)
	}
	if parts.confirmationPolicy == "" {
		t.Fatal("confirmation policy diagnostic must be retained")
	}
	for _, want := range []promptSectionRole{
		promptSectionBase,
		promptSectionSkills,
		promptSectionConfirmation,
	} {
		if !promptPartsHaveSection(parts, want) {
			t.Fatalf("missing prompt section %q in %#v", want, parts.sections)
		}
	}
}

func promptPartsHaveSection(parts systemPromptParts, role promptSectionRole) bool {
	for _, section := range parts.sections {
		if section.role == role {
			return true
		}
	}
	return false
}
