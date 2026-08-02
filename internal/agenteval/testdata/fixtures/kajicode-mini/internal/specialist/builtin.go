package specialist

type Manifest struct {
	Name         string
	Description  string
	Tools        []string
	SystemPrompt string
}

func Builtins() []Manifest {
	return []Manifest{
		{
			Name:         "worker",
			Description:  "Handles general delegated coding tasks.",
			Tools:        []string{"read-only", "edit", "execute", "plan"},
			SystemPrompt: "Complete the assigned task precisely.",
		},
		{
			Name:         "explorer",
			Description:  "Performs read-only codebase exploration.",
			Tools:        []string{"read-only"},
			SystemPrompt: "Find relevant files, symbols, tests, and behavior quickly.",
		},
		{
			Name:         "code-review",
			Description:  "Reviews changes for correctness and missing tests.",
			Tools:        []string{"read-only"},
			SystemPrompt: "Prioritize actionable correctness findings.",
		},
	}
}

func ReadOnlyTools() []string {
	return []string{"read_file", "read_minified_file", "list_directory", "grep", "glob"}
}

func ReadOnlyToolAllowed(name string) bool {
	for _, tool := range ReadOnlyTools() {
		if tool == name {
			return true
		}
	}
	return false
}
