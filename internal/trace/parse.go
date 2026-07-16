package trace

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"time"
)

// ReadNDJSON parses an NDJSON trace emitted by WriteNDJSON back into a TurnTrace.
// It is the inverse of WriteNDJSON and is used by the benchmark harness to turn a
// captured trace file into structured per-span stats. Lines that are not valid
// JSON or lack a "type" field are skipped (the emitter is the only writer, so in
// practice every line is valid; the skip keeps a stray blank line from fataling a
// benchmark run). The first "trace" line sets the run/timing fields; "span" and
// "counter" lines populate Spans and Counters.
func ReadNDJSON(r io.Reader) (*TurnTrace, error) {
	if r == nil {
		return nil, nil
	}
	t := &TurnTrace{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			continue
		}
		typ, _ := obj["type"].(string)
		switch typ {
		case "trace":
			t.RunID, _ = obj["run_id"].(string)
			t.SessionID, _ = obj["session_id"].(string)
			t.Profile, _ = obj["profile"].(string)
			t.StartedAt = parseTime(obj["started_at"])
			t.FirstVisibleEventAt = parseTime(obj["first_visible_at"])
			t.FirstUsefulActionAt = parseTime(obj["first_useful_at"])
			t.FirstTokenAt = parseTime(obj["first_token_at"])
			t.CompletedAt = parseTime(obj["completed_at"])
		case "span":
			name, _ := obj["name"].(string)
			t.Spans = append(t.Spans, Span{Name: name, Duration: parseDurationMs(obj["duration_ms"])})
		case "counter":
			name, _ := obj["name"].(string)
			t.Counters = append(t.Counters, Counter{Name: name, Value: parseInt64(obj["value"])})
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return t, nil
}

func parseTime(v any) time.Time {
	s, _ := v.(string)
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func parseDurationMs(v any) time.Duration {
	f := toFloat64(v)
	return time.Duration(f * float64(time.Millisecond))
}

func parseInt64(v any) int64 {
	return int64(toFloat64(v))
}

func toFloat64(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case int:
		return float64(n)
	default:
		return 0
	}
}
