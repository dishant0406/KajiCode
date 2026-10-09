package harness

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// Search ranks entries by how well they match query and returns at most limit
// of them, best first. It uses BM25, so a word that appears in many entries
// (like the project name) counts for little and a rare word counts for a lot.
// The title is counted twice because it is the entry's own summary.
//
// An entry must match at least two query words (one when the query is a single
// word) and cover at least searchMinCoverage of the query's weight, where rare
// words weigh more and words no note contains are left out. Results scoring
// under half of the best one are dropped. So a note that shares a couple of
// incidental words with a request about something else is not returned. An empty query returns the most recent entries instead.
func Search(entries []Entry, query string, limit int) []Entry {
	if limit <= 0 || len(entries) == 0 {
		return nil
	}
	if strings.TrimSpace(query) == "" {
		recent := append([]Entry(nil), entries...)
		OrderByRecency(recent)
		return firstN(recent, limit)
	}
	queryWords := Keywords(query)
	if len(queryWords) == 0 {
		return nil
	}

	docs := make([][]string, len(entries))
	docFreq := map[string]int{}
	totalLen := 0
	for i, entry := range entries {
		docs[i] = searchWords(entry.Title + " " + entry.Title + " " + entry.Path + " " + entry.Content)
		totalLen += len(docs[i])
		for word := range toSet(docs[i]) {
			docFreq[word]++
		}
	}
	avgLen := float64(totalLen) / float64(len(entries))
	idf := func(word string) float64 {
		df := float64(docFreq[word])
		return math.Log(1 + (float64(len(entries))-df+0.5)/(df+0.5))
	}
	queryWeight := 0.0
	for _, word := range queryWords {
		if docFreq[word] > 0 {
			queryWeight += idf(word)
		}
	}
	minMatched := min(2, len(queryWords))

	type scored struct {
		entry Entry
		score float64
	}
	var results []scored
	for i, entry := range entries {
		score, matched, matchedWeight := bm25(docs[i], queryWords, idf, avgLen)
		if matched >= minMatched && matchedWeight >= searchMinCoverage*queryWeight {
			results = append(results, scored{entry: entry, score: score})
		}
	}
	if len(results) == 0 {
		return nil
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].score != results[j].score {
			return results[i].score > results[j].score
		}
		return compareRecency(results[i].entry, results[j].entry) < 0
	})

	cutoff := results[0].score / 2
	out := make([]Entry, 0, limit)
	for _, result := range results {
		if result.score < cutoff || len(out) == limit {
			break
		}
		out = append(out, result.entry)
	}
	return out
}

// bm25 scores one document against the query words. It also reports how many
// of them the document contains and their combined idf weight. k1=1.2 and
// b=0.75 are the standard BM25 constants.
func bm25(doc []string, queryWords []string, idf func(string) float64, avgLen float64) (score float64, matched int, matchedWeight float64) {
	const k1, b = 1.2, 0.75
	termFreq := map[string]int{}
	for _, word := range doc {
		termFreq[word]++
	}
	for _, word := range queryWords {
		freq := float64(termFreq[word])
		if freq == 0 {
			continue
		}
		matched++
		matchedWeight += idf(word)
		score += idf(word) * freq * (k1 + 1) / (freq + k1*(1-b+b*float64(len(doc))/avgLen))
	}
	return score, matched, matchedWeight
}

// searchWords lowercases text and splits it into letter/digit words, dropping
// single letters and common English filler so they never drive a match. Short
// terms such as "go", "ci", or "ui" are kept. A
// trailing "s" is removed so "tests" matches "test"; it is removed the same way
// from notes and queries, so odd stems like "statu" still match each other.
func searchWords(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	words := fields[:0]
	for _, word := range fields {
		if len([]rune(word)) < 2 || stopWords[word] {
			continue
		}
		if len(word) > 3 && strings.HasSuffix(word, "s") && !strings.HasSuffix(word, "ss") {
			word = strings.TrimSuffix(word, "s")
		}
		words = append(words, word)
	}
	return words
}

// Keywords returns the distinct search words in text, in order: lowercased,
// without filler words or single letters.
func Keywords(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, word := range searchWords(text) {
		if !seen[word] {
			seen[word] = true
			out = append(out, word)
		}
	}
	return out
}

func toSet(words []string) map[string]bool {
	set := make(map[string]bool, len(words))
	for _, word := range words {
		set[word] = true
	}
	return set
}

func firstN(entries []Entry, n int) []Entry {
	if len(entries) > n {
		return entries[:n]
	}
	return entries
}

// searchMinCoverage is the share of a query's weight a result must cover.
const searchMinCoverage = 0.5

// stopWords are filler: common English words and generic request verbs that
// say nothing about which note is relevant ("can you tell me", "already know").
// Single letters are dropped separately.
var stopWords = toSet(strings.Fields(`
am an as at be by do he if in is it me my no of oh ok on or so to up us we
about above after again against all already also always and any are aren
because been before being below between both but can cannot cant could couldn
did didn does doesn doing don done dont down during each either else even ever
every few for from further had hadn has hasn have haven having her here hers
herself him himself his how into isn its itself just let lets like may might
more most must mustn myself needn neither nor not now off once only onto other
ought our ours ourselves out over own really same shall shan she should
shouldn since some still such sure than that the their theirs them themselves
then there these they this those though through too under until upon very was
wasn were weren what whatever when where whether which while who whom whose
why will with within without won would wouldn yet you your yours yourself
yourselves
actually basically please thanks thank okay hey hello yes yeah
answer ask asked tell told show give know knew think want wanted need needed
see look looking get got make made use used using try trying explain describe
thing things something anything everything stuff way one two
`))
