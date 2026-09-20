package agents

// Builtins returns the agents shipped with KajiCode. They are ordinary Agent
// values (not files) so a run always has a usable delegation set even with no
// user or project definitions on disk. Project and user definitions with the
// same name override them.
func Builtins() []Agent {
	builtins := []Agent{
		{
			Name:         "worker",
			Description:  "Handles general delegated coding tasks and reports concrete outcomes.",
			Tools:        []string{"read", "edit", "execute", "plan"},
			Mode:         ModeSubagent,
			SystemPrompt: workerPrompt,
		},
		{
			Name:         "planner",
			Description:  "Produces concrete implementation plans from read-only codebase inspection.",
			Tools:        []string{"read", "plan"},
			Mode:         ModeSubagent,
			SystemPrompt: plannerPrompt,
		},
		{
			Name:         "explorer",
			Description:  "Performs fast read-only codebase exploration without modifying files.",
			Tools:        []string{"read"},
			Mode:         ModeSubagent,
			SystemPrompt: explorerPrompt,
		},
		{
			Name:         "verifier",
			Description:  "Runs focused validation and reports failures, likely causes, and next fixes.",
			Tools:        []string{"read", "execute"},
			Mode:         ModeSubagent,
			SystemPrompt: verifierPrompt,
		},
		{
			Name:         "code-review",
			Description:  "Reviews code changes for correctness, regressions, and missing tests.",
			Tools:        []string{"read"},
			Mode:         ModeSubagent,
			SystemPrompt: codeReviewPrompt,
		},
	}
	for index := range builtins {
		builtins[index].Location = LocationBuiltin
		builtins[index].FilePath = "(builtin)"
		if err := Validate(&builtins[index]); err != nil {
			// A malformed builtin is a programming error, not user input: fail
			// loudly at startup rather than silently shipping no agents.
			panic("invalid built-in agent " + builtins[index].Name + ": " + err.Error())
		}
	}
	return builtins
}

const workerPrompt = `You are a focused task agent inside KajiCode.

Complete the assigned task precisely, stay within scope, and report:
- the concrete work performed
- the outcome
- any blockers or follow-ups`

const plannerPrompt = `You are a read-only planning agent inside KajiCode.

Inspect the codebase enough to produce one concrete implementation plan. Do not
edit files or run shell commands. Prefer repository structure, existing tests,
and local conventions over generic architecture advice. Report:
- the recommended approach
- files or packages to touch
- tests and validation commands
- risks and sequencing notes`

const explorerPrompt = `You are a read-only codebase exploration agent inside KajiCode.

Find relevant files, symbols, tests, and behavior quickly. Do not edit files or run shell commands. Report concise findings with paths and line references when useful.`

const verifierPrompt = `You are a validation agent inside KajiCode.

Run the narrowest useful checks for the assigned change. Do not edit files.
Summarize exact commands, pass/fail status, important failure lines, and the
next fix that would unblock the run.`

const codeReviewPrompt = `You are a code review agent inside KajiCode.

Review changes for correctness bugs, regressions, unsafe behavior, and missing tests. Prioritize actionable findings over style feedback.`
