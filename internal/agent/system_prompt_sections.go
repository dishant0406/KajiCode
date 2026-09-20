package agent

import "strings"

type promptSectionRole string

const (
	promptSectionBase            promptSectionRole = "base"
	promptSectionModel           promptSectionRole = "model"
	promptSectionRuntime         promptSectionRole = "runtime"
	promptSectionCommandPrefixes promptSectionRole = "command-prefixes"
	promptSectionWorkspaceSeed   promptSectionRole = "workspace-seed"
	promptSectionUserGuidelines  promptSectionRole = "user-guidelines"
	promptSectionAgentGuidelines promptSectionRole = "agent-guidelines"
	promptSectionProjectContext  promptSectionRole = "project-context"
	promptSectionHarnessConfig   promptSectionRole = "harness-config"
	promptSectionMCPInstructions promptSectionRole = "mcp-instructions"
	promptSectionSkills          promptSectionRole = "skills"
	promptSectionLearning        promptSectionRole = "learning"
	promptSectionResponseStyle   promptSectionRole = "response-style"
	promptSectionConfirmation    promptSectionRole = "confirmation"
)

type promptSection struct {
	role    promptSectionRole
	content string
}

type promptSectionBuilder struct {
	sections []promptSection
}

func (builder *promptSectionBuilder) add(role promptSectionRole, content string) {
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	builder.sections = append(builder.sections, promptSection{role: role, content: content})
}

func (builder promptSectionBuilder) build() systemPromptParts {
	contents := make([]string, 0, len(builder.sections))
	parts := systemPromptParts{
		sections: append([]promptSection(nil), builder.sections...),
	}
	for _, section := range builder.sections {
		contents = append(contents, section.content)
		switch section.role {
		case promptSectionBase:
			parts.baseInstructions = appendDiagnosticSection(parts.baseInstructions, section.content)
		case promptSectionConfirmation:
			parts.confirmationPolicy = appendDiagnosticSection(parts.confirmationPolicy, section.content)
		case promptSectionProjectContext:
			parts.projectContext = appendDiagnosticSection(parts.projectContext, section.content)
		case promptSectionSkills:
			parts.skills = appendDiagnosticSection(parts.skills, section.content)
		}
	}
	parts.prompt = strings.Join(contents, "\n\n")
	return parts
}

func appendDiagnosticSection(existing string, next string) string {
	if existing == "" {
		return next
	}
	return existing + "\n\n" + next
}
