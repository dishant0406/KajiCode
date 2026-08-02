package agent

import (
	"strings"

	"github.com/dishant0406/KajiCode/internal/config"
)

func harnessConfigContext(harness config.HarnessConfig) string {
	var b strings.Builder
	if addenda := enabledPromptAddenda(harness.PromptAddenda); len(addenda) > 0 {
		b.WriteString("<harness_prompt_addenda>\n")
		b.WriteString("Additional operator-configured instructions for this run:\n")
		for _, addendum := range addenda {
			id := strings.TrimSpace(addendum.ID)
			if id != "" {
				b.WriteString("\n[" + id + "]\n")
			}
			b.WriteString(strings.TrimSpace(addendum.Text))
			b.WriteString("\n")
		}
		b.WriteString("</harness_prompt_addenda>\n")
	}
	if rules := enabledPermissionRules(harness.PermissionRules); len(rules) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("<harness_permission_policy>\n")
		b.WriteString("Runtime permission rules are enforced by KajiCode. Do not try to bypass them; choose safer tools or ask the user when blocked.\n")
		for _, rule := range rules {
			b.WriteString("- ")
			if id := strings.TrimSpace(rule.ID); id != "" {
				b.WriteString(id + ": ")
			}
			b.WriteString(strings.ToLower(strings.TrimSpace(rule.Action)))
			b.WriteString(" " + strings.TrimSpace(rule.Match))
			if side := strings.TrimSpace(rule.SideEffect); side != "" {
				b.WriteString(" side_effect=" + side)
			}
			if risk := strings.TrimSpace(rule.MinRisk); risk != "" {
				b.WriteString(" min_risk=" + risk)
			}
			if reason := strings.TrimSpace(rule.Reason); reason != "" {
				b.WriteString(" reason=" + reason)
			}
			b.WriteString("\n")
		}
		b.WriteString("</harness_permission_policy>")
	}
	return strings.TrimSpace(b.String())
}

func enabledPromptAddenda(addenda []config.HarnessPromptAddendum) []config.HarnessPromptAddendum {
	out := make([]config.HarnessPromptAddendum, 0, len(addenda))
	for _, addendum := range addenda {
		if addendum.IsEnabled() && strings.TrimSpace(addendum.Text) != "" {
			out = append(out, addendum)
		}
	}
	return out
}

func enabledPermissionRules(rules []config.HarnessPermissionRule) []config.HarnessPermissionRule {
	out := make([]config.HarnessPermissionRule, 0, len(rules))
	for _, rule := range rules {
		if rule.IsEnabled() && strings.TrimSpace(rule.Match) != "" && strings.TrimSpace(rule.Action) != "" {
			out = append(out, rule)
		}
	}
	return out
}
