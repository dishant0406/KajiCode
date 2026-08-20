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
	promptSectionModeContract    promptSectionRole = "mode-contract"
	promptSectionSpecialists     promptSectionRole = "specialists"
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

func modeContractContext(options Options) string {
	switch options.PermissionMode {
	case PermissionModeSpecDraft:
		return `<mode_contract>
Mode: spec-draft.
This is a planning/specification pass. Use only read-only inspection tools plus ask_user when genuinely blocked.
Do not edit files, run shell commands, spawn specialists, request extra permissions, or call update_plan.
The only write-capable exception is submit_spec, which saves the completed draft under .kajicode/specs and stops for review.
</mode_contract>`
	default:
		return ""
	}
}
