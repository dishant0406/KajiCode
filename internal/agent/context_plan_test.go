package agent

import (
	"testing"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
)

func TestContextPlanIncludesToolDefinitions(t *testing.T) {
	messages := []kajicoderuntime.Message{
		{Role: kajicoderuntime.MessageRoleUser, Content: filler(400)},
	}
	toolDefs := []kajicoderuntime.ToolDefinition{
		{Name: "read_file", Description: filler(800), Parameters: map[string]any{"type": "object"}},
	}

	plan := planTurnContext(messages, toolDefs, 100, 6, 0)

	if plan.MessageTokens <= 0 || plan.ToolTokens <= 0 {
		t.Fatalf("expected message and tool tokens, got %#v", plan)
	}
	if plan.TotalTokens != plan.MessageTokens+plan.ToolTokens {
		t.Fatalf("TotalTokens = %d, want message+tool %d", plan.TotalTokens, plan.MessageTokens+plan.ToolTokens)
	}
	if !plan.ShouldCompact {
		t.Fatalf("expected plan to compact over threshold: %#v", plan)
	}
}

func TestEffectiveCompactionUsesHarnessProfile(t *testing.T) {
	openWeight := Options{Model: "qwen3-coder"}
	if got := effectiveCompactionPreserveLast(openWeight); got != 10 {
		t.Fatalf("open-weight preserveLast = %d, want 10", got)
	}
	if got := effectiveCompactionTriggerRatio(openWeight); got != 0.64 {
		t.Fatalf("open-weight compaction ratio = %v, want 0.64", got)
	}

	explicit := Options{Model: "qwen3-coder", CompactionPreserveLast: 3}
	if got := effectiveCompactionPreserveLast(explicit); got != 3 {
		t.Fatalf("explicit preserveLast = %d, want 3", got)
	}
}
