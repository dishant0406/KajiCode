package agent

import (
	"strings"
	"testing"
)

// The agent roster and delegation guidance live in the Task tool's description,
// never in the system prompt. This guards the single-source invariant: the system
// prompt must be identical whether or not agents are registered.
func TestSystemPromptHasNoAgentDelegationBlock(t *testing.T) {
	with := buildSystemPrompt(Options{Agents: []AgentInfo{
		{Name: "explorer", WhenToUse: "Read-only codebase exploration."},
	}})
	for _, banned := range []string{"<specialists>", "</specialists>", "Available specialists"} {
		if strings.Contains(with, banned) {
			t.Fatalf("system prompt must not contain agent roster text %q", banned)
		}
	}
	if without := buildSystemPrompt(Options{}); with != without {
		t.Fatal("system prompt must be identical with and without registered agents")
	}
}
