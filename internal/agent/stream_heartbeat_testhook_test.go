package agent

import (
	"sync"
	"time"
)

// Test-only hook for the heartbeat cadence. Production reads
// waitingTickInterval directly; tests shrink it via this setter so heartbeat
// assertions run in milliseconds. Kept in a _test file so no test knob leaks
// into the production binary.
var intervalMu sync.Mutex

func setWaitingTickIntervalForTest(d time.Duration) {
	intervalMu.Lock()
	defer intervalMu.Unlock()
	waitingTickInterval = d
}
