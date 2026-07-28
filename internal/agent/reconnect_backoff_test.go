package agent

import (
	"strings"
	"testing"
)

func TestBackoffGrows(t *testing.T) {
	if backoffFor(1) != streamReconnectBase {
		t.Fatalf("attempt 1 backoff = %v, want %v", backoffFor(1), streamReconnectBase)
	}
	if backoffFor(2) != 2*streamReconnectBase {
		t.Fatalf("attempt 2 backoff = %v, want %v", backoffFor(2), 2*streamReconnectBase)
	}
	if got := backoffFor(20); got != streamReconnectMax {
		t.Fatalf("attempt 20 backoff = %v, want cap %v", got, streamReconnectMax)
	}
}

func TestJitteredBackoffStaysInBounds(t *testing.T) {
	for attempt := 1; attempt <= 5; attempt++ {
		base := backoffFor(attempt)
		for i := 0; i < 200; i++ {
			got := jitteredBackoff(attempt)
			if got < base || got > base+base/2 {
				t.Fatalf("attempt %d jittered backoff %v out of [%v, %v]", attempt, got, base, base+base/2)
			}
		}
	}
}

func TestReconnectNoticeRoutesThroughReasoning(t *testing.T) {
	var got string
	notify := reconnectNoticeFor(Options{OnReasoning: func(s string) { got += s }})
	if notify == nil {
		t.Fatal("expected a notifier when OnReasoning is set")
	}
	notify(1, 2)
	if got == "" || !strings.Contains(strings.ToLower(got), "reconnecting 1/2") {
		t.Fatalf("notice = %q, want a reconnecting message", got)
	}
	if reconnectNoticeFor(Options{}) != nil {
		t.Fatal("expected nil notifier when OnReasoning is unset")
	}
}
