package agent

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// Compaction artifacts.
//
// The judge decides which stale tool bodies still matter, but that decision is
// made ABOVE the summarizer and the summarizer compresses it away: a body the
// judge restores lands in the summarizer's input, and the context the model
// reads next turn is only the summary. Measured on real sessions, 1.5% of the
// paths the judge rescued survived into a summary.
//
// This block is the fix. The judge pass already knows every stale tool result
// and what it acted on, so it reports that as a bounded list of artifacts —
// the tool, the target (path/pattern/command/query/url), and a head slice of the
// body — which is rendered verbatim into the injected summary. A path therefore
// reaches the model whether or not the prose summary happens to name it, and the
// model can re-read the file instead of preferring to have never lost it.
//
// The block is rendered BEFORE the preserved-state JSON block: the state block
// is located by the LAST occurrence of its label, so keeping the single-line
// JSON last means a carried tool body containing that label cannot shadow it.
//
// The list is deterministic given the judged history: no extra classifier call
// and no model in the loop. Order and caps are fixed, so the same judged input
// always renders the same block (it depends only on classifier probabilities,
// which are fixed for a fixed input).

const (
	// maxDurableArtifacts caps how many artifacts the block carries, newest
	// first. The newest results are the ones the model is most likely still
	// working from.
	maxDurableArtifacts = 20
	// maxArtifactHeadBytes caps each artifact's body head, so one huge result
	// cannot dominate the block. The target and the opening lines carry the
	// fact that mattered; the rest is re-readable.
	maxArtifactHeadBytes = 300
	// maxArtifactTargetBytes caps a target, and it is always collapsed to one
	// line: a shell command (heredocs especially) can be multi-line and would
	// otherwise break the block's line-per-artifact format.
	maxArtifactTargetBytes = 200
	// maxArtifactsBytes caps the whole rendered block, keeping the carry a
	// fixed small fraction of the context compaction just removed.
	maxArtifactsBytes = 6 << 10
)

// artifactsLabel heads the carried-artifacts block. It is stable so the
// summarizer prompt can reference it and tests can assert on it.
const artifactsLabel = "## Carried artifacts (from tool results removed by compaction; re-read a file for its full body)"

// durableArtifact is one stale tool result worth keeping reachable after
// compaction. index is the source message index, so CompactMessages can keep
// only the artifacts that actually fall in the elided middle. path is true when
// the target names a file, which is the only kind the model can re-read.
type durableArtifact struct {
	index  int
	tool   string
	target string
	body   string
	path   bool
}

// artifactsWithin narrows the reported artifacts to those whose source message
// falls in the elided middle [systemEnd, boundary). An artifact whose message is
// preserved verbatim in the tail is already in context, so carrying it again
// would only duplicate it.
func artifactsWithin(artifacts []durableArtifact, systemEnd, boundary int) []durableArtifact {
	kept := artifacts[:0:0]
	for _, artifact := range artifacts {
		if artifact.index >= systemEnd && artifact.index < boundary {
			kept = append(kept, artifact)
		}
	}
	return kept
}

// artifactTarget names what a tool acted on, pulled from the tool call's JSON
// arguments: a file path, a search pattern, a command, a query, or a URL. It
// returns "" when the call carries no extractable target, which is the signal
// that the artifact would add nothing the model can act on. The result is
// collapsed to a single line and byte-capped, because a shell command can be
// multi-line.
func artifactTarget(arguments string) string {
	if path := editPathFromArguments(arguments); path != "" {
		return oneLine(path, maxArtifactTargetBytes)
	}
	var parsed struct {
		Pattern string `json:"pattern"`
		Command string `json:"command"`
		Query   string `json:"query"`
		URL     string `json:"url"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(arguments)), &parsed); err != nil {
		return ""
	}
	for _, candidate := range []string{parsed.Pattern, parsed.Command, parsed.Query, parsed.URL} {
		if value := strings.TrimSpace(candidate); value != "" {
			return oneLine(value, maxArtifactTargetBytes)
		}
	}
	return ""
}

// artifactIsPath reports whether a target names a file the model can re-read
// rather than a command or query. It is a heuristic for ranking only: artifacts
// whose target is a path are what a stale read_file/grep result was about, and
// they are the ones the model acts on next.
func artifactIsPath(target string) bool {
	first := strings.Fields(target)
	if len(first) == 0 {
		return false
	}
	word := first[0]
	if !strings.ContainsAny(word, "./_") || strings.ContainsAny(word, `"'`) {
		return false
	}
	ext := pathExt(word)
	return ext != "" && len(ext) <= 5
}

// pathExt returns the extension (with dot) of the last path-like segment, or "".
func pathExt(word string) string {
	idx := strings.LastIndexByte(word, '.')
	slash := strings.LastIndexByte(word, '/')
	if idx <= 0 || idx < slash || idx == len(word)-1 {
		return ""
	}
	return word[idx:]
}

// oneLine collapses whitespace runs to a single space and byte-caps the result
// on a rune boundary.
func oneLine(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if limit <= 0 || len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut] + "…"
}

// artifactHead returns a bounded, single-line head of a tool body so it renders
// as one list item.
func artifactHead(body string) string {
	return oneLine(body, maxArtifactHeadBytes)
}

// formatArtifacts renders the carried-artifacts block, or "" when there is
// nothing to carry. Items are emitted in the order given until maxArtifactsBytes
// is reached, so the block is bounded on an arbitrarily large input.
func formatArtifacts(artifacts []durableArtifact) string {
	if len(artifacts) == 0 {
		return ""
	}
	lines := []string{artifactsLabel}
	total := len(artifactsLabel)
	for _, artifact := range artifacts {
		if len(lines) > maxDurableArtifacts {
			break
		}
		line := oneLine("- "+artifact.tool+" "+artifact.target+": "+artifact.body, maxArtifactsBytes)
		if total+len(line)+1 > maxArtifactsBytes {
			break
		}
		lines = append(lines, line)
		total += len(line) + 1
	}
	if len(lines) == 1 {
		return ""
	}
	return strings.Join(lines, "\n")
}

// newDurableArtifact builds one artifact from a stale tool result, or reports
// false when the item carries no target worth naming.
func newDurableArtifact(candidate judgeCandidate) (durableArtifact, bool) {
	target := artifactTarget(candidate.argument)
	if target == "" {
		return durableArtifact{}, false
	}
	return durableArtifact{
		index:  candidate.index,
		tool:   candidate.tool,
		target: target,
		body:   artifactHead(candidate.body),
		path:   artifactIsPath(target),
	}, true
}
