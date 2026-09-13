package mcp

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// progressMethod is the MCP notification a server sends while a long call is
// still running (carrying the progressToken the client supplied).
const progressMethod = "notifications/progress"

var progressTokenCounter atomic.Int64

// nextProgressToken returns a unique token for a tool call's progress tracking.
func nextProgressToken() string {
	return "kj-" + strconv.FormatInt(progressTokenCounter.Add(1), 10)
}

// progressTimeout resets a tool call's deadline each time the server reports
// progress, so a legitimately long operation (a big build, a slow query) is not
// killed by a fixed timeout while a genuinely hung one still is. On expiry it
// cancels the call with context.DeadlineExceeded so the caller sees the same
// error a normal deadline would produce.
type progressTimeout struct {
	timeout time.Duration
	cancel  context.CancelCauseFunc

	mu    sync.Mutex
	timer *time.Timer
}

func newProgressTimeout(timeout time.Duration, cancel context.CancelCauseFunc) *progressTimeout {
	progress := &progressTimeout{timeout: timeout, cancel: cancel}
	progress.timer = time.AfterFunc(timeout, func() { cancel(context.DeadlineExceeded) })
	return progress
}

// reset restarts the deadline (called on each progress notification).
func (progress *progressTimeout) reset() {
	progress.mu.Lock()
	defer progress.mu.Unlock()
	if progress.timer != nil {
		progress.timer.Reset(progress.timeout)
	}
}

// stop cancels the timer so it cannot fire after the call completed.
func (progress *progressTimeout) stop() {
	progress.mu.Lock()
	defer progress.mu.Unlock()
	if progress.timer != nil {
		progress.timer.Stop()
		progress.timer = nil
	}
}

// progressRegistry tracks the in-flight progress token of each call so a
// progress notification can find and reset the matching timer. It is embedded by
// each transport's client.
type progressRegistry struct {
	mu      sync.Mutex
	pending map[string]*progressTimeout
}

func (registry *progressRegistry) addProgress(token string, progress *progressTimeout) {
	registry.mu.Lock()
	if registry.pending == nil {
		registry.pending = map[string]*progressTimeout{}
	}
	registry.pending[token] = progress
	registry.mu.Unlock()
}

func (registry *progressRegistry) removeProgress(token string) {
	registry.mu.Lock()
	delete(registry.pending, token)
	registry.mu.Unlock()
}

// touchProgress resets the timer for the call identified by a progress
// notification's token. Unknown tokens are ignored.
func (registry *progressRegistry) touchProgress(token string) {
	registry.mu.Lock()
	progress := registry.pending[token]
	registry.mu.Unlock()
	if progress != nil {
		progress.reset()
	}
}

// progressTokenFromParams extracts progressToken from a notifications/progress
// params object.
func progressTokenFromParams(params json.RawMessage) string {
	if len(params) == 0 {
		return ""
	}
	var decoded struct {
		ProgressToken string `json:"progressToken"`
	}
	if err := json.Unmarshal(params, &decoded); err != nil {
		return ""
	}
	return decoded.ProgressToken
}
