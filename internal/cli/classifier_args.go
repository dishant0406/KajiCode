package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/dishant0406/KajiCode/internal/classifier"
)

type classifierAddOptions struct {
	json        bool
	setActive   bool
	apiKeyStdin bool
	profile     classifier.Profile
}

func parseClassifierListArgs(args []string) (jsonOut bool, help bool, err error) {
	for _, arg := range args {
		switch arg {
		case "-h", "--help", "help":
			return false, true, nil
		case "--json":
			jsonOut = true
		default:
			return false, false, execUsageError{fmt.Sprintf("unknown classifier list flag %q", arg)}
		}
	}
	return jsonOut, false, nil
}

func parseClassifierPositionalCommand(args []string, command string) (jsonOut bool, name string, help bool, err error) {
	for _, arg := range args {
		switch arg {
		case "-h", "--help", "help":
			return false, "", true, nil
		case "--json":
			jsonOut = true
		default:
			if strings.HasPrefix(arg, "-") {
				return false, "", false, execUsageError{fmt.Sprintf("unknown classifier %s flag %q", command, arg)}
			}
			if name != "" {
				return false, "", false, execUsageError{fmt.Sprintf("classifier %s takes a single classifier name", command)}
			}
			name = arg
		}
	}
	if name == "" {
		return false, "", false, execUsageError{fmt.Sprintf("classifier %s requires a classifier name", command)}
	}
	return jsonOut, name, false, nil
}

// parseClassifierCheckArgs is like parseClassifierPositionalCommand but the name
// is optional: `classifier check` probes the active profile.
func parseClassifierCheckArgs(args []string) (jsonOut bool, name string, help bool, err error) {
	for _, arg := range args {
		switch arg {
		case "-h", "--help", "help":
			return false, "", true, nil
		case "--json":
			jsonOut = true
		default:
			if strings.HasPrefix(arg, "-") {
				return false, "", false, execUsageError{fmt.Sprintf("unknown classifier check flag %q", arg)}
			}
			if name != "" {
				return false, "", false, execUsageError{"classifier check takes a single classifier name"}
			}
			name = arg
		}
	}
	return jsonOut, name, false, nil
}

func parseClassifierAddArgs(args []string) (classifierAddOptions, bool, error) {
	options := classifierAddOptions{}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "-h" || arg == "--help" || arg == "help":
			return options, true, nil
		case arg == "--json":
			options.json = true
		case arg == "--set-active":
			options.setActive = true
		case arg == "--api-key-stdin":
			options.apiKeyStdin = true
		case arg == "--kind":
			value, err := classifierFlagValue(args, &index, arg)
			if err != nil {
				return options, false, err
			}
			options.profile.Kind = value
		case arg == "--endpoint" || arg == "--url":
			value, err := classifierFlagValue(args, &index, arg)
			if err != nil {
				return options, false, err
			}
			options.profile.Endpoint = value
		case arg == "--model":
			value, err := classifierFlagValue(args, &index, arg)
			if err != nil {
				return options, false, err
			}
			options.profile.Model = value
		case arg == "--auth-header":
			value, err := classifierFlagValue(args, &index, arg)
			if err != nil {
				return options, false, err
			}
			options.profile.AuthHeader = value
		case arg == "--auth-scheme":
			value, err := classifierFlagValue(args, &index, arg)
			if err != nil {
				return options, false, err
			}
			options.profile.AuthScheme = value
		case arg == "--api-key-env":
			value, err := classifierFlagValue(args, &index, arg)
			if err != nil {
				return options, false, err
			}
			options.profile.APIKeyEnv = value
		case arg == "--header":
			value, err := classifierFlagValue(args, &index, arg)
			if err != nil {
				return options, false, err
			}
			if err := addClassifierHeader(&options.profile, value); err != nil {
				return options, false, err
			}
		case arg == "--timeout":
			value, err := classifierFlagValue(args, &index, arg)
			if err != nil {
				return options, false, err
			}
			timeout, convErr := strconv.Atoi(value)
			if convErr != nil || timeout <= 0 {
				return options, false, execUsageError{fmt.Sprintf("invalid --timeout %q: want a positive number of milliseconds", value)}
			}
			options.profile.TimeoutMS = timeout
		case strings.HasPrefix(arg, "-"):
			return options, false, execUsageError{fmt.Sprintf("unknown classifier add flag %q", arg)}
		default:
			if options.profile.Name != "" {
				return options, false, execUsageError{"classifier add takes a single classifier name"}
			}
			options.profile.Name = strings.TrimSpace(arg)
		}
	}
	if options.profile.Name == "" {
		return options, false, execUsageError{"classifier add requires a name"}
	}
	return options, false, nil
}

func classifierFlagValue(args []string, index *int, flag string) (string, error) {
	if *index+1 >= len(args) {
		return "", execUsageError{fmt.Sprintf("%s requires a value", flag)}
	}
	*index++
	return strings.TrimSpace(args[*index]), nil
}

func addClassifierHeader(profile *classifier.Profile, value string) error {
	key, headerValue, ok := strings.Cut(value, "=")
	key = strings.TrimSpace(key)
	if !ok || key == "" {
		return execUsageError{fmt.Sprintf("invalid header %q; want key=value", value)}
	}
	if profile.Headers == nil {
		profile.Headers = map[string]string{}
	}
	profile.Headers[key] = strings.TrimSpace(headerValue)
	return nil
}
