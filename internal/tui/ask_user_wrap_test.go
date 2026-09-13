package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/dishant0406/KajiCode/internal/agent"
)

func askUserWrapTestPrompt() pendingAskUserPrompt {
	return pendingAskUserPrompt{
		request: agent.AskUserRequest{
			Questions: []agent.AskUserQuestion{
				{
					Question: "Which of these very long alternative naming schemes should the new flag adopt for the release?",
					Options: []string{
						"Use the short --version flag everywhere for consistency across all platforms",
						"Keep both and alias them",
					},
					OptionDescriptions: []string{
						"A very long description explaining the trade-offs of the short flag across platforms and docs",
					},
				},
			},
		},
		states: []askUserAnswerState{{cursor: 0}},
	}
}

func TestAskUserQuestionnaireWrapsInsteadOfTruncating(t *testing.T) {
	width := 60
	rendered := renderAskUserQuestionnaire(askUserWrapTestPrompt(), "", width)
	plain := plainRender(t, rendered)
	for _, want := range []string{"alternative naming schemes", "across all platforms", "trade-offs"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("wrapped prompt should contain %q, got:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "…") {
		t.Fatalf("wrapped prompt should not truncate with ellipsis, got:\n%s", plain)
	}
	for index, line := range strings.Split(rendered, "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line %d is %d cells wide, want <= %d: %q", index, got, width, line)
		}
	}
}
