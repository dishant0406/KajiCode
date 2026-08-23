package cli

import (
	"time"

	"github.com/dishant0406/KajiCode/internal/config"
	"github.com/dishant0406/KajiCode/internal/providers/providerio"
)

// profileStreamIdleTimeout maps a provider profile's optional
// streamIdleTimeoutSeconds onto the providers.Options duration. 0/omitted keeps
// providerio's own precedence (KAJICODE_STREAM_IDLE_TIMEOUT env, then
// DefaultStreamIdleTimeout), so this helper returns 0 unless the user pinned a
// positive value in config.
//
// It lives beside the other config→provider wiring (app.go newProvider) and is
// deliberately the ONLY place that converts seconds to a duration, so tests can
// pin the conversion contract once.
func profileStreamIdleTimeout(profile config.ProviderProfile) time.Duration {
	if profile.StreamIdleTimeoutSeconds <= 0 {
		return 0
	}
	return time.Duration(profile.StreamIdleTimeoutSeconds) * time.Second
}

// streamIdleTimeoutForProfile reports the EFFECTIVE idle timeout a profile will
// run with: an explicit config value wins; otherwise ResolveStreamIdleTimeout
// applies its env/default precedence. Exposed for diagnostics surfaces (e.g.
// /doctor) that want to display the value without constructing a provider.
func streamIdleTimeoutForProfile(profile config.ProviderProfile) time.Duration {
	if d := profileStreamIdleTimeout(profile); d > 0 {
		return d
	}
	return providerio.ResolveStreamIdleTimeout(0)
}
