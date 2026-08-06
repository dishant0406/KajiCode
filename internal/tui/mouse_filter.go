package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

const mouseEventThrottleInterval = 15 * time.Millisecond

func mouseEventFilter() func(tea.Model, tea.Msg) tea.Msg {
	return newMouseEventFilter(time.Now, mouseEventThrottleInterval)
}

func programMouseEventFilter(send func(tea.Msg)) func(tea.Model, tea.Msg) tea.Msg {
	motionFilter := mouseEventFilter()
	wheelFilter := newTranscriptWheelCoalescer(send)
	return func(model tea.Model, msg tea.Msg) tea.Msg {
		if msg = wheelFilter.filter(model, msg); msg == nil {
			return nil
		}
		return motionFilter(model, msg)
	}
}

func newMouseEventFilter(now func() time.Time, minInterval time.Duration) func(tea.Model, tea.Msg) tea.Msg {
	var lastMotion time.Time
	return func(_ tea.Model, msg tea.Msg) tea.Msg {
		switch msg.(type) {
		case tea.MouseMotionMsg:
			current := now()
			if !lastMotion.IsZero() && current.Sub(lastMotion) < minInterval {
				return nil
			}
			lastMotion = current
		}
		return msg
	}
}
