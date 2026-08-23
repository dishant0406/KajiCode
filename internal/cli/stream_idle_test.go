package cli

import (
	"testing"
	"time"

	"github.com/dishant0406/KajiCode/internal/config"
)

// TestProfileStreamIdleTimeout pins the seconds→duration mapping: 0/omitted
// defers to providerio's own precedence (env/default), a positive value
// converts literally.
func TestProfileStreamIdleTimeout(t *testing.T) {
	if got := profileStreamIdleTimeout(config.ProviderProfile{}); got != 0 {
		t.Fatalf("empty profile = %v, want 0 (defer to env/default)", got)
	}
	if got := profileStreamIdleTimeout(config.ProviderProfile{StreamIdleTimeoutSeconds: -5}); got != 0 {
		t.Fatalf("negative value = %v, want 0 (treated as unset)", got)
	}
	if got := profileStreamIdleTimeout(config.ProviderProfile{StreamIdleTimeoutSeconds: 90}); got != 90*time.Second {
		t.Fatalf("90s profile = %v, want %v", got, 90*time.Second)
	}
}

// TestStreamIdleTimeoutForProfile verifies the effective-timeout resolution:
// explicit config wins over ResolveStreamIdleTimeout's env/default chain.
func TestStreamIdleTimeoutForProfile(t *testing.T) {
	explicit := streamIdleTimeoutForProfile(config.ProviderProfile{StreamIdleTimeoutSeconds: 45})
	if explicit != 45*time.Second {
		t.Fatalf("explicit config = %v, want 45s", explicit)
	}
	fallback := streamIdleTimeoutForProfile(config.ProviderProfile{})
	if fallback <= 0 {
		t.Fatalf("fallback = %v, want a positive default from providerio", fallback)
	}
}
