package cli

import (
	"fmt"
	"strings"

	"github.com/dishant0406/KajiCode/internal/config"
)

func parseHarnessPromptAdd(args []string) (string, config.HarnessPromptAddendum, bool, error) {
	if len(args) == 0 {
		return "", config.HarnessPromptAddendum{}, false, execUsageError{"prompt add requires an id"}
	}
	id := strings.TrimSpace(args[0])
	addendum := config.HarnessPromptAddendum{}
	project := false
	for index := 1; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--project":
			project = true
		case arg == "--disabled":
			v := false
			addendum.Enabled = &v
		case arg == "--text":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return "", addendum, false, err
			}
			addendum.Text = value
			index = next
		case strings.HasPrefix(arg, "--text="):
			value, err := requiredInlineFlagValue(arg, "--text")
			if err != nil {
				return "", addendum, false, err
			}
			addendum.Text = value
		default:
			return "", addendum, false, execUsageError{fmt.Sprintf("unknown harness prompt flag %q", arg)}
		}
	}
	return id, addendum, project, nil
}

func parseHarnessRuleAdd(args []string) (config.HarnessPermissionRule, bool, error) {
	if len(args) == 0 {
		return config.HarnessPermissionRule{}, false, execUsageError{"rule add requires an id"}
	}
	rule := config.HarnessPermissionRule{ID: strings.TrimSpace(args[0])}
	project := false
	for index := 1; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--project":
			project = true
		case arg == "--disabled":
			v := false
			rule.Enabled = &v
		case arg == "--match", arg == "--action", arg == "--side-effect", arg == "--min-risk", arg == "--command-contains", arg == "--reason":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return rule, false, err
			}
			assignHarnessRuleFlag(&rule, arg, value)
			index = next
		case strings.HasPrefix(arg, "--match="):
			value, err := requiredInlineFlagValue(arg, "--match")
			if err != nil {
				return rule, false, err
			}
			rule.Match = value
		case strings.HasPrefix(arg, "--action="):
			value, err := requiredInlineFlagValue(arg, "--action")
			if err != nil {
				return rule, false, err
			}
			rule.Action = value
		case strings.HasPrefix(arg, "--side-effect="):
			value, err := requiredInlineFlagValue(arg, "--side-effect")
			if err != nil {
				return rule, false, err
			}
			rule.SideEffect = value
		case strings.HasPrefix(arg, "--min-risk="):
			value, err := requiredInlineFlagValue(arg, "--min-risk")
			if err != nil {
				return rule, false, err
			}
			rule.MinRisk = value
		case strings.HasPrefix(arg, "--command-contains="):
			value, err := requiredInlineFlagValue(arg, "--command-contains")
			if err != nil {
				return rule, false, err
			}
			rule.CommandContains = append(rule.CommandContains, value)
		case strings.HasPrefix(arg, "--reason="):
			value, err := requiredInlineFlagValue(arg, "--reason")
			if err != nil {
				return rule, false, err
			}
			rule.Reason = value
		default:
			return rule, false, execUsageError{fmt.Sprintf("unknown harness rule flag %q", arg)}
		}
	}
	return rule, project, nil
}

func assignHarnessRuleFlag(rule *config.HarnessPermissionRule, flag string, value string) {
	switch flag {
	case "--match":
		rule.Match = value
	case "--action":
		rule.Action = value
	case "--side-effect":
		rule.SideEffect = value
	case "--min-risk":
		rule.MinRisk = value
	case "--command-contains":
		rule.CommandContains = append(rule.CommandContains, value)
	case "--reason":
		rule.Reason = value
	}
}

func parseHarnessRemove(args []string) (string, bool, error) {
	project := false
	id := ""
	for _, arg := range args {
		switch arg {
		case "--project":
			project = true
		default:
			if strings.HasPrefix(arg, "-") {
				return "", false, execUsageError{fmt.Sprintf("unknown harness remove flag %q", arg)}
			}
			if id != "" {
				return "", false, execUsageError{"remove accepts exactly one id"}
			}
			id = strings.TrimSpace(arg)
		}
	}
	if id == "" {
		return "", false, execUsageError{"remove requires an id"}
	}
	return id, project, nil
}

func harnessDisplay(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
