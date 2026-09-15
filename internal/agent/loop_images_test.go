package agent

import (
	"bytes"
	"context"
	"testing"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/tools"
)

// imageEchoProvider records the messages of the first request it receives, then
// returns an empty final answer so the loop terminates after one turn.
type imageEchoProvider struct {
	seen []kajicoderuntime.Message
}

func (p *imageEchoProvider) StreamCompletion(ctx context.Context, request kajicoderuntime.CompletionRequest) (<-chan kajicoderuntime.StreamEvent, error) {
	if p.seen == nil {
		p.seen = append([]kajicoderuntime.Message{}, request.Messages...)
	}
	events := make(chan kajicoderuntime.StreamEvent)
	close(events)
	return events, nil
}

func TestRunSeedsImagesIntoUserTurn(t *testing.T) {
	provider := &imageEchoProvider{}
	images := []kajicoderuntime.ImageBlock{{MediaType: "image/png", Data: []byte{0x89, 0x50}}}

	if _, err := Run(context.Background(), "look at this", provider, Options{
		MaxTurns: 1,
		Images:   images,
	}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if len(provider.seen) < 2 {
		t.Fatalf("provider saw %d messages, want >= 2", len(provider.seen))
	}
	user := provider.seen[len(provider.seen)-1]
	if user.Role != kajicoderuntime.MessageRoleUser {
		t.Fatalf("last seeded message role = %q, want user", user.Role)
	}
	if len(user.Images) != 1 || user.Images[0].MediaType != "image/png" {
		t.Fatalf("user.Images = %#v, want one image/png block", user.Images)
	}
}

// TestCopyMessagesDeepCopiesImageBytes locks the anti-aliasing guarantee for
// copyMessages: copies must carry INDEPENDENT image bytes, so mutating the
// source message's Data never bleeds into a history/request/result copy.
func TestCopyMessagesDeepCopiesImageBytes(t *testing.T) {
	source := []Message{
		{
			Role:    kajicoderuntime.MessageRoleUser,
			Content: "look",
			Images: []kajicoderuntime.ImageBlock{
				{MediaType: "image/png", Data: []byte{0x89, 0x50, 0x4e, 0x47}},
			},
		},
	}

	copied := copyMessages(source)
	if len(copied) != 1 || len(copied[0].Images) != 1 {
		t.Fatalf("unexpected copy shape: %#v", copied)
	}

	// Mutating the source bytes must not change the copy.
	source[0].Images[0].Data[0] = 0x00
	if !bytes.Equal(copied[0].Images[0].Data, []byte{0x89, 0x50, 0x4e, 0x47}) {
		t.Fatalf("copy image bytes aliased the source: %v", copied[0].Images[0].Data)
	}
	if &source[0].Images[0].Data[0] == &copied[0].Images[0].Data[0] {
		t.Fatal("copy Data shares backing array with source")
	}
}

func TestRunWithoutImagesSeedsNilImages(t *testing.T) {
	provider := &imageEchoProvider{}
	if _, err := Run(context.Background(), "hello", provider, Options{MaxTurns: 1}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	user := provider.seen[len(provider.seen)-1]
	if user.Images != nil {
		t.Fatalf("user.Images = %#v, want nil for text-only run", user.Images)
	}
}

// imageReturningTool mimics read_file returning a real image part.
type imageReturningTool struct{}

func (imageReturningTool) Name() string        { return "image_returning" }
func (imageReturningTool) Description() string { return "returns an image" }
func (imageReturningTool) Parameters() tools.Schema {
	return tools.Schema{Type: "object", AdditionalProperties: false, Properties: map[string]tools.PropertySchema{}}
}
func (imageReturningTool) Safety() tools.Safety {
	return tools.Safety{SideEffect: tools.SideEffectRead, Permission: tools.PermissionAllow, Reason: "test"}
}
func (imageReturningTool) Run(context.Context, map[string]any) tools.Result {
	return tools.Result{
		Status: tools.StatusOK,
		Output: "Image read successfully: a.png",
		Images: []kajicoderuntime.ImageBlock{{MediaType: "image/png", Data: []byte{0x89, 0x50}}},
	}
}

// TestRunInjectsToolImagesAsUserTurn locks the provider-neutral contract: a
// tool-returned image is delivered to the model as ONE synthetic user turn
// appended AFTER the batch's tool_results, never on the tool message itself
// (no provider serializes images on a tool role) and never between tool_results
// (that breaks strict provider replay).
func TestRunInjectsToolImagesAsUserTurn(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(imageReturningTool{})
	turnOne := append(probeCallEvents("call-1", "image_returning", "a"),
		kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventDone})
	provider := &mockProvider{turns: [][]kajicoderuntime.StreamEvent{
		turnOne,
		{{Type: kajicoderuntime.StreamEventText, Content: "done"}, {Type: kajicoderuntime.StreamEventDone}},
	}}

	if _, err := Run(context.Background(), "look", provider, Options{Registry: registry}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(provider.requests) < 2 {
		t.Fatalf("expected a second request, got %d", len(provider.requests))
	}
	messages := provider.requests[1].Messages

	// Every tool message must carry no images.
	for _, message := range messages {
		if message.Role == kajicoderuntime.MessageRoleTool && len(message.Images) != 0 {
			t.Fatalf("tool message carried images: %#v", message.Images)
		}
	}

	// The synthetic user turn must come after the tool result and hold the image.
	toolIndex, userIndex := -1, -1
	for i, message := range messages {
		if message.Role == kajicoderuntime.MessageRoleTool && message.ToolCallID == "call-1" {
			toolIndex = i
		}
		if message.Role == kajicoderuntime.MessageRoleUser && len(message.Images) > 0 {
			userIndex = i
		}
	}
	if userIndex == -1 {
		t.Fatalf("no synthetic user turn with images found: %#v", messages)
	}
	if toolIndex == -1 || userIndex < toolIndex {
		t.Fatalf("image turn (index %d) must follow the tool result (index %d)", userIndex, toolIndex)
	}
	if got := messages[userIndex].Images[0].MediaType; got != "image/png" {
		t.Fatalf("injected media type = %q, want image/png", got)
	}
}
