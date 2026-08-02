package config

import (
	"errors"
	"fmt"
	"strings"
)

func SetHarnessPromptAddendum(path string, addendum HarnessPromptAddendum) (FileConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return FileConfig{}, fmt.Errorf("config path is required")
	}
	if strings.TrimSpace(addendum.ID) == "" {
		return FileConfig{}, fmt.Errorf("prompt addendum id is required")
	}
	if addendum.IsEnabled() && strings.TrimSpace(addendum.Text) == "" {
		return FileConfig{}, fmt.Errorf("prompt addendum text is required")
	}
	cfg, err := readConfigFileForWrite(path)
	if err != nil {
		return FileConfig{}, err
	}
	mergePromptAddendum(&cfg.Harness, addendum)
	if err := writeConfigFile(path, cfg); err != nil {
		return FileConfig{}, err
	}
	return cfg, nil
}

func RemoveHarnessPromptAddendum(path string, id string) (FileConfig, error) {
	path = strings.TrimSpace(path)
	id = strings.TrimSpace(id)
	if path == "" {
		return FileConfig{}, fmt.Errorf("config path is required")
	}
	if id == "" {
		return FileConfig{}, fmt.Errorf("prompt addendum id is required")
	}
	cfg, err := readConfigFileForWrite(path)
	if err != nil {
		return FileConfig{}, err
	}
	next := cfg.Harness.PromptAddenda[:0]
	removed := false
	for _, addendum := range cfg.Harness.PromptAddenda {
		if strings.EqualFold(strings.TrimSpace(addendum.ID), id) {
			removed = true
			continue
		}
		next = append(next, addendum)
	}
	if !removed {
		return FileConfig{}, fmt.Errorf("prompt addendum %q not found", id)
	}
	cfg.Harness.PromptAddenda = next
	if err := writeConfigFile(path, cfg); err != nil {
		return FileConfig{}, err
	}
	return cfg, nil
}

func SetHarnessPermissionRule(path string, rule HarnessPermissionRule) (FileConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return FileConfig{}, fmt.Errorf("config path is required")
	}
	if strings.TrimSpace(rule.ID) == "" {
		return FileConfig{}, fmt.Errorf("permission rule id is required")
	}
	candidate := HarnessConfig{PermissionRules: []HarnessPermissionRule{rule}}
	if issues := validateHarnessConfig(candidate); len(issues) > 0 {
		return FileConfig{}, errors.New(issues[0].Message)
	}
	cfg, err := readConfigFileForWrite(path)
	if err != nil {
		return FileConfig{}, err
	}
	mergePermissionRule(&cfg.Harness, rule)
	if err := writeConfigFile(path, cfg); err != nil {
		return FileConfig{}, err
	}
	return cfg, nil
}

func RemoveHarnessPermissionRule(path string, id string) (FileConfig, error) {
	path = strings.TrimSpace(path)
	id = strings.TrimSpace(id)
	if path == "" {
		return FileConfig{}, fmt.Errorf("config path is required")
	}
	if id == "" {
		return FileConfig{}, fmt.Errorf("permission rule id is required")
	}
	cfg, err := readConfigFileForWrite(path)
	if err != nil {
		return FileConfig{}, err
	}
	next := cfg.Harness.PermissionRules[:0]
	removed := false
	for _, rule := range cfg.Harness.PermissionRules {
		if strings.EqualFold(strings.TrimSpace(rule.ID), id) {
			removed = true
			continue
		}
		next = append(next, rule)
	}
	if !removed {
		return FileConfig{}, fmt.Errorf("permission rule %q not found", id)
	}
	cfg.Harness.PermissionRules = next
	if err := writeConfigFile(path, cfg); err != nil {
		return FileConfig{}, err
	}
	return cfg, nil
}
