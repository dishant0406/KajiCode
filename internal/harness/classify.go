package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/dishant0406/KajiCode/internal/classifier"
	"github.com/dishant0406/KajiCode/internal/redaction"
)

const (
	// classifierBatchBytes bounds one classifier request: the request, the
	// notes, and one question per note. Measured on jev-1.13, batches of about
	// 60 notes (about 45 KB) answered in under a second, while 360 notes in one
	// request failed with the router's max_tokens error.
	classifierBatchBytes = 45_000
	// classifierRequestLen caps the user's request inside each batch, so a
	// pasted log cannot push every batch over the limit.
	classifierRequestLen = 2000
	// classifierSearchTimeout bounds the whole lookup; past it the lookup falls
	// back to keyword search so a slow classifier never stalls a run. The batches
	// run in parallel and the slowest one decides the total: over 25 real
	// lookups (200 notes, jev-1.13) most took 0.8–1.4s, 4 of 25 went past 1.5s,
	// and the slowest was 1.74s. 2s keeps fallback rare without a long stall.
	classifierSearchTimeout = 2 * time.Second
	// classifierRelevanceThreshold is the probability at or above which a note
	// counts as relevant. Measured on real requests, relevant notes scored
	// 0.6–0.98 and unrelated ones stayed at or below 0.44.
	classifierRelevanceThreshold = 0.5
	// classifierSummaryLen is how much of each note the classifier sees.
	classifierSummaryLen = 300
)

// Find returns at most limit entries that help with query, best first. When a
// classifier is connected it asks the classifier; when there is none, the query
// has no keywords (empty or only filler words), or the classifier fails or
// misses the deadline, it uses keyword Search instead.
func Find(ctx context.Context, cl classifier.Classifier, entries []Entry, query string, limit int) []Entry {
	if cl == nil || len(Keywords(query)) == 0 || len(entries) == 0 || limit <= 0 {
		return Search(entries, query, limit)
	}
	found, err := rankWithClassifier(ctx, cl, entries, query, limit)
	if err != nil {
		return Search(entries, query, limit)
	}
	return found
}

// rankWithClassifier asks the classifier, for every entry, whether it helps
// with query. Entries are sent in parallel batches. If any batch fails, or does
// not answer every note it was asked about, the whole lookup fails at once,
// because a note in that batch might be the one that matters. It returns the
// entries at or above classifierRelevanceThreshold.
func rankWithClassifier(ctx context.Context, cl classifier.Classifier, entries []Entry, query string, limit int) ([]Entry, error) {
	ctx, cancel := context.WithTimeout(ctx, classifierSearchTimeout)
	defer cancel()

	probability := map[string]float64{}
	var firstErr error
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, batch := range classifierBatches(entries, query) {
		wg.Add(1)
		go func(batch classifier.Request) {
			defer wg.Done()
			result, err := cl.Classify(ctx, batch)
			if err == nil {
				err = checkAnswers(batch, result)
			}
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
					cancel() // the lookup has failed; stop waiting on the other batches
				}
				return
			}
			for id, answer := range result {
				probability[id] = answer.Probability
			}
		}(batch)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}

	type ranked struct {
		entry       Entry
		probability float64
	}
	var relevant []ranked
	for i, entry := range entries {
		if p := probability[noteID(i)]; p >= classifierRelevanceThreshold {
			relevant = append(relevant, ranked{entry: entry, probability: p})
		}
	}
	sort.SliceStable(relevant, func(i, j int) bool {
		if relevant[i].probability != relevant[j].probability {
			return relevant[i].probability > relevant[j].probability
		}
		return compareRecency(relevant[i].entry, relevant[j].entry) < 0
	})
	out := make([]Entry, 0, min(limit, len(relevant)))
	for _, r := range relevant[:min(limit, len(relevant))] {
		out = append(out, r.entry)
	}
	return out, nil
}

// checkAnswers fails a batch unless it got a yes/no answer for every note it
// asked about. A missing or malformed answer is a classifier fault, not a "no".
func checkAnswers(batch classifier.Request, result classifier.Result) error {
	for id := range batch.Questions {
		if answer, ok := result[id]; !ok || answer.Type != classifier.KindNoul {
			return fmt.Errorf("%w: no answer for note %s", classifier.ErrUnavailable, id)
		}
	}
	return nil
}

// classifierBatches packs entries into requests of at most classifierBatchBytes,
// one yes/no question per entry. Text is redacted before it is shortened, so a
// secret is never cut into a fragment the redactor no longer recognizes.
func classifierBatches(entries []Entry, query string) []classifier.Request {
	request := Snippet(redact(query), classifierRequestLen)
	base := jsonSize(request) + 64
	var batches []classifier.Request
	var notes []map[string]string
	questions := map[string]classifier.Question{}
	size := base
	flush := func() {
		if len(notes) == 0 {
			return
		}
		batches = append(batches, classifier.Request{
			State:     map[string]any{"request": request, "notes": notes},
			Questions: questions,
		})
		notes = nil
		questions = map[string]classifier.Question{}
		size = base
	}
	for i, entry := range entries {
		id := noteID(i)
		note := map[string]string{
			"id":      id,
			"title":   Snippet(redact(entry.Title), classifierSummaryLen),
			"summary": Snippet(redact(entry.Content), classifierSummaryLen),
		}
		question := classifier.Noul(
			"For note "+id+": would this saved note help the agent answer or carry out the request?",
			"directly relevant to the request",
			"unrelated to the request",
		)
		noteSize := jsonSize(note) + jsonSize(question) + len(id) + 8
		if len(notes) > 0 && size+noteSize > classifierBatchBytes {
			flush()
		}
		notes = append(notes, note)
		questions[id] = question
		size += noteSize
	}
	flush()
	return batches
}

// jsonSize is how many bytes value takes as JSON, so batches are packed by
// their real size.
func jsonSize(value any) int {
	data, _ := json.Marshal(value)
	return len(data)
}

func noteID(index int) string {
	return fmt.Sprintf("n%d", index)
}

func redact(text string) string {
	return redaction.RedactString(text, redaction.Options{})
}
