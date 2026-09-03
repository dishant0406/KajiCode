package tui

import (
	"strings"

	"github.com/dishant0406/KajiCode/internal/agent"
)

// Mid-text skill mention (opencode's mention-menu parity): an "@token" that is
// not resolved as a specialist or a file can name an installed skill, so
// "run @code-review on this diff" pulls the skill into the turn without the
// user switching to a leading slash command. Discovery reuses the @-file
// palette plumbing: skill rows are appended after files/specialists and
// completion inserts "@<skill-slug> " at the trigger.

// skillMentionSlug maps a skill's frontmatter name to the @-mention token:
// the same slash-shaped slug used for /commands (letters, digits, dot,
// underscore, hyphen). Returns "" for names that cannot be typed as a token.
func skillMentionSlug(name string) string {
	return skillSlashName(name)
}

// skillMentionSuggestions returns skill rows matching the @-query, styled like
// the slash palette (desc suffixed with "(skill)"). Rows whose slug is claimed
// by a builtin/user command are skipped so completion never inserts a token
// that dispatch would not resolve.
func (m model) skillMentionSuggestions(query string) []commandSuggestion {
	prefix := strings.ToLower(strings.TrimSpace(query))
	taken := m.takenSlashNames()
	var out []commandSuggestion
	for _, info := range m.skillSuggestionInfos() {
		slug := skillMentionSlug(info.Name)
		if slug == "" || taken[slug] {
			continue
		}
		if prefix != "" && !strings.HasPrefix(slug, prefix) {
			continue
		}
		desc := strings.TrimSpace(info.Description)
		if desc == "" {
			desc = "skill"
		} else {
			desc += " (skill)"
		}
		out = append(out, commandSuggestion{Name: "@" + slug, Desc: desc})
		if len(out) >= maxCommandSuggestions {
			break
		}
	}
	return out
}

// expandSkillMentions rewrites every "@<skill-slug>" token in the prompt into
// an instruction for the agent to load that skill via the skill tool, leaving
// the surrounding prose intact. The transcript keeps the user's verbatim
// "@mention" (expansion happens in launchPrompt after the user row is
// appended, matching the specialist-mention path). Unknown @tokens (files,
// anything else) pass through untouched. Returns the rewritten prompt and
// whether anything changed.
func expandSkillMentions(prompt string, skillsList []agent.SkillInfo) (string, bool) {
	if len(skillsList) == 0 || !strings.Contains(prompt, "@") {
		return prompt, false
	}
	slugs := map[string]string{}
	for _, info := range skillsList {
		if slug := skillMentionSlug(info.Name); slug != "" {
			slugs[strings.ToLower(slug)] = strings.TrimSpace(info.Name)
		}
	}
	changed := false
	rewritten := tokenizeAtMentions(prompt, func(token string) (string, bool) {
		if name, ok := slugs[strings.ToLower(token)]; ok {
			changed = true
			return "Load the \"" + name + "\" skill with the skill tool and follow its instructions for this request", true
		}
		return "", false
	})
	return rewritten, changed
}

// tokenizeAtMentions splits text into whitespace-delimited words; for each
// word starting with "@", replace is called with the token (without "@").
// When replace returns ok=true the word is swapped for the replacement;
// otherwise the original word is kept. Non-@ words are always kept.
func tokenizeAtMentions(text string, replace func(token string) (string, bool)) string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return text
	}
	out := make([]string, 0, len(words))
	for _, word := range words {
		// Only "@token" words (len > 1) can name a skill; a bare "@" or any
		// other word passes through, and an unmatched token stays verbatim.
		if strings.HasPrefix(word, "@") && len(word) > 1 {
			if replacement, ok := replace(word[1:]); ok {
				out = append(out, replacement)
				continue
			}
		}
		out = append(out, word)
	}
	return strings.Join(out, " ")
}
