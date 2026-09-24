package sessions

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
)

// Session-title generation shared by every surface (TUI, headless CLI, ACP).
// The logic is provider-shaped as a single system + user turn with no tools, so
// it works identically from the TUI, `kajicode sessions retitle`, and ACP.

const (
	// TitleMaxMessageChars caps how much of any single message feeds the title
	// prompt — a title needs the gist, not the whole turn.
	TitleMaxMessageChars = 320
	// TitleMaxDigestChars bounds the whole digest so titling stays one cheap call.
	TitleMaxDigestChars = 1600
	// TitleWordCap is the most words a cleaned title keeps.
	TitleWordCap = 8
	// TitleLimit is the maximum rune length of a stored title, matching the
	// limit session creation applies to a first-message title.
	TitleLimit = 80
)

// TitleTrimCutset is stripped from both ends of a model-produced title:
// whitespace, surrounding quotes/backticks, markdown marks, trailing punctuation.
const TitleTrimCutset = " \t\r\n\"'`*#.,:;!"

// TitleSystemPrompt instructs the model to emit only a short title.
const TitleSystemPrompt = "You write a short, specific title for a coding-assistant conversation so a user can tell it apart from others in a list. " +
	"Reply with ONLY the title and nothing else: 3 to 6 words, Title Case, naming the concrete task or topic. " +
	"No surrounding quotes, no trailing punctuation, no preamble, no explanation."

// ErrTitleNoContent marks a session with nothing worth titling, so a caller skips
// the provider call entirely.
var ErrTitleNoContent = errors.New("session has no content to title")

// TitleDigest renders a compact, bounded transcript of a session for the title
// prompt: user/assistant text and tool names, each trimmed, the whole thing
// capped. skip reports message content that is not real progress (a failed run's
// guardrail stop), so a caller can exclude it; nil skips nothing.
func TitleDigest(events []Event, skip func(content string) bool) string {
	var builder strings.Builder
	total := 0
	add := func(label, content string) bool {
		content = strings.Join(strings.Fields(content), " ")
		if content == "" {
			return true
		}
		content = CutRunes(content, TitleMaxMessageChars)
		line := label + ": " + content + "\n"
		if total > 0 && total+len(line) > TitleMaxDigestChars {
			return false
		}
		builder.WriteString(line)
		total += len(line)
		return true
	}
	for _, event := range events {
		payload := eventPayload(event)
		switch event.Type {
		case EventMessage:
			content := payloadString(payload, "content")
			if skip != nil && skip(content) {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(payloadString(payload, "role"))) {
			case "user":
				if !add("User", content) {
					return strings.TrimSpace(builder.String())
				}
			case "assistant":
				if !add("Assistant", content) {
					return strings.TrimSpace(builder.String())
				}
			}
		case EventToolCall:
			if name := strings.TrimSpace(payloadString(payload, "name")); name != "" {
				if !add("Tool", name) {
					return strings.TrimSpace(builder.String())
				}
			}
		}
	}
	return strings.TrimSpace(builder.String())
}

// CleanTitle normalizes a raw model response into a single short title line:
// first non-empty line only, surrounding quotes/markup and a leading "Title:"
// label removed, whitespace collapsed, word- and rune-capped. Returns "" when
// nothing usable remains so the caller keeps the existing title.
func CleanTitle(raw string) string {
	title := strings.TrimSpace(raw)
	if title == "" {
		return ""
	}
	for _, line := range strings.Split(title, "\n") {
		trimmed := strings.Trim(line, TitleTrimCutset)
		// Skip a code-fence opener, including a language tag (```go, ~~~json).
		if trimmed == "" || strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			continue
		}
		title = trimmed
		break
	}
	title = strings.Trim(title, TitleTrimCutset)
	if idx := strings.IndexAny(title, ":-"); idx > 0 && idx <= 6 {
		if strings.EqualFold(strings.TrimSpace(title[:idx]), "title") {
			title = strings.TrimSpace(title[idx+1:])
		}
	}
	title = strings.Trim(title, TitleTrimCutset)
	fields := strings.Fields(title)
	if len(fields) == 0 {
		return ""
	}
	if len(fields) > TitleWordCap {
		fields = fields[:TitleWordCap]
	}
	return CutRunes(strings.Join(fields, " "), TitleLimit)
}

// GenerateTitle asks the provider for a concise title for digest and returns the
// cleaned result. It is provider-shaped exactly like the one-shot summarization
// call: system instructions + one user turn, no tools.
func GenerateTitle(ctx context.Context, provider kajicoderuntime.Provider, digest string) (string, error) {
	if provider == nil {
		return "", errors.New("no provider configured")
	}
	if strings.TrimSpace(digest) == "" {
		return "", ErrTitleNoContent
	}
	request := kajicoderuntime.CompletionRequest{
		Messages: []kajicoderuntime.Message{
			{Role: kajicoderuntime.MessageRoleSystem, Content: TitleSystemPrompt},
			{Role: kajicoderuntime.MessageRoleUser, Content: "Conversation:\n\n" + digest + "\n\nTitle:"},
		},
	}
	stream, err := provider.StreamCompletion(ctx, request)
	if err != nil {
		return "", err
	}
	collected := kajicoderuntime.CollectStreamWithOptions(ctx, stream, kajicoderuntime.CollectOptions{})
	if collected.Error != "" {
		return "", errors.New(collected.Error)
	}
	title := CleanTitle(collected.Text)
	if title == "" {
		return "", errors.New("model returned no usable title")
	}
	return title, nil
}

// RetitleSession generates a title for a stored session from its events, applies
// any skip predicate, and persists the result with UpdateTitle. It returns the
// updated metadata. skip may be nil.
func (store *Store) RetitleSession(ctx context.Context, sessionID string, provider kajicoderuntime.Provider, skip func(string) bool) (Metadata, error) {
	events, err := store.ReadEvents(sessionID)
	if err != nil {
		return Metadata{}, err
	}
	digest := TitleDigest(events, skip)
	title, err := GenerateTitle(ctx, provider, digest)
	if err != nil {
		return Metadata{}, err
	}
	return store.UpdateTitle(sessionID, title)
}

// CutRunes truncates text to limit bytes without splitting a UTF-8 rune.
func CutRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(text) <= limit {
		return text
	}
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	return text[:limit]
}
