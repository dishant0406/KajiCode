package agent

import (
	"context"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
)

// sessionProvider adapts a TurnSession back to the Provider interface so the
// loop's existing Provider-shaped call sites (streamWithReconnect, the
// compaction summarizer, finalAnswerAfterMaxTurns) route their stream I/O
// through the session without any signature change. For the default adapter
// this reduces to the wrapped provider's StreamCompletion, so behavior is
// byte-identical; an optimized session (PR8) takes effect here transparently.
type sessionProvider struct {
	session kajicoderuntime.TurnSession
}

func (s sessionProvider) StreamCompletion(ctx context.Context, request kajicoderuntime.CompletionRequest) (<-chan kajicoderuntime.StreamEvent, error) {
	return s.session.Stream(ctx, request)
}
