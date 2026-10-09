package harness

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dishant0406/KajiCode/internal/classifier"
)

// fakeClassifier answers each note question with the probability set for the
// note's title, and records what it was asked.
type fakeClassifier struct {
	byTitle map[string]float64
	err     error
	delay   time.Duration
	// omitTitle leaves that note unanswered; answerType overrides the answer
	// kind; failFast makes the first call fail at once while the rest are slow.
	omitTitle  string
	answerType string
	failFast   bool

	mu        sync.Mutex
	requests  []classifier.Request
	inFlight  int
	maxFlight int
}

func (f *fakeClassifier) Name() string { return "fake" }

func (f *fakeClassifier) Classify(ctx context.Context, req classifier.Request) (classifier.Result, error) {
	f.mu.Lock()
	first := len(f.requests) == 0
	f.requests = append(f.requests, req)
	f.inFlight++
	f.maxFlight = max(f.maxFlight, f.inFlight)
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.inFlight--
		f.mu.Unlock()
	}()
	if f.failFast && first {
		return nil, classifier.ErrUnavailable
	}
	select {
	case <-time.After(f.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if f.err != nil {
		return nil, f.err
	}
	kind := classifier.KindNoul
	if f.answerType != "" {
		kind = f.answerType
	}
	result := classifier.Result{}
	for _, note := range req.State.(map[string]any)["notes"].([]map[string]string) {
		if note["title"] == f.omitTitle {
			continue
		}
		result[note["id"]] = classifier.Answer{Type: kind, Probability: f.byTitle[note["title"]]}
	}
	return result, nil
}

func TestFindUsesClassifierInsteadOfKeywords(t *testing.T) {
	// The keyword search would pick the release note for this request; the
	// classifier knows the vision note is what matters.
	fake := &fakeClassifier{byTitle: map[string]float64{
		"KajiCode vision-gate orphan token": 0.92,
		"Mongo keys with dots":              0.61,
		"KajiCode release flow":             0.30,
	}}
	got := Find(context.Background(), fake, searchFixture(), "push the release tag", 5)
	if len(got) != 2 || got[0].Title != "KajiCode vision-gate orphan token" || got[1].Title != "Mongo keys with dots" {
		t.Fatalf("want classifier picks above 0.5, best first; got %v", titles(got))
	}
	if keyword := Search(searchFixture(), "push the release tag", 5); len(keyword) == 0 || keyword[0].Title != "KajiCode release flow" {
		t.Fatalf("fixture no longer separates keyword and classifier picks: %v", titles(keyword))
	}
}

func TestFindReturnsNothingWhenClassifierFindsNothingRelevant(t *testing.T) {
	fake := &fakeClassifier{byTitle: map[string]float64{}}
	if got := Find(context.Background(), fake, searchFixture(), "compaction judge ordering", 5); len(got) != 0 {
		t.Fatalf("classifier answered no; keyword search must not be used, got %v", titles(got))
	}
}

func TestFindRespectsLimit(t *testing.T) {
	fake := &fakeClassifier{byTitle: map[string]float64{}}
	for _, entry := range searchFixture() {
		fake.byTitle[entry.Title] = 0.9
	}
	if got := Find(context.Background(), fake, searchFixture(), "deploy release notes", 2); len(got) != 2 {
		t.Fatalf("limit 2 returned %d", len(got))
	}
}

func TestFindFallsBackToKeywordsWhenClassifierFails(t *testing.T) {
	want := titles(Search(searchFixture(), "compaction judge ordering", 5))
	for name, fake := range map[string]*fakeClassifier{
		"error": {err: classifier.ErrUnavailable},
		"slow":  {delay: classifierSearchTimeout + time.Second},
	} {
		start := time.Now()
		got := titles(Find(context.Background(), fake, searchFixture(), "compaction judge ordering", 5))
		if strings.Join(got, "|") != strings.Join(want, "|") || len(got) == 0 {
			t.Fatalf("%s: want keyword fallback %v, got %v", name, want, got)
		}
		if elapsed := time.Since(start); elapsed > classifierSearchTimeout+500*time.Millisecond {
			t.Fatalf("%s: lookup took %v, deadline is %v", name, elapsed, classifierSearchTimeout)
		}
	}
}

// A reply that skips a note, or answers with the wrong kind, is a classifier
// fault: it must fall back, not be read as "nothing is relevant".
func TestFindFallsBackOnIncompleteAnswers(t *testing.T) {
	want := strings.Join(titles(Search(searchFixture(), "compaction judge ordering", 5)), "|")
	for name, fake := range map[string]*fakeClassifier{
		"missing note": {byTitle: map[string]float64{}, omitTitle: "Mongo keys with dots"},
		"wrong kind":   {byTitle: map[string]float64{}, answerType: classifier.KindChoice},
	} {
		if got := strings.Join(titles(Find(context.Background(), fake, searchFixture(), "compaction judge ordering", 5)), "|"); got != want || got == "" {
			t.Fatalf("%s: want keyword fallback %q, got %q", name, want, got)
		}
	}
}

// Once one batch fails the lookup has failed; it must not wait for the rest.
func TestFindStopsWaitingAfterFirstFailure(t *testing.T) {
	var entries []Entry
	for i := 0; i < 200; i++ {
		entries = append(entries, NewEntry(KindMemory, "note "+noteID(i), strings.Repeat("detail ", 80), "", "", ScopeProject, "agent", time.Now()))
	}
	fake := &fakeClassifier{byTitle: map[string]float64{}, delay: time.Hour, failFast: true}
	start := time.Now()
	Find(context.Background(), fake, entries, "some request", 3)
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("lookup waited %v after a batch had already failed", elapsed)
	}
}

func TestFindSkipsClassifierForEmptyQueryOrNoClassifier(t *testing.T) {
	fake := &fakeClassifier{byTitle: map[string]float64{}}
	if got := Find(context.Background(), fake, searchFixture(), "  ", 2); len(got) != 2 {
		t.Fatalf("empty query should list recent notes, got %v", titles(got))
	}
	if len(fake.requests) != 0 {
		t.Fatal("empty query must not call the classifier")
	}
	if got := Find(context.Background(), nil, searchFixture(), "compaction judge", 5); len(got) == 0 {
		t.Fatal("no classifier should use keyword search")
	}
}

func TestFindSendsParallelBoundedBatches(t *testing.T) {
	var entries []Entry
	for i := 0; i < 300; i++ {
		entries = append(entries, NewEntry(KindMemory, "note "+noteID(i), strings.Repeat("detail ", 80), "", "", ScopeProject, "agent", time.Now()))
	}
	// A pasted log as the request must be capped, not copied whole into every batch.
	request := strings.Repeat("panic: runtime error in handler ", 2000)
	fake := &fakeClassifier{byTitle: map[string]float64{}, delay: 50 * time.Millisecond}
	Find(context.Background(), fake, entries, request, 3)

	if len(fake.requests) < 2 {
		t.Fatalf("300 notes should need several batches, got %d", len(fake.requests))
	}
	asked := 0
	for _, req := range fake.requests {
		body, _ := json.Marshal(map[string]any{"state": req.State, "questions": req.Questions})
		if len(body) > classifierBatchBytes {
			t.Fatalf("request is %d bytes with questions, budget %d", len(body), classifierBatchBytes)
		}
		if got := len([]rune(req.State.(map[string]any)["request"].(string))); got > classifierRequestLen {
			t.Fatalf("request text is %d characters, cap %d", got, classifierRequestLen)
		}
		asked += len(req.Questions)
	}
	if asked != len(entries) {
		t.Fatalf("asked about %d notes, want %d", asked, len(entries))
	}
	if fake.maxFlight < 2 {
		t.Fatalf("batches ran one at a time (max in flight %d)", fake.maxFlight)
	}
}

func TestFindRedactsNoteTextBeforeSending(t *testing.T) {
	secret := "ghp_" + strings.Repeat("a", 36)
	// The secret straddles the 300-character cut, so redacting after cutting
	// would leave a fragment the redactor no longer recognizes.
	content := strings.Repeat("x", classifierSummaryLen-20) + " " + secret + " tail"
	entries := []Entry{NewEntry(KindMemory, "Token note", content, "", "", ScopeProject, "agent", time.Now())}
	fake := &fakeClassifier{byTitle: map[string]float64{}}
	Find(context.Background(), fake, entries, "which api token", 3)
	state, _ := json.Marshal(fake.requests[0].State)
	if strings.Contains(string(state), "ghp_aaaa") {
		t.Fatalf("secret sent to the classifier: %s", state)
	}
}
