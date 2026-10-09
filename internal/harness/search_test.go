package harness

import (
	"testing"
	"time"
)

func searchFixture() []Entry {
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	mk := func(title, content string) Entry {
		return NewEntry(KindMemory, title, content, "", "", ScopeProject, "agent", now)
	}
	return []Entry{
		mk("KajiCode vision-gate orphan token", "The TUI drops images for a non-vision model but leaves the [Image #1] attachment token in the composer."),
		mk("KajiCode release flow", "Re-run the gates, then a conventional commit and push the tag."),
		mk("KajiCode compaction judge ordering", "Prune floor, then judge override, then summarizer. KajiCode keeps the newest turns."),
		mk("KajiCode tests on this machine", "Run make test; go test ./... has two known pre-existing failures in internal/tui."),
		mk("Mongo keys with dots", "MongoDB keys containing '..' break $unset; rebuild the map and $set the whole object."),
	}
}

func titles(entries []Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Title
	}
	return out
}

func TestSearchMatchesKeywordQueryNotExactSubstring(t *testing.T) {
	got := Search(searchFixture(), "attachment token [Image #1] composer vision drop", 5)
	if len(got) == 0 || got[0].Title != "KajiCode vision-gate orphan token" {
		t.Fatalf("keyword query should find the vision note first, got %v", titles(got))
	}
}

func TestSearchNaturalQuestion(t *testing.T) {
	got := Search(searchFixture(), "how do I run the tests here?", 5)
	if len(got) == 0 || got[0].Title != "KajiCode tests on this machine" {
		t.Fatalf("question should find the tests note, got %v", titles(got))
	}
}

func TestSearchKeepsTwoLetterTerms(t *testing.T) {
	entries := []Entry{
		NewEntry(KindMemory, "Go test timeouts", "go test needs -timeout for the full suite.", "", "", ScopeProject, "agent", time.Now()),
		NewEntry(KindMemory, "Release flow", "Tag and push.", "", "", ScopeProject, "agent", time.Now()),
	}
	if got := Search(entries, "go", 3); len(got) != 1 || got[0].Title != "Go test timeouts" {
		t.Fatalf("two-letter term should match, got %v", titles(got))
	}
}

func TestSearchMatchesPlurals(t *testing.T) {
	entries := []Entry{NewEntry(KindMemory, "Known failure", "go test has one known failure in internal/tui.", "", "", ScopeProject, "agent", time.Now())}
	if got := Search(entries, "tests failures", 3); len(got) != 1 {
		t.Fatalf("plural query should match singular note, got %v", titles(got))
	}
}

// A note that shares two words with a request mostly about something else must
// not ride along: it covers too little of the request's weight.
func TestSearchIgnoresIncidentalOverlap(t *testing.T) {
	got := Search(searchFixture(), "prune floor then judge override then summarizer, unlike mongo keys", 5)
	if len(got) != 1 || got[0].Title != "KajiCode compaction judge ordering" {
		t.Fatalf("only the compaction note should match, got %v", titles(got))
	}
}

func TestSearchCommonWordDoesNotMatchEverything(t *testing.T) {
	// "kajicode" is in most notes, so it must not pull them all in on its own.
	got := Search(searchFixture(), "kajicode mongo keys", 5)
	if len(got) != 1 || got[0].Title != "Mongo keys with dots" {
		t.Fatalf("rare word should decide the match, got %v", titles(got))
	}
}

func TestSearchChitChatReturnsNothing(t *testing.T) {
	for _, query := range []string{"hello", "what is this about?", "thanks!", "hello provider"} {
		if got := Search(searchFixture(), query, 5); len(got) != 0 {
			t.Fatalf("%q should match nothing, got %v", query, titles(got))
		}
	}
}

func TestSearchSingleWordQuery(t *testing.T) {
	got := Search(searchFixture(), "compaction", 5)
	if len(got) != 1 || got[0].Title != "KajiCode compaction judge ordering" {
		t.Fatalf("single word should match its note, got %v", titles(got))
	}
}

func TestSearchEmptyQueryListsMostRecent(t *testing.T) {
	entries := searchFixture()
	entries[3].LastUsedAt = time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	got := Search(entries, "  ", 2)
	if len(got) != 2 || got[0].Title != "KajiCode tests on this machine" {
		t.Fatalf("empty query should list the most recent first, got %v", titles(got))
	}
}

func TestSearchRespectsLimitAndEmptyInput(t *testing.T) {
	if got := Search(searchFixture(), "kajicode release compaction tests", 1); len(got) > 1 {
		t.Fatalf("limit 1 returned %d entries", len(got))
	}
	if got := Search(nil, "anything", 3); got != nil {
		t.Fatalf("no entries should return nil, got %v", got)
	}
	if got := Search(searchFixture(), "release", 0); got != nil {
		t.Fatalf("zero limit should return nil, got %v", got)
	}
}

func TestSearchFindsNoteForRealisticBugReport(t *testing.T) {
	cases := map[string]string{
		"the image attachment token stays in the composer after switching to a text-only model, fix it": "KajiCode vision-gate orphan token",
		"mongo $unset fails on keys with dots in my migration script, can you fix the update query":     "Mongo keys with dots",
	}
	for request, want := range cases {
		got := Search(searchFixture(), request, 3)
		if len(got) == 0 || got[0].Title != want {
			t.Fatalf("%q should find %q first, got %v", request, want, titles(got))
		}
	}
}
