package tui

import (
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
)

const (
	transcriptWheelCoalesceInterval = 16 * time.Millisecond
	transcriptWheelMaxLinesPerFrame = chatWheelScrollLines * 6
)

type chatWheelScrollMsg struct {
	delta int
	x     int
	y     int
}

type transcriptWheelCoalescer struct {
	send      func(tea.Msg)
	afterFunc func(func()) coalesceTimer

	mu           sync.Mutex
	delta        int
	x            int
	y            int
	timer        coalesceTimer
	stopped      bool
	targetCached bool
	targetKey    transcriptWheelTargetKey
	targetRect   tuiRect
}

func newTranscriptWheelCoalescer(send func(tea.Msg)) *transcriptWheelCoalescer {
	return &transcriptWheelCoalescer{
		send: send,
		afterFunc: func(fn func()) coalesceTimer {
			return time.AfterFunc(transcriptWheelCoalesceInterval, fn)
		},
	}
}

func (c *transcriptWheelCoalescer) filter(teaModel tea.Model, msg tea.Msg) tea.Msg {
	switch typed := msg.(type) {
	case tea.MouseWheelMsg:
		if !c.shouldCoalesceTranscriptWheel(teaModel, typed) {
			return msg
		}
		c.add(transcriptWheelDelta(typed), typed.Mouse().X, typed.Mouse().Y)
		return nil
	default:
		if transcriptWheelCancelMessage(msg) {
			c.cancel()
		}
		return msg
	}
}

func (c *transcriptWheelCoalescer) shouldCoalesceTranscriptWheel(teaModel tea.Model, msg tea.MouseWheelMsg) bool {
	if transcriptWheelDelta(msg) == 0 {
		return false
	}
	m, ok := teaModel.(model)
	if !ok || !m.mouseCapture || m.mouseReleased || m.transcriptHitTestBlocked() {
		return false
	}
	key := transcriptWheelTargetKeyForModel(m)
	c.mu.Lock()
	if c.targetCached && c.targetKey == key {
		rect := c.targetRect
		c.mu.Unlock()
		return !rect.contains(mouseX(msg), mouseY(msg))
	}
	c.mu.Unlock()

	rect := m.composerMouseRect()
	c.mu.Lock()
	c.targetCached = true
	c.targetKey = key
	c.targetRect = rect
	c.mu.Unlock()
	return !rect.contains(mouseX(msg), mouseY(msg))
}

func transcriptWheelDelta(msg tea.MouseWheelMsg) int {
	switch msg.Mouse().Button {
	case tea.MouseWheelUp:
		return chatWheelScrollLines
	case tea.MouseWheelDown:
		return -chatWheelScrollLines
	default:
		return 0
	}
}

func transcriptWheelCancelMessage(msg tea.Msg) bool {
	switch msg.(type) {
	case tea.KeyMsg, tea.PasteMsg, tea.MouseClickMsg, tea.MouseReleaseMsg:
		return true
	default:
		return false
	}
}

func (c *transcriptWheelCoalescer) add(delta int, x int, y int) {
	if delta == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if sameSign(c.delta, delta) {
		c.delta += delta
	} else {
		c.delta = delta
	}
	c.stopped = false
	c.delta = clampInt(c.delta, -transcriptWheelMaxLinesPerFrame, transcriptWheelMaxLinesPerFrame)
	c.x = x
	c.y = y
	if c.timer == nil {
		c.timer = c.afterFunc(c.flush)
	}
}

func (c *transcriptWheelCoalescer) cancel() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.delta = 0
	c.stopped = true
	c.targetCached = false
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
}

func (c *transcriptWheelCoalescer) flush() {
	c.mu.Lock()
	if c.delta == 0 || c.stopped {
		c.timer = nil
		c.stopped = false
		c.mu.Unlock()
		return
	}
	msg := chatWheelScrollMsg{delta: c.delta, x: c.x, y: c.y}
	c.delta = 0
	c.timer = nil
	send := c.send
	c.mu.Unlock()

	if send != nil {
		send(msg)
	}
}

func sameSign(a int, b int) bool {
	return a == 0 || b == 0 || (a > 0) == (b > 0)
}
