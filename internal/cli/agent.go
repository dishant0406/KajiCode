package cli

import (
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"strings"

	"github.com/dishant0406/KajiCode/internal/agents"
)

type agentOptions struct {
	json        bool
	location    agents.Location
	description string
	prompt      string
	extends     string
	model       string
	thinking    string
	tools       []string
	exclude     []string
	force       bool
}

func runAgents(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	command, remaining, options, help, err := parseAgentArgs(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeAgentHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if err := validateAgentCommand(command, remaining); err != nil {
		return writeExecUsageError(stderr, err.Error())
	}

	workspaceRoot, err := resolveWorkspaceRoot("", deps)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	paths, err := agents.DefaultPaths(workspaceRoot)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}

	switch command {
	case "list":
		return runAgentList(paths, options, stdout, stderr)
	case "show":
		return runAgentShow(paths, remaining[0], options, stdout, stderr)
	case "create":
		return runAgentCreate(paths, remaining[0], options, stdout, stderr)
	case "delete", "rm":
		return runAgentDelete(paths, remaining[0], options, stdout, stderr)
	case "edit":
		return runAgentEdit(paths, remaining[0], options, stdout, stderr, deps)
	case "path":
		return runAgentPath(paths, options, stdout)
	default:
		return writeExecUsageError(stderr, fmt.Sprintf("unknown agent command %q", command))
	}
}

func parseAgentArgs(args []string) (string, []string, agentOptions, bool, error) {
	command := "list"
	commandExplicit := false
	remaining := []string{}
	options := agentOptions{}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "-h" || arg == "--help" || arg == "help":
			return command, remaining, options, true, nil
		case arg == "--json":
			options.json = true
		case arg == "--user":
			options.location = agents.LocationUser
		case arg == "--project":
			options.location = agents.LocationProject
		case arg == "--force":
			options.force = true
		case arg == "--location" || arg == "--description" || arg == "--prompt" || arg == "--system-prompt" ||
			arg == "--tools" || arg == "--exclude-tools" || arg == "--extends" || arg == "--model" || arg == "--thinking":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return command, remaining, options, false, err
			}
			index = next
			if err := applyAgentOption(&options, arg, value); err != nil {
				return command, remaining, options, false, err
			}
		case strings.HasPrefix(arg, "--"):
			flag, _, hasValue := strings.Cut(arg, "=")
			if !hasValue {
				return command, remaining, options, false, fmt.Errorf("unknown agent flag %q", arg)
			}
			value, err := requiredInlineFlagValue(arg, flag)
			if err != nil {
				return command, remaining, options, false, err
			}
			if err := applyAgentOption(&options, flag, value); err != nil {
				return command, remaining, options, false, err
			}
		case strings.HasPrefix(arg, "-"):
			return command, remaining, options, false, fmt.Errorf("unknown agent flag %q", arg)
		default:
			if !commandExplicit {
				command = arg
				commandExplicit = true
			} else {
				remaining = append(remaining, arg)
			}
		}
	}
	return command, remaining, options, false, nil
}

func applyAgentOption(options *agentOptions, flag string, value string) error {
	value = strings.TrimSpace(value)
	switch flag {
	case "--location":
		switch strings.ToLower(value) {
		case "user":
			options.location = agents.LocationUser
		case "project":
			options.location = agents.LocationProject
		default:
			return fmt.Errorf("--location must be user or project")
		}
	case "--description":
		options.description = value
	case "--prompt", "--system-prompt":
		options.prompt = value
	case "--tools":
		options.tools = parseToolList(value)
	case "--exclude-tools":
		options.exclude = parseToolList(value)
	case "--extends":
		options.extends = value
	case "--model":
		options.model = value
	case "--thinking":
		options.thinking = value
	default:
		return fmt.Errorf("unknown agent flag %q", flag)
	}
	return nil
}

func validateAgentCommand(command string, remaining []string) error {
	needsName := map[string]bool{"show": true, "create": true, "delete": true, "rm": true, "edit": true}
	switch {
	case command == "list" || command == "path":
		if len(remaining) != 0 {
			return fmt.Errorf("agent %s does not accept positional arguments", command)
		}
	case needsName[command]:
		if len(remaining) != 1 {
			return fmt.Errorf("agent %s requires an agent name", command)
		}
	default:
		return fmt.Errorf("unknown agent command %q", command)
	}
	return nil
}

func runAgentList(paths agents.Paths, options agentOptions, stdout io.Writer, stderr io.Writer) int {
	result, err := agents.Load(paths)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	if options.json {
		if err := writePrettyJSON(stdout, struct {
			Paths    agents.Paths   `json:"paths"`
			Agents   []agents.Agent `json:"agents"`
			Warnings []string       `json:"warnings,omitempty"`
		}{
			Paths:    result.Paths,
			Agents:   result.Agents,
			Warnings: result.Warnings,
		}); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintln(stdout, formatAgentList(result)); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runAgentShow(paths agents.Paths, name string, options agentOptions, stdout io.Writer, stderr io.Writer) int {
	result, err := agents.Load(paths)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	agent, ok := agents.Find(result, name)
	if !ok {
		return writeExecUsageError(stderr, "KajiCode agent not found: "+name)
	}
	if options.json {
		if err := writePrettyJSON(stdout, agent); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintln(stdout, formatAgentShow(agent)); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runAgentCreate(paths agents.Paths, name string, options agentOptions, stdout io.Writer, stderr io.Writer) int {
	location := options.location
	if location == "" {
		location = agents.LocationUser
	}
	dir, err := agentDirFor(paths, location)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	agent := agents.Agent{
		Name:         name,
		Description:  options.description,
		Extends:      options.extends,
		Model:        options.model,
		Thinking:     options.thinking,
		Tools:        options.tools,
		ExcludeTools: options.exclude,
		SystemPrompt: options.prompt,
		Mode:         agents.ModeSubagent,
	}
	if err := agents.Validate(&agent); err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	path := dir + string(os.PathSeparator) + name + ".md"
	if _, statErr := os.Lstat(path); statErr == nil {
		if !options.force {
			return writeExecUsageError(stderr, fmt.Sprintf("agent %q already exists at %s (pass --force to replace it)", name, path))
		}
		if info, infoErr := os.Lstat(path); infoErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return writeExecUsageError(stderr, "refusing to overwrite symlink agent file: "+path)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	if err := os.WriteFile(path, []byte(agents.RenderMarkdown(agent)), 0o644); err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	if options.json {
		if err := writePrettyJSON(stdout, map[string]any{"name": name, "path": path, "created": true}); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintf(stdout, "Created agent %s at %s\n", name, path); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runAgentDelete(paths agents.Paths, name string, options agentOptions, stdout io.Writer, stderr io.Writer) int {
	location := options.location
	if location == "" {
		location = agents.LocationUser
	}
	dir, err := agentDirFor(paths, location)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	path := dir + string(os.PathSeparator) + name + ".md"
	if info, statErr := os.Lstat(path); statErr != nil || info.IsDir() {
		return writeExecUsageError(stderr, "agent file not found: "+path)
	} else if info.Mode()&os.ModeSymlink != 0 {
		return writeExecUsageError(stderr, "refusing to delete symlink agent file: "+path)
	}
	if err := os.Remove(path); err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	if options.json {
		if err := writePrettyJSON(stdout, map[string]any{"name": name, "path": path, "deleted": true}); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintf(stdout, "Deleted agent %s at %s\n", name, path); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runAgentEdit(paths agents.Paths, name string, options agentOptions, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	location := options.location
	if location == "" {
		location = agents.LocationUser
	}
	dir, err := agentDirFor(paths, location)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	path := dir + string(os.PathSeparator) + name + ".md"
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			if location == agents.LocationUser && agentIsBuiltin(paths, name) {
				return writeExecUsageError(stderr, fmt.Sprintf("cannot edit builtin agent %q: builtins are read-only. Create a user or project agent of the same name to override it.", name))
			}
			return writeExecUsageError(stderr, "agent not found: "+name)
		}
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return writeExecUsageError(stderr, "refusing to edit symlink agent file: "+path)
	}
	runEditor := deps.runEditor
	if runEditor == nil {
		runEditor = openEditor
	}
	if err := runEditor(path); err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if _, err := fmt.Fprintf(stdout, "Edited agent %s at %s\n", name, path); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runAgentPath(paths agents.Paths, options agentOptions, stdout io.Writer) int {
	if options.json {
		if err := writePrettyJSON(stdout, paths); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintln(stdout, formatAgentPaths(paths)); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func agentDirFor(paths agents.Paths, location agents.Location) (string, error) {
	switch location {
	case agents.LocationProject:
		if strings.TrimSpace(paths.ProjectDir) == "" {
			return "", fmt.Errorf("no project directory available (run inside a workspace)")
		}
		return paths.ProjectDir, nil
	case agents.LocationUser, "":
		return paths.UserDir, nil
	default:
		return "", fmt.Errorf("--location must be user or project")
	}
}

func agentIsBuiltin(paths agents.Paths, name string) bool {
	result, err := agents.Load(agents.Paths{UserDir: paths.UserDir})
	if err != nil {
		return false
	}
	agent, ok := agents.Find(result, name)
	return ok && agent.Location == agents.LocationBuiltin
}

func openEditor(path string) error {
	editor := strings.TrimSpace(os.Getenv("VISUAL"))
	if editor == "" {
		editor = strings.TrimSpace(os.Getenv("EDITOR"))
	}
	if editor == "" {
		return fmt.Errorf("VISUAL or EDITOR must be set to edit agents")
	}
	parts := strings.Fields(editor)
	if len(parts) == 0 {
		return fmt.Errorf("VISUAL or EDITOR must be set to edit agents")
	}
	command := osexec.Command(parts[0], append(parts[1:], path)...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("editor failed: %w", err)
	}
	return nil
}

func writeAgentHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  kajicode agent [command] [flags]

Commands:
  list       List built-in, user, and project agents
  show NAME  Show one agent definition
  create NAME
             Create a user agent definition
  delete NAME
             Delete a user agent definition
  edit NAME  Open a user agent definition in $VISUAL or $EDITOR
  path       Print agent directories
  help       Show this help

Flags:
  --json                      Print JSON output
  --user                      Use the user agent directory (default)
  --project                   Use the project .kajicode/agents directory
  --description <text>        Description for create
  --prompt <text>             System prompt for create
  --tools <list>              Tool ids/categories for create
  --exclude-tools <list>      Tools to deny for create
  --extends <name>            Base agent for create
  --model <model>             Model override for create
  --thinking <value>          Reasoning effort override for create
  --force                     Replace an existing agent during create
`)
	return err
}
