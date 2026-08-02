package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const (
	HarnessRuleAllow = "allow"
	HarnessRuleAsk   = "ask"
	HarnessRuleDeny  = "deny"
)

func (cfg HarnessPromptAddendum) IsEnabled() bool {
	return cfg.Enabled == nil || *cfg.Enabled
}

func (cfg HarnessPermissionRule) IsEnabled() bool {
	return cfg.Enabled == nil || *cfg.Enabled
}

func (cfg HarnessConfig) Empty() bool {
	return len(cfg.PromptAddenda) == 0 && len(cfg.PermissionRules) == 0
}

func mergeHarnessConfig(dst *HarnessConfig, src HarnessConfig) {
	for _, addendum := range src.PromptAddenda {
		mergePromptAddendum(dst, addendum)
	}
	for _, rule := range src.PermissionRules {
		mergePermissionRule(dst, rule)
	}
}

func mergeProjectHarnessConfig(dst *HarnessConfig, src HarnessConfig) error {
	for _, rule := range src.PermissionRules {
		if normalizeHarnessRuleAction(rule.Action) == HarnessRuleAllow {
			return fmt.Errorf("project harness permission rule %q cannot use action %q; project config may only ask or deny", strings.TrimSpace(rule.ID), HarnessRuleAllow)
		}
	}
	mergeHarnessConfig(dst, src)
	return nil
}

func mergePromptAddendum(dst *HarnessConfig, addendum HarnessPromptAddendum) {
	addendum.ID = strings.TrimSpace(addendum.ID)
	addendum.Text = strings.TrimSpace(addendum.Text)
	if addendum.ID == "" {
		dst.PromptAddenda = append(dst.PromptAddenda, addendum)
		return
	}
	for index := range dst.PromptAddenda {
		if strings.EqualFold(strings.TrimSpace(dst.PromptAddenda[index].ID), addendum.ID) {
			dst.PromptAddenda[index] = addendum
			return
		}
	}
	dst.PromptAddenda = append(dst.PromptAddenda, addendum)
}

func mergePermissionRule(dst *HarnessConfig, rule HarnessPermissionRule) {
	rule.ID = strings.TrimSpace(rule.ID)
	rule.Match = strings.TrimSpace(rule.Match)
	rule.Action = normalizeHarnessRuleAction(rule.Action)
	rule.SideEffect = strings.TrimSpace(rule.SideEffect)
	rule.MinRisk = strings.TrimSpace(rule.MinRisk)
	rule.Reason = strings.TrimSpace(rule.Reason)
	rule.CommandContains = normalizeStringList(rule.CommandContains)
	if rule.ID == "" {
		dst.PermissionRules = append(dst.PermissionRules, rule)
		return
	}
	for index := range dst.PermissionRules {
		if strings.EqualFold(strings.TrimSpace(dst.PermissionRules[index].ID), rule.ID) {
			dst.PermissionRules[index] = rule
			return
		}
	}
	dst.PermissionRules = append(dst.PermissionRules, rule)
}

func validateHarnessConfig(cfg HarnessConfig) []Issue {
	var issues []Issue
	for index, addendum := range cfg.PromptAddenda {
		if strings.TrimSpace(addendum.Text) == "" && addendum.IsEnabled() {
			issues = append(issues, Issue{FieldPath: fmt.Sprintf("harness.promptAddenda[%d].text", index), Message: "enabled prompt addendum text is required"})
		}
	}
	for index, rule := range cfg.PermissionRules {
		path := fmt.Sprintf("harness.permissionRules[%d]", index)
		if strings.TrimSpace(rule.Match) == "" {
			issues = append(issues, Issue{FieldPath: path + ".match", Message: "permission rule match is required"})
		}
		switch normalizeHarnessRuleAction(rule.Action) {
		case HarnessRuleAllow, HarnessRuleAsk, HarnessRuleDeny:
		default:
			issues = append(issues, Issue{FieldPath: path + ".action", Message: "permission rule action must be allow, ask, prompt, or deny"})
		}
		if side := strings.TrimSpace(rule.SideEffect); side != "" && !validHarnessSideEffect(side) {
			issues = append(issues, Issue{FieldPath: path + ".sideEffect", Message: "permission rule sideEffect is not recognized"})
		}
		if risk := strings.TrimSpace(rule.MinRisk); risk != "" && !validHarnessRisk(risk) {
			issues = append(issues, Issue{FieldPath: path + ".minRisk", Message: "permission rule minRisk must be low, medium, high, or critical"})
		}
	}
	return issues
}

func normalizeHarnessRuleAction(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "prompt":
		return HarnessRuleAsk
	default:
		return strings.ToLower(strings.TrimSpace(action))
	}
}

func validHarnessSideEffect(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "none", "read", "write", "shell", "network", "local_control", "local_browser", "local_desktop", "local_terminal", "out_of_workspace":
		return true
	default:
		return false
	}
}

func validHarnessRisk(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "low", "medium", "high", "critical":
		return true
	default:
		return false
	}
}

func readConfigFileForWrite(path string) (FileConfig, error) {
	cfg := FileConfig{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return FileConfig{}, fmt.Errorf("invalid config JSON %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return FileConfig{}, fmt.Errorf("read config %s: %w", path, err)
	}
	return cfg, nil
}

func normalizeStringList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
