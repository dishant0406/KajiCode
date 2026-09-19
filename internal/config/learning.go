package config

import (
	"encoding/json"
	"time"
)

// LearningConfig tunes KajiCode's self-learning (perpetual memory) loop.
//
// Learning is event-driven, not timer-driven: a refinement pass runs when the
// agent does something worth learning from — a tool call that failed and was
// then fixed, a user correction, a test that failed then passed, a repeat of a
// workflow already recorded — or when a run finishes. There is no "every N
// turns" schedule, because most sessions are shorter than any such interval and
// the lessons that matter are produced by failure and correction, not by the
// passage of turns.
//
// Enabled turns on automatic learning (on by default). DebounceMs is the minimum
// gap between two automatic passes so a burst of signals cannot burn provider
// budget; a manual request bypasses it. Compact also triggers a pass after a
// context compaction. PruneAfterDays drops entries that have not been reinforced
// within that window (0 disables pruning). MaxEntries caps the durable store per
// scope so memory cannot grow without bound.
type LearningConfig struct {
	Enabled        *bool `json:"enabled,omitempty"`
	DebounceMs     int64 `json:"debounceMs,omitempty"`
	Compact        *bool `json:"compact,omitempty"`
	PruneAfterDays int   `json:"pruneAfterDays,omitempty"`
	MaxEntries     int   `json:"maxEntries,omitempty"`

	// enabledSet distinguishes an explicit false from an unset field so user
	// config merge can override a default-on value. Not persisted.
	enabledSet  bool
	compactSet  bool
	debounceSet bool
	pruneSet    bool
	maxSet      bool
}

const (
	// AutoLearnDebounceDefaultMs is the minimum gap between automatic passes.
	// Long enough to coalesce a burst of failure→fix signals, short enough that
	// a lesson lands while it is still relevant.
	AutoLearnDebounceDefaultMs = int64(30 * 1000) // 30 seconds
	// AutoLearnPruneAfterDaysDefault is the age at which an unreinforced entry is
	// dropped, so stale lessons age out instead of accumulating forever.
	AutoLearnPruneAfterDaysDefault = 90
	// AutoLearnMaxEntriesDefault caps stored entries per scope.
	AutoLearnMaxEntriesDefault = 200
)

// DefaultLearningConfig returns the production defaults: auto-learning on, a
// 30-second debounce, post-compaction learning on, prune unreinforced entries
// after 90 days, and cap each scope at 200 entries.
func DefaultLearningConfig() LearningConfig {
	enabled := true
	compact := true
	return LearningConfig{
		Enabled:        &enabled,
		DebounceMs:     AutoLearnDebounceDefaultMs,
		Compact:        &compact,
		PruneAfterDays: AutoLearnPruneAfterDaysDefault,
		MaxEntries:     AutoLearnMaxEntriesDefault,
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

// Debounce returns the minimum gap between automatic passes as a duration.
func (cfg LearningConfig) Debounce() time.Duration {
	if cfg.DebounceMs <= 0 {
		return time.Duration(AutoLearnDebounceDefaultMs) * time.Millisecond
	}
	return time.Duration(cfg.DebounceMs) * time.Millisecond
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
	if cfg.DebounceMs <= 0 {
		cfg.DebounceMs = def.DebounceMs
	}
	if cfg.PruneAfterDays <= 0 {
		cfg.PruneAfterDays = def.PruneAfterDays
	}
	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = def.MaxEntries
	}
	return cfg
}

// Empty reports whether the config changes no default behavior. Used by
// MarshalJSON to omit an absent block exactly like HarnessConfig.Empty.
func (cfg LearningConfig) Empty() bool {
	return !cfg.enabledSet && !cfg.compactSet && !cfg.debounceSet && !cfg.pruneSet && !cfg.maxSet
}

// UnmarshalJSON distinguishes an explicitly-declared field from an absent one
// so merges can correctly override the default-on behavior. Mirrors the
// LocalControl/STT enable-gate pattern already in this package.
func (cfg *LearningConfig) UnmarshalJSON(data []byte) error {
	type rawLearning struct {
		Enabled        *bool  `json:"enabled"`
		DebounceMs     *int64 `json:"debounceMs"`
		Compact        *bool  `json:"compact"`
		PruneAfterDays *int   `json:"pruneAfterDays"`
		MaxEntries     *int   `json:"maxEntries"`
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
	if raw.DebounceMs != nil {
		cfg.DebounceMs = *raw.DebounceMs
		cfg.debounceSet = true
	}
	if raw.Compact != nil {
		cfg.Compact = raw.Compact
		cfg.compactSet = true
	}
	if raw.PruneAfterDays != nil {
		cfg.PruneAfterDays = *raw.PruneAfterDays
		cfg.pruneSet = true
	}
	if raw.MaxEntries != nil {
		cfg.MaxEntries = *raw.MaxEntries
		cfg.maxSet = true
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
		Enabled        *bool `json:"enabled,omitempty"`
		DebounceMs     int64 `json:"debounceMs,omitempty"`
		Compact        *bool `json:"compact,omitempty"`
		PruneAfterDays int   `json:"pruneAfterDays,omitempty"`
		MaxEntries     int   `json:"maxEntries,omitempty"`
	}
	raw := rawLearning{
		DebounceMs:     cfg.DebounceMs,
		PruneAfterDays: cfg.PruneAfterDays,
		MaxEntries:     cfg.MaxEntries,
	}
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
	if src.DebounceMs > 0 {
		dst.DebounceMs = src.DebounceMs
		dst.debounceSet = true
	}
	if src.Compact != nil {
		compact := *src.Compact
		dst.Compact = &compact
		dst.compactSet = true
	}
	if src.PruneAfterDays > 0 {
		dst.PruneAfterDays = src.PruneAfterDays
		dst.pruneSet = true
	}
	if src.MaxEntries > 0 {
		dst.MaxEntries = src.MaxEntries
		dst.maxSet = true
	}
}

func validateLearningConfig(cfg LearningConfig) []Issue {
	var issues []Issue
	if cfg.DebounceMs < 0 {
		issues = append(issues, Issue{FieldPath: "learning.debounceMs", Message: "debounceMs must be >= 0"})
	}
	if cfg.PruneAfterDays < 0 {
		issues = append(issues, Issue{FieldPath: "learning.pruneAfterDays", Message: "pruneAfterDays must be >= 0"})
	}
	if cfg.MaxEntries < 0 {
		issues = append(issues, Issue{FieldPath: "learning.maxEntries", Message: "maxEntries must be >= 0"})
	}
	return issues
}
