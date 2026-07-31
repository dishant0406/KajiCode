package tui

import (
	"sync"
	"sync/atomic"
)

var (
	themeRenderMu  sync.RWMutex
	themeRenderGen atomic.Uint64
)

func currentThemeGeneration() uint64 {
	return themeRenderGen.Load()
}

func withThemeReadLock(fn func()) {
	themeRenderMu.RLock()
	defer themeRenderMu.RUnlock()
	fn()
}

func withThemeWriteLock(fn func()) {
	themeRenderMu.Lock()
	defer themeRenderMu.Unlock()
	fn()
	themeRenderGen.Add(1)
}
