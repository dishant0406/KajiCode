package config

import (
	"encoding/json"
)

// LearningConfig tunes KajiCode's self-learning (perpetual memory) loop. It
// mirrors prime-agent's autoRefine settings adapted to KajiCode.
//
// Enabled turns on automatic learning. Auto-learning is on by default.
// TurnInterval triggers a learning review every N assistant turns; the default
// is 10. Compact triggers a review after a context compaction. CooldownMs is
// the minimum gap between reviews; a failed review or apply also stamps the
// cooldown so a buggy/edge state cannot burn provider budget in a tight loop.
type LearningConfig struct {
	Enabled      *bool `json:"enabled,omitempty"`
	TurnInterval int   `json:"turnInterval,omitempty"`
	Compact      *bool `json:"compact,omitempty"`
	CooldownMs   int64 `json:"cooldownMs,omitempty"`

	// enabledSet distinguishes an explicit false from an unset field so user
	// config merge can override a default-on value. Not persisted.
	enabledSet  bool
	compactSet  bool
	turnSet     bool
	cooldownSet bool
}

const (
	AutoLearnTurnIntervalDefault = 10
	AutoLearnCooldownMsDefault   = int64(20 * 60 * 1000) // 20 minutes
)

// DefaultLearningConfig returns the production defaults: auto-learning on,
// 10-turn interval, compact on, 20-minute cooldown.
func DefaultLearningConfig() LearningConfig {
	enabled := true
	compact := true
	return LearningConfig{
		Enabled:      &enabled,
		TurnInterval: AutoLearnTurnIntervalDefault,
		Compact:      &compact,
		CooldownMs:   AutoLearnCooldownMsDefault,
	}
}

// IsEnabled reports whether auto-learning is on. nil (unset) means enabled.
func (cfg LearningConfig) IsEnabled() bool {
	return cfg.Enabled == nil || *cfg.Enabled
}

// IsCompactEnabled reports whether auto-learning runs after compaction.
func (cfg LearningConfig) IsCompactEnabled() bool {
	return cfg.Compact == nil || *cfg.Compact
}

// Effective returns the config with defaults applied and ranges clamped, for
// resolved runs. It leaves the receiver unchanged and returns a copy.
func (cfg LearningConfig) Effective() LearningConfig {
	def := DefaultLearningConfig()
	if cfg.Enabled == nil {
		cfg.Enabled = def.Enabled
	}
	if cfg.Compact == nil {
		cfg.Compact = def.Compact
	}
	if cfg.TurnInterval <= 0 {
		cfg.TurnInterval = def.TurnInterval
	}
	if cfg.CooldownMs <= 0 {
		cfg.CooldownMs = def.CooldownMs
	}
	return cfg
}

// Empty reports whether the config changes no default behavior. Used by
// MarshalJSON to omit an absent block exactly like HarnessConfig.Empty.
func (cfg LearningConfig) Empty() bool {
	return !cfg.enabledSet && !cfg.compactSet && !cfg.turnSet && !cfg.cooldownSet
}

// UnmarshalJSON distinguishes an explicitly-declared field from an absent one
// so merges can correctly override the default-on behavior. Mirrors the
// LocalControl/STT enable-gate pattern already in this package.
func (cfg *LearningConfig) UnmarshalJSON(data []byte) error {
	type rawLearning struct {
		Enabled      *bool  `json:"enabled"`
		TurnInterval *int   `json:"turnInterval"`
		Compact      *bool  `json:"compact"`
		CooldownMs   *int64 `json:"cooldownMs"`
	}
	var raw rawLearning
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*cfg = LearningConfig{}
	if raw.Enabled != nil {
		cfg.Enabled = raw.Enabled
		cfg.enabledSet = true
	}
	if raw.TurnInterval != nil {
		cfg.TurnInterval = *raw.TurnInterval
		cfg.turnSet = true
	}
	if raw.Compact != nil {
		cfg.Compact = raw.Compact
		cfg.compactSet = true
	}
	if raw.CooldownMs != nil {
		cfg.CooldownMs = *raw.CooldownMs
		cfg.cooldownSet = true
	}
	return nil
}

// MarshalJSON omits the block when it changes nothing (mirrors
// FileConfig.MarshalJSON calling HarnessConfig.Empty).
func (cfg LearningConfig) MarshalJSON() ([]byte, error) {
	if cfg.Empty() {
		return []byte("{}"), nil
	}
	type rawLearning struct {
		Enabled      *bool `json:"enabled,omitempty"`
		TurnInterval int   `json:"turnInterval,omitempty"`
		Compact      *bool `json:"compact,omitempty"`
		CooldownMs   int64 `json:"cooldownMs,omitempty"`
	}
	raw := rawLearning{TurnInterval: cfg.TurnInterval, CooldownMs: cfg.CooldownMs}
	if cfg.enabledSet {
		raw.Enabled = cfg.Enabled
	}
	if cfg.compactSet {
		raw.Compact = cfg.Compact
	}
	return json.Marshal(raw)
}

func mergeLearningConfig(dst *LearningConfig, src LearningConfig) {
	if src.Enabled != nil {
		enabled := *src.Enabled
		dst.Enabled = &enabled
		dst.enabledSet = true
	}
	if src.TurnInterval > 0 {
		dst.TurnInterval = src.TurnInterval
		dst.turnSet = true
	}
	if src.Compact != nil {
		compact := *src.Compact
		dst.Compact = &compact
		dst.compactSet = true
	}
	if src.CooldownMs > 0 {
		dst.CooldownMs = src.CooldownMs
		dst.cooldownSet = true
	}
}

func validateLearningConfig(cfg LearningConfig) []Issue {
	var issues []Issue
	if cfg.TurnInterval < 0 {
		issues = append(issues, Issue{FieldPath: "learning.turnInterval", Message: "turnInterval must be >= 0"})
	}
	if cfg.CooldownMs < 0 {
		issues = append(issues, Issue{FieldPath: "learning.cooldownMs", Message: "cooldownMs must be >= 0"})
	}
	return issues
}
