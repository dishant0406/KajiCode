// Package classifier is KajiCode's pluggable "fast classifier" capability: a
// small, provider-agnostic seam that asks yes/no, single-choice, and rubric
// questions about some state and gets back typed answers.
//
// It is deliberately not a model provider. A classifier does not generate text;
// it scores a decision, and the features that consume it (compaction, tool-result
// filtering, the completion gate) are all fail-open: when the classifier is off,
// unreachable, or slow, the caller proceeds exactly as it did before. The only
// backend today speaks the Jev-shaped wire contract ({state, questions} →
// {answers}), which the hosted TypeSafe Jev router and any compatible router
// serve. The narrow interface keeps a future local backend possible without
// touching a single consumer.
package classifier

import (
	"context"
	"errors"
)

// ErrUnavailable reports that the classifier could not answer — it is disabled,
// unreachable, timed out, rate-limited, or returned a body we could not parse.
// Every caller treats it as "the classifier is off" and proceeds unchanged, so a
// classifier outage can never block the agent. All client errors wrap it, so
// errors.Is(err, ErrUnavailable) is the single check a caller needs.
var ErrUnavailable = errors.New("classifier unavailable")

// Answer kinds, which are also the wire "type" of a question and its answer.
const (
	// KindNoul is a yes/no question answered with a probability in [0,1].
	KindNoul = "noul"
	// KindChoice picks one option and returns the full distribution.
	KindChoice = "choice"
	// KindScore rates along an ordered rubric and returns the distribution.
	KindScore = "score"
)

// Question is one typed ask. A single request carries many questions and they are
// answered in parallel, so a caller pays one round trip per decision point no
// matter how many questions it batches.
type Question struct {
	Type         string
	Instructions string
	// Criteria pins the meaning of an answer.
	//   noul:   {"true": "<what yes means>", "false": "<what no means>"}
	//   choice: {"<option>": "<what that option means>"}
	Criteria map[string]string
	// Levels is the ordered rubric (low → high) for a score question.
	Levels []string
}

// Answer is the classifier's response to one Question, keyed by its id in Result.
type Answer struct {
	Type          string
	Probability   float64 // noul
	Choice        string  // choice
	Score         float64 // score
	Confidence    float64 // choice/score
	Probabilities map[string]float64
}

// Result maps question id → Answer. A missing id means that question was not
// answered (the caller supplies its own default); it is never a hard failure.
type Result map[string]Answer

// Request is the neutral, provider-independent ask. State is the text or object
// the questions are judged against (string, map, or slice); Questions is the batch.
type Request struct {
	State     any
	Questions map[string]Question
}

// Classifier is the seam every classifier-powered feature depends on. A nil
// Classifier means "the feature is off"; the agent loop is byte-identical when it
// is nil.
type Classifier interface {
	Classify(ctx context.Context, req Request) (Result, error)
	Name() string
}

// Noul builds a yes/no question. trueHint/falseHint are optional but strongly
// recommended: they pin the meaning of the probability and are the difference
// between a usable and a noisy signal on a small classifier.
func Noul(instructions, trueHint, falseHint string) Question {
	var criteria map[string]string
	if trueHint != "" || falseHint != "" {
		criteria = map[string]string{}
		if trueHint != "" {
			criteria["true"] = trueHint
		}
		if falseHint != "" {
			criteria["false"] = falseHint
		}
	}
	return Question{Type: KindNoul, Instructions: instructions, Criteria: criteria}
}

// Choice builds a single-select question over the given options (option → hint).
func Choice(instructions string, options map[string]string) Question {
	return Question{Type: KindChoice, Instructions: instructions, Criteria: options}
}

// Score builds an ordered rubric rating question.
func Score(instructions string, levels []string) Question {
	return Question{Type: KindScore, Instructions: instructions, Levels: levels}
}

// Probability returns the noul probability for id, or fallback when the answer is
// missing or of the wrong kind. It keeps callers from reaching into the map shape.
func (r Result) Probability(id string, fallback float64) float64 {
	answer, ok := r[id]
	if !ok || answer.Type != KindNoul {
		return fallback
	}
	return answer.Probability
}
