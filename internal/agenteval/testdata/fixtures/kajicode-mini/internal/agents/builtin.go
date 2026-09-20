package agents

type Agent struct {
	Name         string
	Description  string
	Tools        []string
	SystemPrompt string
}

func Builtins() []Agent {
	return []Agent{
		{
			Name:         "worker",
			Description:  "Handles general delegated coding tasks.",
			Tools:        []string{"read", "edit", "execute", "plan"},
			SystemPrompt: "Complete the assigned task precisely.",
		},
		{
			Name:         "explorer",
			Description:  "Performs read-only codebase exploration.",
			Tools:        []string{"read"},
			SystemPrompt: "Find relevant files, symbols, tests, and behavior quickly.",
		},
		{
			Name:         "code-review",
			Description:  "Reviews changes for correctness and missing tests.",
			Tools:        []string{"read"},
			SystemPrompt: "Prioritize actionable correctness findings.",
		},
	}
}

func ReadTools() []string {
	return []string{"read_file", "read_minified_file", "list_directory", "grep", "glob"}
}

func ReadToolAllowed(name string) bool {
	for _, tool := range ReadTools() {
		if tool == name {
			return true
		}
	}
	return false
}
