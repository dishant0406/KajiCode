package agent

import (
	"bytes"
	"context"
	"testing"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
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
