package agent

import (
	"context"
	"sync"
	"time"

	"github.com/dishant0406/KajiCode/internal/providers/providerio"
)

// Stream-liveness heartbeat: a silent provider stream is indistinguishable
// from a dead one for the user. While a stream is open, a ticker emits
// periodic OnPhase ticks ("waiting for model… 45s") so surfaces can show
// liveness instead of freezing silently. The heartbeat stops on Stop or
// context cancellation.
//
// The heartbeat never touches the conversation or the collected result — it
// only fires OnPhase callbacks (status-only by contract) and is therefore safe
// to run alongside CollectStreamWithOptions forwarding.

// waitingTickInterval is how often the heartbeat fires. 15s keeps the UI
// fresh without spamming the phase sink; every tick carries cumulative
// elapsed time so silence stays quantified. Var (not const) so tests can
// shrink it; production never touches it.
var (
	waitingTickInterval        = 15 * time.Second
	defaultWaitingTickInterval = waitingTickInterval
)

// waitingHeartbeat periodically re-emits the current wait phase with growing
// elapsed time until stopped. kind/baseDetail can be bumped mid-flight (e.g.
// provider_request -> streaming) without resetting the elapsed clock.
type waitingHeartbeat struct {
	stop    chan struct{}
	stopped chan struct{}

	mu         sync.Mutex
	kind       PhaseKind
	baseDetail string
}

// startWaitingHeartbeat launches the ticker goroutine. Nil OnPhase
// short-circuits into a no-op heartbeat whose Stop is still safe to call.
func startWaitingHeartbeat(ctx context.Context, options Options, kind PhaseKind, baseDetail string) *waitingHeartbeat {
	hb := &waitingHeartbeat{
		stop:       make(chan struct{}),
		stopped:    make(chan struct{}),
		kind:       kind,
		baseDetail: baseDetail,
	}
	if options.OnPhase == nil {
		close(hb.stopped)
		return hb
	}
	go func() {
		defer close(hb.stopped)
		started := time.Now()
		ticker := time.NewTicker(waitingTickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-hb.stop:
				return
			case <-ticker.C:
				hb.mu.Lock()
				kind := hb.kind
				base := hb.baseDetail
				hb.mu.Unlock()
				emitPhase(options, kind, heartbeatDetail(base, time.Since(started)))
			}
		}
	}()
	return hb
}

// bump switches the phase being ticked without resetting elapsed time.
func (hb *waitingHeartbeat) bump(kind PhaseKind, baseDetail string) {
	if hb == nil {
		return
	}
	hb.mu.Lock()
	hb.kind = kind
	hb.baseDetail = baseDetail
	hb.mu.Unlock()
}

// Stop halts the ticker and waits for the goroutine to exit, so a test or a
// retry loop never races a late tick against a new phase emission.
func (hb *waitingHeartbeat) Stop() {
	if hb == nil {
		return
	}
	select {
	case <-hb.stop:
	default:
		close(hb.stop)
	}
	<-hb.stopped
}

// heartbeatDetail renders "base… 45s" style progress text.
func heartbeatDetail(base string, elapsed time.Duration) string {
	return base + "… " + elapsed.Round(time.Second).String()
}

// maxTurnSilentBudget bounds how long ONE generation (initial attempt plus any
// stall retry) may stay completely silent before the loop stops re-issuing
// streams and surfaces the timeout error instead. It is a multiple of the
// provider idle watchdog, so raising KAJICODE_STREAM_IDLE_TIMEOUT (or the
// profile's streamIdleTimeoutSeconds) raises this budget with it — slow
// reasoning models keep their room, while the interactive worst case drops
// from "idle × 2 attempts back-to-back" to this single cap.
const turnSilentBudgetMultiplier = 3

// turnSilentBudgetRemaining reports whether another silent-stream attempt may
// still start inside the per-generation budget measured from turnSilentStart.
// ctx cancellation always returns false so an Esc abort wins over the budget.
// A zero/negative time.Time (zero value) is treated as "no measurement yet" and
// allowed — defensive for callers that never set the anchor.
func turnSilentBudgetRemaining(ctx context.Context, turnSilentStart time.Time) bool {
	if ctx.Err() != nil {
		return false
	}
	if turnSilentStart.IsZero() {
		return true
	}
	idle := providerio.ResolveStreamIdleTimeout(0)
	if idle <= 0 {
		// Watchdog disabled: no budget to enforce.
		return true
	}
	budget := idle * time.Duration(turnSilentBudgetMultiplier)
	return time.Since(turnSilentStart) < budget
}
