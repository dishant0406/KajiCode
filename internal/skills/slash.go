package skills

import "strings"

// SlashName maps a skill's frontmatter name to its slash-command form: the name
// lowercased, and only if it fits the slash-token shape (letters, digits,
// dot/underscore/hyphen). Returns "" for names that cannot be typed as a
// /command (e.g. containing spaces); those skills remain loadable by the model
// via the skill tool and are still listed by the skill listing, but have no
// slash form.
//
// This is the single source of truth for the slash shape, shared by the TUI
// (command dispatch and completion) and ACP (advertised commands and routing),
// so both agree on which skills are invocable as /name.
func SlashName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return ""
	}
	for _, r := range name {
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.'
		if !valid {
			return ""
		}
	}
	return name
}

// bareInvocationNote is appended when a skill is invoked with no request. The
// body alone is instructions with no target ("review the PR" — which PR?), and
// without this note the model improvises one instead of asking. The wording is
// conditional so self-contained skills (no target needed) still just run.
const bareInvocationNote = "The user invoked this skill directly without providing a request. " +
	"If these instructions need a target or details that are not already clear from the conversation " +
	"(which pull request, file, branch, topic, …), ask for them first — do not guess or pick one yourself. " +
	"If the instructions are self-contained, proceed."

// InvocationPrompt builds the agent prompt for a direct skill invocation: the
// skill body (its instructions), then either the user's request or — for a bare
// invocation — the ask-first note. Shared by the TUI (/name dispatch) and ACP
// (/{name} expansion) so both invoke a skill identically.
func InvocationPrompt(body, args string) string {
	body = strings.TrimSpace(body)
	if strings.TrimSpace(args) != "" {
		return body + "\n\n" + strings.TrimSpace(args)
	}
	return body + "\n\n" + bareInvocationNote
}
