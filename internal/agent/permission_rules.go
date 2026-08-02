package agent

import (
	"fmt"
	"path"
	"strings"

	"github.com/dishant0406/KajiCode/internal/config"
	"github.com/dishant0406/KajiCode/internal/sandbox"
	"github.com/dishant0406/KajiCode/internal/tools"
)

type permissionRuleDecision struct {
	action PermissionAction
	reason string
	rule   config.HarnessPermissionRule
}

func evaluatePermissionRules(call ToolCall, tool tools.Tool, args map[string]any, permissionMode PermissionMode, options Options) (permissionRuleDecision, bool) {
	rules := enabledPermissionRules(options.Harness.PermissionRules)
	if len(rules) == 0 || tool == nil {
		return permissionRuleDecision{}, false
	}
	req := sandboxRequest(call.Name, tool, args, false, permissionMode, options)
	risk := sandbox.Classify(req)
	sideEffect := string(tool.Safety().SideEffect)
	command := permissionRuleCommandText(args)
	for index := len(rules) - 1; index >= 0; index-- {
		rule := rules[index]
		if !permissionRuleMatches(rule, call.Name, sideEffect, risk, command) {
			continue
		}
		action := permissionRuleAction(rule.Action)
		if action == "" {
			continue
		}
		reason := strings.TrimSpace(rule.Reason)
		if reason == "" {
			reason = "harness permission rule matched"
			if id := strings.TrimSpace(rule.ID); id != "" {
				reason += ": " + id
			}
		}
		return permissionRuleDecision{action: action, reason: reason, rule: rule}, true
	}
	return permissionRuleDecision{}, false
}

func permissionRuleMatches(rule config.HarnessPermissionRule, toolName string, sideEffect string, risk sandbox.Risk, command string) bool {
	if !wildcardMatch(strings.TrimSpace(rule.Match), toolName) {
		return false
	}
	if expected := strings.TrimSpace(rule.SideEffect); expected != "" && !strings.EqualFold(expected, sideEffect) {
		return false
	}
	if minRisk := sandbox.RiskLevel(strings.ToLower(strings.TrimSpace(rule.MinRisk))); minRisk != "" && permissionRuleRiskRank(risk.Level) < permissionRuleRiskRank(minRisk) {
		return false
	}
	for _, needle := range rule.CommandContains {
		needle = strings.TrimSpace(needle)
		if needle != "" && !strings.Contains(command, needle) {
			return false
		}
	}
	return true
}

func wildcardMatch(pattern string, value string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	if pattern == value || pattern == "*" {
		return true
	}
	if ok, err := path.Match(pattern, value); err == nil {
		return ok
	}
	return false
}

func permissionRuleAction(action string) PermissionAction {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case config.HarnessRuleAllow:
		return PermissionActionAllow
	case config.HarnessRuleAsk, "prompt":
		return PermissionActionPrompt
	case config.HarnessRuleDeny:
		return PermissionActionDeny
	default:
		return ""
	}
}

func permissionRuleRiskRank(level sandbox.RiskLevel) int {
	switch level {
	case sandbox.RiskCritical:
		return 4
	case sandbox.RiskHigh:
		return 3
	case sandbox.RiskMedium:
		return 2
	case sandbox.RiskLow:
		return 1
	default:
		return 0
	}
}

func permissionRuleCommandText(args map[string]any) string {
	for _, key := range []string{"command", "cmd", "script", "shell"} {
		if value, ok := args[key]; ok {
			return strings.TrimSpace(toString(value))
		}
	}
	return ""
}

func toString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	default:
		return fmt.Sprint(value)
	}
}
