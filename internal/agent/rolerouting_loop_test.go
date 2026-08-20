package agent

import (
	"context"
	"testing"

	"github.com/dishant0406/KajiCode/internal/config"
	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/tools"
)

// TestRunRoutesToRoleOnTurnZero verifies that with RoleRouting wired and an
// explicit role selected, the loop swaps the run's provider to the role's profile
// before the first request, and stays there (one swap total).
func TestRunRoutesToRoleOnTurnZero(t *testing.T) {
	registry := tools.NewRegistry()

	baseProvider := &mockProvider{turns: [][]kajicoderuntime.StreamEvent{}}
	roleProvider := &mockProvider{turns: [][]kajicoderuntime.StreamEvent{
		{
			{Type: kajicoderuntime.StreamEventText, Content: "answered by role model"},
			{Type: kajicoderuntime.StreamEventDone},
		},
	}}

	switches := 0
	result, err := Run(context.Background(), "go", baseProvider, Options{
		Registry: registry,
		Model:    "claude-sonnet-4.5",
		MaxTurns: 3,
		RoleRouting: &RoleRouting{
			Current: func(_ context.Context, role string) (Provider, config.ProviderProfile, bool) {
				if role == "implement" {
					switches++
					return roleProvider, config.ProviderProfile{Model: "gpt-4.1", Name: "openai"}, true
				}
				return baseProvider, config.ProviderProfile{Model: "claude-sonnet-4.5"}, true
			},
			RoleFor: func(_ RoleContext) string { return "implement" },
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if switches != 1 {
		t.Fatalf("expected one provider swap, got %d", switches)
	}
	// The base provider saw 0 requests; the role provider served the run.
	if len(baseProvider.requests) != 0 {
		t.Fatalf("expected base provider to serve 0 requests, got %d", len(baseProvider.requests))
	}
	if len(roleProvider.requests) == 0 {
		t.Fatal("expected the role provider to serve the run")
	}
	if result.FinalAnswer != "answered by role model" {
		t.Fatalf("expected final answer from the role provider, got %q", result.FinalAnswer)
	}
}

// TestRunRoutesOnlyWhenRoleDiffers verifies that once the role is set, no further
// swaps occur on subsequent turns.
func TestRunRoutesOnlyWhenRoleDiffers(t *testing.T) {
	registry := tools.NewRegistry()
	baseProvider := &mockProvider{turns: [][]kajicoderuntime.StreamEvent{}}
	roleProvider := &mockProvider{turns: [][]kajicoderuntime.StreamEvent{
		{
			{Type: kajicoderuntime.StreamEventText, Content: "role turn"},
			{Type: kajicoderuntime.StreamEventDone},
		},
	}}
	switches := 0
	_, err := Run(context.Background(), "go", baseProvider, Options{
		Registry: registry,
		Model:    "claude-sonnet-4.5",
		MaxTurns: 4,
		RoleRouting: &RoleRouting{
			Current: func(_ context.Context, role string) (Provider, config.ProviderProfile, bool) {
				switches++
				return roleProvider, config.ProviderProfile{Model: "gpt-4.1", Name: "openai"}, true
			},
			RoleFor: func(_ RoleContext) string { return "implement" },
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if switches != 1 {
		t.Fatalf("expected exactly one role-provider build (role unchanged after swap), got %d", switches)
	}
}

// TestRunRoleRoutingOffLeavesLoopByteIdentical verifies a nil RoleRouting never
// invokes Current and the run stays on the base provider.
func TestRunRoleRoutingOffLeavesLoopByteIdentical(t *testing.T) {
	registry := tools.NewRegistry()
	provider := &mockProvider{turns: [][]kajicoderuntime.StreamEvent{
		{
			{Type: kajicoderuntime.StreamEventText, Content: "done on base"},
			{Type: kajicoderuntime.StreamEventDone},
		},
	}}
	_, err := Run(context.Background(), "go", provider, Options{
		Registry: registry,
		Model:    "claude-sonnet-4.5",
		MaxTurns: 3,
		// RoleRouting intentionally nil.
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("expected base provider to serve the single turn, got %d requests", len(provider.requests))
	}
}

// TestRunVisionRoleRoutesAndSwapsBack verifies the per-message vision seam: when a
// RoleFor turns that carry an image to "vision", the loop routes turn 0 to the
// vision provider and swaps BACK to the default model on the next (image-less)
// turn. This is the exact contract the TUI's per-message vision auto-routing relies
// on.
func TestRunVisionRoleRoutesAndSwapsBack(t *testing.T) {
	registry := tools.NewRegistry()
	root := t.TempDir()
	registry.Register(tools.NewReadFileTool(root))
	baseProvider := &mockProvider{turns: [][]kajicoderuntime.StreamEvent{
		{ // turn 1: base model answers after the vision turn's tool call
			{Type: kajicoderuntime.StreamEventText, Content: "base swapped-back turn"},
			{Type: kajicoderuntime.StreamEventDone},
		},
	}}
	visionProvider := &mockProvider{turns: [][]kajicoderuntime.StreamEvent{
		{ // turn 0: image turn routed to vision, which calls a tool (forces turn 1)
			{Type: kajicoderuntime.StreamEventToolCallStart, ToolCallID: "call_1", ToolName: "read_file"},
			{Type: kajicoderuntime.StreamEventToolCallDelta, ToolCallID: "call_1", ArgumentsFragment: `{"path":"README.md"}`},
			{Type: kajicoderuntime.StreamEventToolCallEnd, ToolCallID: "call_1"},
			{Type: kajicoderuntime.StreamEventDone},
		},
	}}

	beforeModel := "claude-sonnet-4.5"
	result, err := Run(context.Background(), "go", baseProvider, Options{
		Registry: registry,
		Model:    beforeModel,
		MaxTurns: 4,
		Images:   []kajicoderuntime.ImageBlock{{MediaType: "image/png", Data: []byte("a")}},
		RoleRouting: &RoleRouting{
			Current: func(_ context.Context, role string) (Provider, config.ProviderProfile, bool) {
				if role == "vision" {
					return visionProvider, config.ProviderProfile{Model: "gpt-5.6-luna"}, true
				}
				return baseProvider, config.ProviderProfile{Model: beforeModel}, true
			},
			RoleFor: func(ctx RoleContext) string {
				if ctx.HasImages {
					return "vision"
				}
				return ""
			},
			ContextWindowFor: func(_ config.ProviderProfile) int { return 0 },
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Turn 0 routed to the vision provider; the next turn swapped back to base.
	if len(visionProvider.requests) != 1 {
		t.Fatalf("expected the vision provider to serve exactly the image turn, got %d", len(visionProvider.requests))
	}
	if len(baseProvider.requests) != 1 {
		t.Fatalf("expected the base provider to serve exactly the swapped-back turn, got %d", len(baseProvider.requests))
	}
	if result.FinalAnswer != "base swapped-back turn" {
		t.Fatalf("expected the final answer from the base (swapped-back) provider, got %q", result.FinalAnswer)
	}
}
