package agent

import (
	"reflect"
	"testing"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
)

func TestSeedRunMessagesKeepsCurrentPromptAsFinalUserTurn(t *testing.T) {
	initial := []kajicoderuntime.Message{
		{Role: kajicoderuntime.MessageRoleSystem, Content: "stale system"},
		{Role: kajicoderuntime.MessageRoleUser, Content: "previous request"},
		{Role: kajicoderuntime.MessageRoleAssistant, Content: "Want me to apply it?"},
	}

	messages := seedRunMessages("fresh system", "yes", nil, initial)

	if len(messages) != 4 {
		t.Fatalf("expected system, two prior messages, and current prompt; got %#v", messages)
	}
	if messages[0].Role != kajicoderuntime.MessageRoleSystem || messages[0].Content != "fresh system" {
		t.Fatalf("expected fresh system prompt first, got %#v", messages[0])
	}
	if messages[2].Content != "Want me to apply it?" {
		t.Fatalf("expected prior assistant question to be preserved, got %#v", messages[2])
	}
	if messages[3].Role != kajicoderuntime.MessageRoleUser || messages[3].Content != "yes" {
		t.Fatalf("expected current prompt as final user turn, got %#v", messages[3])
	}
}

func TestSeedRunMessagesWithoutInitialHistoryMatchesDefaultSeed(t *testing.T) {
	image := kajicoderuntime.ImageBlock{MediaType: "image/png", Data: []byte("abc")}

	got := seedRunMessages("system", "prompt", []kajicoderuntime.ImageBlock{image}, nil)
	want := kajicoderuntime.SeedMessagesWithImages("system", "prompt", []kajicoderuntime.ImageBlock{image})

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected default seed behavior, got %#v want %#v", got, want)
	}
}

func TestSeedRunMessagesClonesCurrentPromptImages(t *testing.T) {
	image := kajicoderuntime.ImageBlock{MediaType: "image/png", Data: []byte{1, 2, 3}}
	initial := []kajicoderuntime.Message{
		{Role: kajicoderuntime.MessageRoleUser, Content: "prior"},
	}

	messages := seedRunMessages("system", "prompt", []kajicoderuntime.ImageBlock{image}, initial)
	if len(messages) != 3 {
		t.Fatalf("expected system, prior, current prompt; got %#v", messages)
	}
	if len(messages[2].Images) != 1 {
		t.Fatalf("expected current prompt image, got %#v", messages[2])
	}
	// Mutating the source image must not alias into the seeded request.
	image.Data[0] = 99
	if messages[2].Images[0].Data[0] == 99 {
		t.Fatalf("seeded image aliases the caller's image slice: %#v", messages[2].Images)
	}
}
