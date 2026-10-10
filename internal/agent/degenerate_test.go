package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/tools"
)

// repeatingTurn streams the same few phrases over and over inside ONE turn, the
// way a model stuck in a loop does. The per-turn guards only see finished turns,
// so this turn must be cut off while it streams.
func repeatingTurn(channel kajicoderuntime.StreamEventType) []kajicoderuntime.StreamEvent {
	events := make([]kajicoderuntime.StreamEvent, 0, 61)
	for i := 0; i < 60; i++ {
		events = append(events, kajicoderuntime.StreamEvent{Type: channel, Content: "Go.\nWriting.\nOK.\n"})
	}
	return append(events, kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventDone})
}

func TestRunStopsAModelThatKeepsRepeatingItself(t *testing.T) {
	for name, channel := range map[string]kajicoderuntime.StreamEventType{
		"reasoning": kajicoderuntime.StreamEventReasoning,
		"text":      kajicoderuntime.StreamEventText,
	} {
		t.Run(name, func(t *testing.T) {
			turns := make([][]kajicoderuntime.StreamEvent, 64)
			for i := range turns {
				turns[i] = repeatingTurn(channel)
			}
			provider := &mockProvider{turns: turns}
			var samples []string

			result, err := Run(context.Background(), "go", provider, Options{
				Registry:         tools.NewRegistry(),
				MaxTurns:         20,
				OnDegenerateTurn: func(sample string) { samples = append(samples, sample) },
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(provider.requests) != maxNoActionTurns {
				t.Fatalf("expected the run to stop after %d turns, got %d", maxNoActionTurns, len(provider.requests))
			}
			if !strings.Contains(result.FinalAnswer, degenerateStopMarker) || !IsNoProgressStop(result.FinalAnswer) {
				t.Fatalf("expected a repeating-output stop message, got %q", result.FinalAnswer)
			}
			if len(samples) != maxNoActionTurns || !strings.Contains(samples[0], "Writing.") {
				t.Fatalf("expected a sample per cut-off turn, got %q", samples)
			}
			for _, message := range result.Messages {
				if strings.Contains(message.Content, "Writing.") {
					t.Fatalf("repeated output must not enter the history: %q", message.Content)
				}
			}
		})
	}
}

func TestRunRecoversAfterARepeatingTurn(t *testing.T) {
	provider := &mockProvider{turns: [][]kajicoderuntime.StreamEvent{
		repeatingTurn(kajicoderuntime.StreamEventReasoning),
		textTurn("here is the answer"),
	}}

	result, err := Run(context.Background(), "go", provider, Options{Registry: tools.NewRegistry(), MaxTurns: 20})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "here is the answer" {
		t.Fatalf("expected the run to recover, got %q", result.FinalAnswer)
	}
	last := provider.requests[1].Messages
	if got := last[len(last)-1].Content; got != degenerateTurnNotice {
		t.Fatalf("expected the model to be told its output was cut off, got %q", got)
	}
}
