package kajicoderuntime

import (
	"strings"
	"unicode"
)

const (
	// repetitionWindow is how many recent segments are compared. If they hold at
	// most repetitionMaxDistinct different values the model is cycling through a
	// few phrases ("Go. Writing. OK. Go. Writing. OK.").
	repetitionWindow      = 16
	repetitionMaxDistinct = 4
	// repetitionMaxLen keeps the window rule to short phrases; real prose
	// sentences are longer and never repeat this tightly.
	repetitionMaxLen = 60
	// repetitionRun is how many identical segments in a row count as stuck.
	repetitionRun = 6
)

// repetitionDetector spots a model stuck repeating itself in streamed prose or
// reasoning. It splits the stream into segments (lines and sentences) and reports
// when the recent ones are the same few phrases. The zero value is ready to use.
type repetitionDetector struct {
	current   strings.Builder
	sentence  bool     // the last character ended a sentence, so whitespace closes it
	recent    []string // normalized segments, newest last
	recentRaw []string // the same segments as streamed, for diagnostics
}

// feed adds a streamed delta and reports whether the stream is now repeating.
func (detector *repetitionDetector) feed(delta string) bool {
	for _, char := range delta {
		switch {
		case char == '\n' || (detector.sentence && unicode.IsSpace(char)):
			detector.sentence = false
			if detector.endSegment() {
				return true
			}
		default:
			detector.current.WriteRune(char)
			detector.sentence = char == '.' || char == '!' || char == '?'
		}
	}
	return false
}

func (detector *repetitionDetector) endSegment() bool {
	raw := strings.TrimSpace(detector.current.String())
	detector.current.Reset()
	segment := normalizeSegment(raw)
	if segment == "" {
		return false
	}
	detector.recent = append(detector.recent, segment)
	detector.recentRaw = append(detector.recentRaw, raw)
	if len(detector.recent) > repetitionWindow {
		detector.recent = detector.recent[1:]
		detector.recentRaw = detector.recentRaw[1:]
	}
	return detector.stuck()
}

func (detector *repetitionDetector) stuck() bool {
	recent := detector.recent
	if len(recent) >= repetitionRun {
		tail := recent[len(recent)-repetitionRun:]
		same := true
		for _, segment := range tail {
			if segment != tail[0] {
				same = false
				break
			}
		}
		if same {
			return true
		}
	}
	if len(recent) < repetitionWindow {
		return false
	}
	distinct := map[string]bool{}
	for _, segment := range recent {
		if len(segment) > repetitionMaxLen {
			return false
		}
		distinct[segment] = true
	}
	return len(distinct) <= repetitionMaxDistinct
}

// normalizeSegment lowercases text and keeps only letters, digits and single
// spaces, so "Go." and "go" compare equal. A segment with no letter or digit
// (a rule line, a bullet) returns "" and is ignored.
func normalizeSegment(text string) string {
	var out strings.Builder
	pendingSpace := false
	for _, char := range strings.ToLower(text) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			if pendingSpace && out.Len() > 0 {
				out.WriteByte(' ')
			}
			pendingSpace = false
			out.WriteRune(char)
		} else if unicode.IsSpace(char) {
			pendingSpace = true
		}
	}
	return out.String()
}

// excerpt returns the most recent segments of the stream, for diagnostics.
func (detector *repetitionDetector) excerpt() string {
	return strings.Join(detector.recentRaw, "\n")
}
