package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/dishant0406/KajiCode/internal/acp"
	"github.com/dishant0406/KajiCode/internal/config"
	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
)

// Wiring for the ACP v1-unstable surface (document sync, NES, providers).

// acpSuggestNes asks the active provider for a next-edit suggestion for the
// buffered document. It returns nil (no suggestion) rather than an error when no
// provider is configured, so NES degrades cleanly.
func acpSuggestNes(deps appDeps) func(ctx context.Context, input acp.NesSuggestInput) (*acp.NesEditSuggestion, error) {
	return func(ctx context.Context, input acp.NesSuggestInput) (*acp.NesEditSuggestion, error) {
		provider, err := sessionProviderIn(input.Cwd, deps)
		if err != nil {
			return nil, err
		}
		if provider == nil {
			return nil, nil
		}
		return suggestNesWithProvider(ctx, provider, input)
	}
}

// suggestNesWithProvider builds a bounded prompt around the cursor position and
// parses the model's single-edit reply into an ACP suggestion. The reply format
// is line-based so it is easy to parse and easy to see: "newText" on the lines
// after a "RANGE line character line character" header.
func suggestNesWithProvider(ctx context.Context, provider kajicoderuntime.Provider, input acp.NesSuggestInput) (*acp.NesEditSuggestion, error) {
	prompt := buildNesPrompt(input)
	stream, err := provider.StreamCompletion(ctx, kajicoderuntime.CompletionRequest{
		Messages: []kajicoderuntime.Message{
			{Role: kajicoderuntime.MessageRoleSystem, Content: nesSystemPrompt},
			{Role: kajicoderuntime.MessageRoleUser, Content: prompt},
		},
	})
	if err != nil {
		return nil, err
	}
	collected := kajicoderuntime.CollectStream(ctx, stream)
	if collected.Error != "" {
		return nil, errors.New(collected.Error)
	}
	line := cursorLine(input.Text, input.Position.Line)
	edit, ok := parseNesEdit(collected.Text, line)
	if !ok {
		return nil, nil
	}
	// The range is line-relative; shift it to the document line the cursor is on.
	edit.Range.Start.Line = input.Position.Line
	edit.Range.End.Line = input.Position.Line
	id := fmt.Sprintf("nes_%d_%d_%d", input.Version, input.Position.Line, input.Position.Character)
	position := input.Position
	return &acp.NesEditSuggestion{
		ID:             id,
		URI:            input.URI,
		Edits:          []acp.NesTextEdit{edit},
		CursorPosition: &position,
	}, nil
}

// nesMaxPromptBytes bounds the document text embedded in a NES prompt.
const nesMaxPromptBytes = 16 << 10

// truncateBytes caps s at max bytes on a UTF-8 boundary.
func truncateBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

const nesSystemPrompt = "You are a code editor completing the user's next edit at the cursor. " +
	"Reply with ONLY the replacement for the current line, and nothing else — no explanation, no markdown, no code fence. " +
	"If the line needs no change, reply with exactly: NONE"

// buildNesPrompt renders a bounded window around the cursor line so the prompt
// stays cheap regardless of document size.
func buildNesPrompt(input acp.NesSuggestInput) string {
	lines := strings.Split(input.Text, "\n")
	if input.Position.Line < 0 || input.Position.Line >= len(lines) {
		return fmt.Sprintf("Language: %s\n\n%s\n\nEdit the line at the cursor.", input.LanguageID, truncateBytes(input.Text, nesMaxPromptBytes))
	}
	const window = 20
	start := input.Position.Line - window
	if start < 0 {
		start = 0
	}
	end := input.Position.Line + window + 1
	if end > len(lines) {
		end = len(lines)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Language: %s\n", input.LanguageID)
	b.WriteString("Document (lines " + fmt.Sprint(start+1) + "-" + fmt.Sprint(end) + "):\n")
	for i := start; i < end; i++ {
		marker := "  "
		if i == input.Position.Line {
			marker = "> "
		}
		fmt.Fprintf(&b, "%s%d: %s\n", marker, i+1, lines[i])
	}
	fmt.Fprintf(&b, "\nThe cursor is on line %d at character %d. Replace that line.\n", input.Position.Line+1, input.Position.Character)
	return b.String()
}

// parseNesEdit extracts the replacement text for the cursor line and builds a
// real edit range covering that whole line. A "NONE"/empty reply means no
// suggestion. line is the current text of the cursor line (used only to size the
// range in bytes; content is not otherwise needed).
func parseNesEdit(raw, line string) (acp.NesTextEdit, bool) {
	text := stripNesReply(raw)
	if text == "" {
		return acp.NesTextEdit{}, false
	}
	// Replace the whole cursor line: line 0-based, characters are byte offsets.
	return acp.NesTextEdit{
		Range:   acp.Range{Start: acp.Position{Line: 0, Character: 0}, End: acp.Position{Line: 0, Character: len(line)}},
		NewText: text,
	}, true
}

// stripNesReply normalizes a model reply into the replacement text: trims
// whitespace, drops a surrounding code fence (only when actually fenced), and
// treats a lone "NONE" as no suggestion. It never strips substrings like "text"
// from real content.
func stripNesReply(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" || strings.EqualFold(text, "NONE") {
		return ""
	}
	if strings.HasPrefix(text, "```") {
		text = strings.TrimPrefix(text, "```")
		// Drop a language tag on the fence line.
		if nl := strings.IndexByte(text, '\n'); nl >= 0 && !strings.ContainsAny(text[:nl], " \t") {
			text = text[nl+1:]
		}
		text = strings.TrimSuffix(strings.TrimSpace(text), "```")
	}
	text = strings.TrimRight(text, "\n")
	if strings.TrimSpace(text) == "" || strings.EqualFold(strings.TrimSpace(text), "NONE") {
		return ""
	}
	return text
}

// cursorLine returns the text of the given 0-based line, or "" when out of range.
func cursorLine(text string, line int) string {
	if line < 0 {
		return ""
	}
	lines := strings.Split(text, "\n")
	if line >= len(lines) {
		return ""
	}
	return lines[line]
}

// acpListProviders maps KajiCode's configured provider profiles onto the ACP
// provider list. `supported` are the API protocols the profile can speak;
// `current` is the non-secret routing config (never the key).
func acpListProviders(deps appDeps) func() ([]acp.ProviderInfo, error) {
	return func() ([]acp.ProviderInfo, error) {
		workspaceRoot, err := resolveWorkspaceRoot("", deps)
		if err != nil {
			return nil, err
		}
		resolved, err := deps.resolveConfig(workspaceRoot, config.Overrides{})
		if err != nil {
			return nil, err
		}
		infos := make([]acp.ProviderInfo, 0, len(resolved.Providers))
		for _, profile := range resolved.Providers {
			active := strings.EqualFold(profile.Name, resolved.ActiveProvider)
			info := acp.ProviderInfo{
				ProviderID: profile.Name,
				Supported:  []string{llmProtocol(profile.APIFormat)},
				Required:   true,
			}
			if active {
				info.Current = &acp.ProviderCurrentInfo{APIType: llmProtocol(profile.APIFormat), BaseURL: profile.BaseURL}
			}
			infos = append(infos, info)
		}
		return infos, nil
	}
}

// llmProtocol maps a KajiCode API format onto the ACP LlmProtocol enum.
func llmProtocol(apiFormat string) string {
	switch strings.ToLower(strings.TrimSpace(apiFormat)) {
	case "messages":
		return "anthropic"
	case "generate-content", "vertex-generate-content":
		return "vertex"
	case "bedrock-converse":
		return "bedrock"
	case "responses", "chat-completions", "":
		return "openai"
	default:
		return "openai"
	}
}

// acpSetProvider switches the active provider to providerID, reusing
// `providers use`. KajiCode's provider profiles are created through
// `providers add`; ACP `providers/set` cannot carry the API key, so an inline
// routing change (apiType/baseUrl) is not applied here. Supplying one is
// rejected rather than silently ignored, so a client is never told a base URL
// change took effect when it did not.
func acpSetProvider(deps appDeps) func(providerID, apiType, baseURL string, headers map[string]string) error {
	return func(providerID, apiType, baseURL string, headers map[string]string) error {
		if strings.TrimSpace(baseURL) != "" {
			return fmt.Errorf("providers/set cannot change baseUrl over ACP; run `kajicode providers add %s --base-url <url>`", providerID)
		}
		var out strings.Builder
		if code := runProvidersUse([]string{providerID}, &out, &out, deps); code != exitSuccess {
			return errFromWriter(out.String())
		}
		return nil
	}
}

// acpDisableProvider makes providerID no longer the active provider, reusing
// `providers use` to switch to another configured provider. KajiCode has no
// per-profile "disabled" flag, so "disabled" means "not in use". Disabling an
// already-inactive provider is a successful no-op (the desired end state);
// disabling the only provider is refused.
func acpDisableProvider(deps appDeps) func(providerID string) error {
	return func(providerID string) error {
		workspaceRoot, err := resolveWorkspaceRoot("", deps)
		if err != nil {
			return err
		}
		resolved, err := deps.resolveConfig(workspaceRoot, config.Overrides{})
		if err != nil {
			return err
		}
		if !strings.EqualFold(resolved.ActiveProvider, providerID) {
			return nil
		}
		for _, profile := range resolved.Providers {
			if strings.EqualFold(profile.Name, providerID) {
				continue
			}
			var out strings.Builder
			if code := runProvidersUse([]string{profile.Name}, &out, &out, deps); code != exitSuccess {
				return errFromWriter(out.String())
			}
			return nil
		}
		return fmt.Errorf("cannot disable %q: it is the only configured provider", providerID)
	}
}
