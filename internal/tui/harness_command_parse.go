package tui

import (
	"fmt"
	"strings"

	"github.com/dishant0406/KajiCode/internal/config"
)

func parseTUIHarnessPromptAdd(args []string) (string, config.HarnessPromptAddendum, bool, error) {
	if len(args) == 0 {
		return "", config.HarnessPromptAddendum{}, false, fmt.Errorf("prompt add requires an id")
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
			value, next, err := tuiFlagValue(args, index, arg)
			if err != nil {
				return "", addendum, false, err
			}
			addendum.Text = value
			index = next
		case strings.HasPrefix(arg, "--text="):
			addendum.Text = strings.TrimPrefix(arg, "--text=")
		default:
			return "", addendum, false, fmt.Errorf("unknown harness prompt flag %q", arg)
		}
	}
	return id, addendum, project, nil
}

func parseTUIHarnessRuleAdd(args []string) (config.HarnessPermissionRule, bool, error) {
	if len(args) == 0 {
		return config.HarnessPermissionRule{}, false, fmt.Errorf("rule add requires an id")
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
		case harnessRuleValueFlag(arg):
			value, next, err := tuiFlagValue(args, index, arg)
			if err != nil {
				return rule, false, err
			}
			assignTUIHarnessRuleFlag(&rule, arg, value)
			index = next
		case strings.HasPrefix(arg, "--match="):
			rule.Match = strings.TrimPrefix(arg, "--match=")
		case strings.HasPrefix(arg, "--action="):
			rule.Action = strings.TrimPrefix(arg, "--action=")
		case strings.HasPrefix(arg, "--side-effect="):
			rule.SideEffect = strings.TrimPrefix(arg, "--side-effect=")
		case strings.HasPrefix(arg, "--min-risk="):
			rule.MinRisk = strings.TrimPrefix(arg, "--min-risk=")
		case strings.HasPrefix(arg, "--command-contains="):
			rule.CommandContains = append(rule.CommandContains, strings.TrimPrefix(arg, "--command-contains="))
		case strings.HasPrefix(arg, "--reason="):
			rule.Reason = strings.TrimPrefix(arg, "--reason=")
		default:
			return rule, false, fmt.Errorf("unknown harness rule flag %q", arg)
		}
	}
	return rule, project, nil
}

func parseTUIHarnessRemove(args []string) (string, bool, error) {
	id := ""
	project := false
	for _, arg := range args {
		if arg == "--project" {
			project = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			return "", false, fmt.Errorf("unknown harness remove flag %q", arg)
		}
		if id != "" {
			return "", false, fmt.Errorf("remove accepts exactly one id")
		}
		id = strings.TrimSpace(arg)
	}
	if id == "" {
		return "", false, fmt.Errorf("remove requires an id")
	}
	return id, project, nil
}

func harnessRuleValueFlag(arg string) bool {
	switch arg {
	case "--match", "--action", "--side-effect", "--min-risk", "--command-contains", "--reason":
		return true
	default:
		return false
	}
}

func assignTUIHarnessRuleFlag(rule *config.HarnessPermissionRule, flag string, value string) {
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

func tuiFlagValue(args []string, index int, flag string) (string, int, error) {
	if index+1 >= len(args) {
		return "", index, fmt.Errorf("%s requires a value", flag)
	}
	value := strings.TrimSpace(args[index+1])
	if value == "" {
		return "", index, fmt.Errorf("%s requires a non-empty value", flag)
	}
	return value, index + 1, nil
}
