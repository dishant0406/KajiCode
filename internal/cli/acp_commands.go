package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/dishant0406/KajiCode/internal/acp"
	"github.com/dishant0406/KajiCode/internal/agent"
	"github.com/dishant0406/KajiCode/internal/usercommands"
)

// The ACP command catalog exposes KajiCode's inspection commands to editors as
// slash commands. Each entry reuses an existing CLI subcommand — the dispatcher
// calls the same run* function the terminal does — so there is exactly one
// implementation of each command. Interactive-only commands (provider/MCP/skill
// pickers, model switching, permission profile) have no text-output contract and
// are exposed through ACP config options and methods instead.

type acpCommandSpec struct {
	name        string
	description string
	// hint is shown to the client as the command's input hint. Empty means the
	// command takes no input.
	hint string
	// baseArgs are prepended when the user sends no arguments (e.g. `list` for
	// `kajicode sessions list`), so a bare `/sessions` still works.
	baseArgs []string
	// session marks a command handled against the ACTIVE ACP session (retitle,
	// compact, export) rather than a CLI subcommand; run is nil for these.
	session bool
	run     func(args []string, stdout, stderr io.Writer, deps appDeps) int
}

// acpCommandSpecs builds the catalog lazily (not as a package-level var) to
// avoid an initialization cycle: the CLI's Run dispatch reaches runACP, which
// builds the catalog, which references the same run* functions.
func acpCommandSpecs() []acpCommandSpec {
	return []acpCommandSpec{
		{name: "config", description: "Show the resolved KajiCode configuration.", run: runConfig},
		{name: "context", description: "Report workspace and runtime context usage.", run: runContext},
		{name: "tools", description: "List registered plugin-tools.", hint: "list", baseArgs: []string{"list"}, run: runTools},
		{name: "skills", description: "List installed skills.", run: runSkills},
		{name: "agents", description: "List available sub-agents.", run: runAgents},
		{name: "models", description: "List model registry entries.", run: func(args []string, stdout, stderr io.Writer, _ appDeps) int {
			return runModels(args, stdout, stderr)
		}},
		{name: "sessions", description: "List recent local sessions.", hint: "list", baseArgs: []string{"list"}, run: runSessions},
		{name: "usage", description: "Summarize token usage and estimated cost.", run: runUsage},
		{name: "doctor", description: "Run backend health checks.", run: runDoctor},
		{name: "verify", description: "Detect and run local verification checks.", run: runVerifyCommand},
		{name: "changes", description: "Inspect local git changes.", hint: "status", baseArgs: []string{"status"}, run: runChanges},
		{name: "repo-info", description: "Characterize the current repository (local git).", run: runRepoInfo},
		{name: "repo-map", description: "Build a deterministic repository map.", run: runRepoMap},
		{name: "prompt-inspect", description: "Inspect runtime prompt sections and token estimates.", hint: "inspect", baseArgs: []string{"inspect"}, run: runPrompt},
		{name: "prompt", description: "Create a reusable prompt snippet (/name).", hint: "name", session: true},
		{name: "harness", description: "Show harness prompt addenda and permission rules.", run: runHarness},
		{name: "learning", description: "Show self-learning settings.", run: runLearningConfig},
		{name: "sandbox", description: "Show the sandbox policy.", hint: "policy", baseArgs: []string{"policy"}, run: runSandbox},
		{name: "trust", description: "Show whether the workspace is trusted.", run: runTrust},
		{name: "mcp", description: "Show MCP server status.", hint: "list", baseArgs: []string{"list"}, run: runMCP},
		{name: "hooks", description: "List configured hooks.", hint: "list", baseArgs: []string{"list"}, run: runHooks},
		{name: "plugins", description: "List installed plugins.", hint: "list", baseArgs: []string{"list"}, run: runPlugins},
		{name: "backends", description: "Inspect MCP, hook, and plugin backend state.", run: runBackends},
		{name: "cron", description: "List scheduled agent jobs.", hint: "list", baseArgs: []string{"list"}, run: runCron},
		{name: "search", description: "Search local session events.", hint: "query", run: runSearch},
		{name: "sandbox-setup", description: "Run native sandbox setup for this platform.", run: runSandboxSetup},
		{name: "web-search", description: "Configure web-search credentials (status/set/remove).", hint: "status", baseArgs: []string{"status"}, run: runWebSearch},
		{name: "add-provider", description: "Add or update a provider (name, base URL, API key).", session: true},
		{name: "refresh-models", description: "Re-discover every provider's models and refresh the model selector.", session: true},
		{name: "retitle", description: "Generate a concise title for this session.", session: true},
		{name: "compact", description: "Compact this session's history now.", session: true},
		{name: "export", description: "Export this session's conversation as plain text.", session: true},
	}
}

// acpCommands projects the catalog into the wire form advertised to clients,
// including the user's saved prompt snippets (usercommands) so a saved /command
// appears in the client's slash palette and prompts for its arguments.
func acpCommands(deps appDeps) func(workspaceRoot string) []acp.AvailableCommand {
	return func(workspaceRoot string) []acp.AvailableCommand {
		specs := acpCommandSpecs()
		out := make([]acp.AvailableCommand, 0, len(specs))
		seen := map[string]bool{}
		for _, spec := range specs {
			command := acp.AvailableCommand{Name: spec.name, Description: spec.description}
			if spec.hint != "" {
				command.Input = &acp.AvailableCommandInput{Hint: spec.hint}
			}
			out = append(out, command)
			seen[spec.name] = true
		}
		for _, snippet := range acpUserCommandSnippets(deps, workspaceRoot) {
			if seen[snippet.Name] {
				continue // a builtin/ACP command wins on a name collision
			}
			snippet.Input = &acp.AvailableCommandInput{Hint: "arguments"}
			out = append(out, snippet)
			seen[snippet.Name] = true
		}
		for _, skill := range acpSkillCommands(deps, workspaceRoot) {
			if seen[skill.Name] {
				continue // a builtin/ACP/usercmd command wins on a name collision
			}
			skill.Input = &acp.AvailableCommandInput{Hint: "request"}
			out = append(out, skill)
			seen[skill.Name] = true
		}
		return out
	}
}

// acpSkillCommands projects the workspace's invocable skills into advertised
// slash commands, so an editor's palette shows each skill as /name (the same
// form the TUI supports). Names already claimed by a builtin/ACP command or a
// saved prompt snippet are skipped, matching the invocation precedence.
func acpSkillCommands(deps appDeps, workspaceRoot string) []acp.AvailableCommand {
	byName := acpSkillNames(workspaceRoot, deps)
	out := make([]acp.AvailableCommand, 0, len(byName))
	for name, skill := range byName {
		if acpSkillClaimed(workspaceRoot, name, deps) {
			continue
		}
		out = append(out, acp.AvailableCommand{Name: name, Description: strings.TrimSpace(skill.Description)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// acpUserCommandSnippets loads the user's file-sourced prompt snippets for the
// workspace, ignoring any whose name collides with a builtin command.
func acpUserCommandSnippets(deps appDeps, workspaceRoot string) []acp.AvailableCommand {
	paths := usercommands.Paths{}
	if dir := acpUserCommandDir(deps); dir != "" {
		paths.UserDir = dir
	}
	if strings.TrimSpace(workspaceRoot) != "" {
		paths.ProjectDir = filepath.Join(workspaceRoot, ".kajicode", "commands")
	}
	snippets := usercommands.Load(paths)
	out := make([]acp.AvailableCommand, 0, len(snippets))
	for _, snippet := range snippets {
		out = append(out, acp.AvailableCommand{Name: snippet.Name, Description: snippet.Description})
	}
	return out
}

// commandChdirMu serializes the process-wide working-directory swap a command
// run performs. ACP serializes turns per session, but two sessions may run at
// once; only one may chdir at a time.
var commandChdirMu sync.Mutex

// acpMaxCommandBytes bounds a single command's output before it is streamed to
// the client. Listing commands (sessions, skills) are unbounded and would flood
// the wire and the model's context; truncation is explicit so a client knows it
// is seeing a prefix.
const acpMaxCommandBytes = 64 << 10

// limitedWriter accepts up to cap bytes and discards the rest, recording that
// it truncated. It satisfies io.Writer, so a subcommand writing into it never
// buffers more than cap even if it produces an unbounded stream.
type limitedWriter struct {
	buf       bytes.Buffer
	cap       int
	truncated bool
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	remaining := w.cap - w.buf.Len()
	if remaining <= 0 {
		w.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		w.truncated = true
		w.buf.Write(p[:remaining])
		return len(p), nil
	}
	w.buf.Write(p)
	return len(p), nil
}

// String returns the captured output trimmed to a UTF-8 boundary with a marker
// appended when the command wrote more than the cap.
func (w *limitedWriter) String() string {
	out := w.buf.String()
	if !w.truncated {
		return out
	}
	for len(out) > 0 && !utf8.ValidString(out) {
		out = out[:len(out)-1]
	}
	return out + "\n\n[output truncated at 64 KiB]\n"
}

// ACPSessionCommand handles the commands that act on the ACTIVE ACP session
// rather than a CLI subcommand: `/compact` and `/export` operate on the session
// id the client is talking to, in the session's workspace. ok=false means the
// command is not one of these and the dispatcher should try the CLI catalog.
type ACPSessionCommand func(command, args, cwd, sessionID string) (output string, ok bool, err error)

// acpRunCommand builds the dispatcher the ACP agent calls for a "/name args"
// prompt. It first consults sessionCommands (compact/export on the live session),
// then the CLI catalog with the session's workspace as the working directory.
// ok=false means the command name is unknown, so the agent treats the text as an
// ordinary prompt.
func acpRunCommand(deps appDeps, sessionCommands ACPSessionCommand) func(context.Context, string, string, string, string) (string, bool, error) {
	return func(_ context.Context, command, args, cwd, sessionID string) (string, bool, error) {
		if sessionCommands != nil {
			if output, handled, err := sessionCommands(command, args, cwd, sessionID); handled {
				return output, true, err
			}
		}
		spec, ok := lookupACPCommand(command)
		if !ok || spec.run == nil {
			return "", false, nil
		}
		fields := strings.Fields(args)
		if len(fields) == 0 {
			fields = spec.baseArgs
		}

		commandChdirMu.Lock()
		restore := acpChdir(cwd, deps)
		defer func() {
			restore()
			commandChdirMu.Unlock()
		}()

		writer := &limitedWriter{cap: acpMaxCommandBytes}
		code := spec.run(fields, writer, writer, deps)
		output := writer.String()
		if code != 0 && strings.TrimSpace(output) == "" {
			return "", true, fmt.Errorf("command failed with exit code %d", code)
		}
		return output, true, nil
	}
}

// isSessionCommand reports whether name is one of the session-scoped ACP
// commands (handled against the live session, not a CLI subcommand).
func isSessionCommand(name string) bool {
	name = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(name, "/")))
	for _, spec := range acpCommandSpecs() {
		if spec.session && spec.name == name {
			return true
		}
	}
	return false
}

// lookupACPCommand resolves a command name (with or without a leading slash,
// case-insensitively) against the catalog.
func lookupACPCommand(command string) (acpCommandSpec, bool) {
	name := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(command, "/")))
	for _, spec := range acpCommandSpecs() {
		if spec.name == name {
			return spec, true
		}
	}
	return acpCommandSpec{}, false
}

// acpChdir temporarily switches the process working directory to dir so a
// command resolves the session's workspace exactly like the terminal would. It
// returns a no-op restore when the directory is empty, already current, or
// cannot be entered.
func acpChdir(dir string, deps appDeps) func() {
	current, err := deps.getwd()
	if err != nil || strings.TrimSpace(dir) == "" || dir == current {
		return func() {}
	}
	if err := os.Chdir(dir); err != nil {
		return func() {}
	}
	return func() { _ = os.Chdir(current) }
}

// acpSessionCommand handles the commands that act on the ACTIVE ACP session
// rather than a CLI subcommand. `/compact` and `/export` operate on the session
// id the client is talking to, in the session's workspace; each runs with that
// workspace as the working directory and its output is capped like any other
// command so a large export cannot flood the wire.
func acpSessionCommand(deps appDeps) ACPSessionCommand {
	return func(command, args, cwd, sessionID string) (string, bool, error) {
		switch strings.ToLower(strings.TrimSpace(command)) {
		case "compact", "export":
			commandChdirMu.Lock()
			restore := acpChdir(cwd, deps)
			defer func() {
				restore()
				commandChdirMu.Unlock()
			}()
			writer := &limitedWriter{cap: acpMaxCommandBytes}
			store := deps.newSessionStore()
			var code int
			if command == "compact" {
				code = runSessionsCompact(store, sessionID, sessionCommandOptions{}, writer, writer, deps)
			} else {
				code = runSessionsExport(store, sessionID, sessionCommandOptions{}, writer, writer, deps)
			}
			if code != exitSuccess {
				return writer.String(), true, fmt.Errorf("%s failed", command)
			}
			return writer.String(), true, nil
		default:
			return "", false, nil
		}
	}
}

// acpRetitle generates and persists a title for the active ACP session, returning
// the new title so the agent can emit session_info_update. It resolves the active
// provider the same way the CLI retitle command does.
func acpRetitle(deps appDeps) func(ctx context.Context, sessionID, cwd string) (string, error) {
	return func(ctx context.Context, sessionID, cwd string) (string, error) {
		commandChdirMu.Lock()
		defer commandChdirMu.Unlock()
		provider, err := sessionProviderIn(cwd, deps)
		if err != nil {
			return "", err
		}
		store := deps.newSessionStore()
		meta, err := store.RetitleSession(ctx, sessionID, provider, agent.IsNoProgressStop)
		if err != nil {
			return "", err
		}
		return meta.Title, nil
	}
}
