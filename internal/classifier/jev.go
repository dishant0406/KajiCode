package classifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dishant0406/KajiCode/internal/providers/providerio"
)

// maxResponseBytes caps how much of a classifier response we read. Answers are
// tiny; a hostile or broken endpoint streaming megabytes must not be buffered.
const maxResponseBytes = 1 << 20

// maxRetryAttempts bounds the 429/503 retry loop (SendWithRetry only replays the
// safe 429/503 statuses); the classifier is fail-open, so a couple of retries is
// the ceiling before the caller falls back.
const maxRetryAttempts = 3

// JevClient is the Jev-shaped HTTP backend: it POSTs {model?, state, questions}
// and parses {answers:{...}}. It is the only backend today, but it is deliberately
// tolerant of small wire differences (see parseAnswers) so any router that mirrors
// the TypeSafe Jev contract works without a code change.
type JevClient struct {
	name     string
	endpoint string
	model    string
	timeout  int
	headers  providerio.AuthHeaders
}

// Name identifies the profile for logs and trace lines.
func (c *JevClient) Name() string { return c.name }

// Classify asks the configured endpoint. Every failure path — malformed request,
// transport error, timeout, non-2xx, unparseable body — is wrapped in
// ErrUnavailable so callers can fail open with a single errors.Is check.
func (c *JevClient) Classify(ctx context.Context, req Request) (Result, error) {
	if c == nil || strings.TrimSpace(c.endpoint) == "" {
		return nil, ErrUnavailable
	}
	if len(req.Questions) == 0 {
		return Result{}, nil
	}
	body, err := json.Marshal(c.buildRequest(req))
	if err != nil {
		return nil, fmt.Errorf("%w: encode request: %v", ErrUnavailable, err)
	}

	ctx, cancel := context.WithTimeout(ctx, c.duration())
	defer cancel()

	response, err := providerio.SendWithRetry(ctx, providerio.HTTPClient(nil), http.MethodPost, c.endpoint, body, c.setHeaders, maxRetryAttempts)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer response.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("%w: read response: %v", ErrUnavailable, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: %s", ErrUnavailable, providerio.ClassifiedError(response.StatusCode, strings.TrimSpace(string(payload)), c.headers.APIKey))
	}
	result, err := parseAnswers(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return result, nil
}

func (c *JevClient) duration() time.Duration {
	timeout := c.timeout
	if timeout <= 0 {
		timeout = DefaultTimeoutMS
	}
	return time.Duration(timeout) * time.Millisecond
}

func (c *JevClient) setHeaders(request *http.Request) {
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	providerio.ApplyAuthHeaders(request, c.headers)
}

// wireQuestion is the on-the-wire form of a Question. Criteria is a map for
// noul/choice and an array for score, so it is sent as `any`.
type wireQuestion struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

type wireRequest struct {
	Model     string                  `json:"model,omitempty"`
	State     any                     `json:"state"`
	Questions map[string]wireQuestion `json:"questions"`
}

func (c *JevClient) buildRequest(req Request) wireRequest {
	questions := make(map[string]wireQuestion, len(req.Questions))
	for id, question := range req.Questions {
		questions[id] = wireQuestion{
			Type:         normalizedKind(question.Type),
			Instructions: question.Instructions,
			Criteria:     wireCriteria(question),
		}
	}
	return wireRequest{Model: c.model, State: req.State, Questions: questions}
}

func normalizedKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case KindChoice:
		return KindChoice
	case KindScore:
		return KindScore
	default:
		return KindNoul
	}
}

func wireCriteria(question Question) any {
	switch normalizedKind(question.Type) {
	case KindChoice:
		if len(question.Criteria) == 0 {
			return nil
		}
		return question.Criteria
	case KindScore:
		if len(question.Levels) == 0 {
			return nil
		}
		return question.Levels
	default:
		if len(question.Criteria) == 0 {
			return nil
		}
		return question.Criteria
	}
}

// wireAnswer is the tolerant decode shape: it accepts the canonical Jev field
// names AND the common aliases a non-Jev router may emit (probability/p for a
// noul). All numeric fields are pointers so "absent" is distinguishable from 0.
type wireAnswer struct {
	Type          string             `json:"type"`
	Noul          *float64           `json:"noul"`
	Probability   *float64           `json:"probability"`
	P             *float64           `json:"p"`
	Choice        string             `json:"choice"`
	Score         *float64           `json:"score"`
	Confidence    float64            `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
}

// parseAnswers decodes a classifier body. It accepts both the canonical
// {"answers":{...}} wrapper and a bare top-level answer map, so a router that
// omits the wrapper still works.
func parseAnswers(payload []byte) (Result, error) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("empty response body")
	}
	var wrapper struct {
		Answers map[string]wireAnswer `json:"answers"`
	}
	if err := json.Unmarshal(trimmed, &wrapper); err == nil && len(wrapper.Answers) > 0 {
		return fromWire(wrapper.Answers), nil
	}
	var flat map[string]wireAnswer
	if err := json.Unmarshal(trimmed, &flat); err != nil {
		return nil, fmt.Errorf("decode response: %v", err)
	}
	if len(flat) == 0 {
		return nil, fmt.Errorf("response contained no answers")
	}
	return fromWire(flat), nil
}

func fromWire(raw map[string]wireAnswer) Result {
	result := make(Result, len(raw))
	for id, answer := range raw {
		kind := strings.ToLower(strings.TrimSpace(answer.Type))
		if kind == "" {
			kind = inferKind(answer)
		}
		switch kind {
		case KindChoice:
			result[id] = Answer{Type: KindChoice, Choice: answer.Choice, Confidence: answer.Confidence, Probabilities: answer.Probabilities}
		case KindScore:
			var score float64
			if answer.Score != nil {
				score = *answer.Score
			}
			result[id] = Answer{Type: KindScore, Score: score, Confidence: answer.Confidence, Probabilities: answer.Probabilities}
		default:
			probability := firstFloat(answer.Noul, answer.Probability, answer.P)
			result[id] = Answer{Type: KindNoul, Probability: probability}
		}
	}
	return result
}

// inferKind guesses a question's type when the router omits "type": a choice
// string or a probabilities map means choice; a score field means score; else noul.
func inferKind(answer wireAnswer) string {
	switch {
	case strings.TrimSpace(answer.Choice) != "":
		return KindChoice
	case answer.Score != nil:
		return KindScore
	default:
		return KindNoul
	}
}

func firstFloat(values ...*float64) float64 {
	for _, value := range values {
		if value != nil {
			return *value
		}
	}
	return 0
}
