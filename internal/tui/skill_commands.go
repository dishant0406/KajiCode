package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Gitlawb/zero/internal/skills"
)

// Skill slash invocation: "/<skill-name> [args]" runs an installed skill
// directly, mirroring user commands (.zero/commands). Skills were previously
// model-pulled only (the skill tool), which made invocation a matter of model
// discretion; typing the skill's name makes it deterministic. Precedence is
// builtin > user command > skill: parseCommand resolves builtins first, and the
// commandUnknown fallback tries user commands before skills.

// handleSkillCommand resolves a "/name args" that wasn't a builtin or a user
// command against the installed skills. On a match it launches a normal agent
// turn whose prompt inlines the skill body (its instructions) followed by the
// typed args, returning handled=true. handled=false means no skill matched, so
// the caller falls through to "unknown command".
func (m model) handleSkillCommand(raw string) (model, tea.Cmd, bool) {
	name, args := splitUserCommand(raw)
	if name == "" {
		return m, nil, false
	}
	skill, ok := m.lookupSkillCommand(name)
	if !ok {
		return m, nil, false
	}
	body := strings.TrimSpace(skill.Content)
	if body == "" {
		// The name matched a real skill, so falling through to "unknown command"
		// would mislead; surface the actual problem instead.
		m.transcript = reduceTranscript(m.transcript, transcriptAction{
			kind: actionAppendError,
			text: "skill /" + name + " has an empty SKILL.md body (" + skill.Path + ")",
		})
		return m, nil, true
	}
	prompt := body
	if args != "" {
		// Mirror usercommands.Expand's no-placeholder behavior: the skill body is
		// the guidance, the typed args are the specific request it applies to.
		prompt += "\n\n" + args
	}
	return m.launchOrDeferExpandedPrompt(prompt)
}

// launchOrDeferExpandedPrompt applies the same run-state guards the plain
// commandPrompt path has to an expanded (user command / skill) prompt: while
// exiting nothing may start, a prompt submitted mid-run is queued for the next
// turn, and compaction-in-flight warns instead of racing the compactor. The
// EXPANDED prompt is what gets queued — the queue flush path resubmits text as
// a literal prompt, so queuing the raw "/name args" would send it to the model
// as prose instead of re-dispatching it.
func (m model) launchOrDeferExpandedPrompt(prompt string) (model, tea.Cmd, bool) {
	if m.exiting {
		return m, nil, true
	}
	if m.pending {
		return m.queueMessage(prompt), nil, true
	}
	if m.compactInFlight {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{
			kind: actionAppendSystem,
			text: "Compact\nstatus: warning\nCompaction is running. Re-run the command when it finishes.",
		})
		return m, nil, true
	}
	next, teaCmd := m.launchPrompt(prompt)
	return next, teaCmd, true
}

// lookupSkillCommand returns the installed skill whose slash name matches the
// given (lowercased) name. Linear scan over a fresh load — skills are re-read
// per invocation so a skill installed mid-session is invocable without a
// restart, and the body is never held on the model.
func (m model) lookupSkillCommand(name string) (skills.Skill, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return skills.Skill{}, false
	}
	for _, skill := range m.installedSkills() {
		if skillSlashName(skill.Name) == name {
			return skill, true
		}
	}
	return skills.Skill{}, false
}

// installedSkills returns the session's installed skills (default dir merged
// with plugin skill roots) via the injected loader, or nil when the session has
// no skills wiring (e.g. bare test models).
func (m model) installedSkills() []skills.Skill {
	if m.loadSkills == nil {
		return nil
	}
	return m.loadSkills()
}

// skillSlashName maps a skill's frontmatter name to its slash-command form:
// lowercased, and only if it fits the slash-token shape (letters, digits,
// dot/underscore/hyphen — a superset of user-command names, since skill names
// are free-form frontmatter). Returns "" for names that cannot be typed as a
// /command (e.g. containing spaces); those skills remain loadable by the model
// via the skill tool and are still listed by /skills.
func skillSlashName(name string) string {
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
