package config

import (
	"errors"
	"fmt"
	"strings"
)

// SetLearningConfig writes a single learning-key value into the user config
// file at path, merging with any existing config. Supported keys:
//
//	enabled        [on|off]
//	turnInterval   <int>    (>= 0; 0 resets to the default 10)
//	compact        [on|off]
//	cooldownMs     <int>    (>= 0; 0 resets to the default 20 minutes)
//
// It returns the resulting FileConfig.
func SetLearningConfig(path, key, value string) (FileConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return FileConfig{}, fmt.Errorf("config path is required")
	}
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return FileConfig{}, fmt.Errorf("learning key and value are required")
	}

	candidate := LearningConfig{}
	normKey := strings.ToLower(key)
	switch normKey {
	case "enabled":
		switch strings.ToLower(value) {
		case "on", "true", "1", "yes":
			b := true
			candidate.Enabled = &b
		case "off", "false", "0", "no":
			b := false
			candidate.Enabled = &b
		default:
			return FileConfig{}, fmt.Errorf("learning enabled must be on or off, got %q", value)
		}
	case "turninterval":
		n, err := parseNonNegativeInt(key, value)
		if err != nil {
			return FileConfig{}, err
		}
		candidate.TurnInterval = n
	case "compact":
		switch strings.ToLower(value) {
		case "on", "true", "1", "yes":
			b := true
			candidate.Compact = &b
		case "off", "false", "0", "no":
			b := false
			candidate.Compact = &b
		default:
			return FileConfig{}, fmt.Errorf("learning compact must be on or off, got %q", value)
		}
	case "cooldownms":
		var milliseconds int64
		if _, err := fmt.Sscanf(value, "%d", &milliseconds); err != nil || milliseconds < 0 {
			return FileConfig{}, fmt.Errorf("learning cooldownMs must be a non-negative integer, got %q", value)
		}
		candidate.CooldownMs = milliseconds
	default:
		return FileConfig{}, fmt.Errorf("unknown learning key %q (supported: enabled, turnInterval, compact, cooldownMs)", key)
	}
	if issues := validateLearningConfig(candidate); len(issues) > 0 {
		return FileConfig{}, errors.New(issues[0].Message)
	}

	cfg, err := readConfigFileForWrite(path)
	if err != nil {
		return FileConfig{}, err
	}
	mergeLearningConfig(&cfg.Learning, candidate)
	if err := writeConfigFile(path, cfg); err != nil {
		return FileConfig{}, err
	}
	return cfg, nil
}

func parseNonNegativeInt(key, value string) (int, error) {
	var n int
	if _, err := fmt.Sscanf(value, "%d", &n); err != nil || n < 0 {
		return 0, fmt.Errorf("learning %s must be a non-negative integer, got %q", key, value)
	}
	return n, nil
}
