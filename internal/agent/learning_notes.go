package agent

import (
	"sort"
	"strings"
	"time"

	"github.com/dishant0406/KajiCode/internal/harness"
	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
)

// Prompt bounds. Notes are stored on disk up to the configured cap, but only a
// small, shortened slice ever reaches the prompt, so a growing store can never
// blow the model's context window.
const (
	// learnedPromptMaxEntries caps the standing notes in the system prompt.
	learnedPromptMaxEntries = 6
	// learnedPromptMaxTitleLen and learnedPromptMaxContentLen shorten each
	// standing note.
	learnedPromptMaxTitleLen   = 120
	learnedPromptMaxContentLen = 160
	// requestNotesMax caps the notes attached to one request.
	requestNotesMax = 3
	// requestNoteContentLen shortens each attached note; recall shows it in full.
	requestNoteContentLen = 300
)

const (
	requestNotesOpen  = "<notes_from_earlier_sessions>"
	requestNotesClose = "</notes_from_earlier_sessions>"
)

// Context renders the standing notes for the system prompt: the project's and
// session's prompt-kind notes (project rules), most recently edited first. It
// is called at run start by buildSystemPromptParts and again by the loop's
// same-session refresh after a pass lands. It has no side effects, so the TUI
// can render the prompt for inspection; BeginRun records the standing notes as
// used. They are chosen by last edit, not last use, so being reinforced never
// locks a note in place.
func (e *LearningEngine) Context() string {
	if e == nil {
		return ""
	}
	standing := standingEntries(e.loadProjectState(), e.loadSessionState())
	var b strings.Builder
	for _, entry := range standing {
		b.WriteString("- " + harness.Snippet(noteTitle(entry), learnedPromptMaxTitleLen) + ": " + harness.Snippet(entry.Content, learnedPromptMaxContentLen) + "\n")
	}
	return strings.TrimSpace(b.String())
}

// standingEntries picks the prompt-kind notes shown in the system prompt from
// the project and session stores, newest edit first, capped. Global notes are
// never standing: they reach the model only when they match a request.
func standingEntries(project, session harness.State) []harness.Entry {
	var rules []harness.Entry
	for _, entry := range harness.MergeHarnessStates(project, session) {
		if entry.Kind == harness.KindPrompt {
			rules = append(rules, entry)
		}
	}
	// UpdatedAt is always an RFC3339 UTC stamp, so it sorts as text.
	sort.SliceStable(rules, func(i, j int) bool { return rules[i].UpdatedAt > rules[j].UpdatedAt })
	if len(rules) > learnedPromptMaxEntries {
		rules = rules[:learnedPromptMaxEntries]
	}
	return rules
}

// Notes finds the saved notes that match the user's request and renders them
// as a short block for that request, so the model sees relevant lessons without
// having to call recall. Standing notes, which are already in the system
// prompt, are skipped. Matched notes are recorded as used. It returns "" when
// the request is too vague (fewer than two keywords) or nothing matches.
func (e *LearningEngine) Notes(request string) string {
	if e == nil || len(harness.Keywords(request)) < 2 {
		return ""
	}
	global, project, session := e.loadGlobalState(), e.loadProjectState(), e.loadSessionState()
	standing := map[string]bool{}
	for _, entry := range standingEntries(project, session) {
		standing[harness.EntryKey(entry)] = true
	}
	merged := harness.MergeHarnessStates(global, project)
	all := harness.MergeHarnessStates(harness.State{Entries: merged}, session)
	candidates := make([]harness.Entry, 0, len(all))
	for _, entry := range all {
		if !standing[harness.EntryKey(entry)] {
			candidates = append(candidates, entry)
		}
	}
	matched := harness.Search(candidates, request, requestNotesMax)
	if len(matched) == 0 {
		return ""
	}
	e.recordUsed(matched)

	var b strings.Builder
	b.WriteString("Saved notes that may help with this request. Use them only if they fit; the current instructions win.\n")
	for _, entry := range matched {
		b.WriteString("- " + noteTitle(entry) + ": " + harness.Snippet(entry.Content, requestNoteContentLen))
		if hint := entry.RunHint(); hint != "" {
			b.WriteString(" " + hint)
		}
		b.WriteString("\n")
	}
	b.WriteString("Call recall with a few keywords to read a note in full.")
	return b.String()
}

// withRequestNotes adds the notes block to the run's request inside messages,
// which must be a copy that is about to be sent to the provider. The loop's
// own history never carries the notes, so the learning pass, compaction, and
// saved sessions see the request exactly as the user typed it. The request is
// found by its text because compaction can move it; if compaction summarized
// it away, nothing is added.
func withRequestNotes(messages []kajicoderuntime.Message, request, notes string) []kajicoderuntime.Message {
	if notes == "" {
		return messages
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == kajicoderuntime.MessageRoleUser && messages[i].Content == request {
			messages[i].Content += "\n\n" + requestNotesOpen + "\n" + notes + "\n" + requestNotesClose
			break
		}
	}
	return messages
}

func noteTitle(entry harness.Entry) string {
	if title := strings.TrimSpace(entry.Title); title != "" {
		return title
	}
	return entry.ID
}

// recordUsed remembers notes this run relied on, for Reinforce.
func (e *LearningEngine) recordUsed(entries []harness.Entry) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, entry := range entries {
		lesson := usedLesson{store: e.storeForScope(entry.Scope), kind: entry.Kind, id: entry.ID}
		e.used[harness.EntryKey(entry)+"\x00"+lesson.storeDir()] = lesson
	}
}

// Reinforce stamps every note the run relied on, so a note that keeps proving
// relevant ranks ahead on ties and is not pruned as stale. Finish calls it once
// when a run ends, including runs that fail or are cancelled.
func (e *LearningEngine) Reinforce() {
	if e == nil {
		return
	}
	e.mu.Lock()
	used := e.used
	e.used = map[string]usedLesson{}
	e.mu.Unlock()
	now := time.Now()
	for _, lesson := range used {
		if lesson.store == nil || lesson.id == "" {
			continue
		}
		lesson.store.TouchEntry(lesson.kind, lesson.id, now, false)
	}
}
