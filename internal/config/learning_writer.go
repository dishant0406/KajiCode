package config

import (
	"errors"
	"fmt"
	"strings"
)

// SetLearningConfig writes a single learning-key value into the user config
// file at path, merging with any existing config. Supported keys:
//
//	enabled         [on|off]
//	debounceMs      <int>    (>= 0; 0 resets to the default 30 seconds)
//	compact         [on|off]
//	pruneAfterDays  <int>    (>= 0; 0 resets to the default 90 days)
//	maxEntries      <int>    (>= 0; 0 resets to the default 200)
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
		b, err := parseOnOff(value)
		if err != nil {
			return FileConfig{}, fmt.Errorf("learning %s", err)
		}
		candidate.Enabled = &b
	case "compact":
		b, err := parseOnOff(value)
		if err != nil {
			return FileConfig{}, fmt.Errorf("learning %s", err)
		}
		candidate.Compact = &b
	case "debouncems":
		n, err := parseNonNegativeInt(key, value)
		if err != nil {
			return FileConfig{}, err
		}
		candidate.DebounceMs = int64(n)
	case "pruneafterdays":
		n, err := parseNonNegativeInt(key, value)
		if err != nil {
			return FileConfig{}, err
		}
		candidate.PruneAfterDays = n
	case "maxentries":
		n, err := parseNonNegativeInt(key, value)
		if err != nil {
			return FileConfig{}, err
		}
		candidate.MaxEntries = n
	default:
		return FileConfig{}, fmt.Errorf("unknown learning key %q (supported: enabled, debounceMs, compact, pruneAfterDays, maxEntries)", key)
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

// parseOnOff parses the shared on/off shorthand used by the boolean learning
// keys.
func parseOnOff(value string) (bool, error) {
	switch strings.ToLower(value) {
	case "on", "true", "1", "yes":
		return true, nil
	case "off", "false", "0", "no":
		return false, nil
	default:
		return false, fmt.Errorf("value must be on or off, got %q", value)
	}
}

func parseNonNegativeInt(key, value string) (int, error) {
	var n int
	if _, err := fmt.Sscanf(value, "%d", &n); err != nil || n < 0 {
		return 0, fmt.Errorf("learning %s must be a non-negative integer, got %q", key, value)
	}
	return n, nil
}
