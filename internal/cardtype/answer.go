package cardtype

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Grading entry point for answer-based card types (the service layer only calls GradeAnswer and never branches by card type).

// ErrUngradable means the answer decodes, but the grader considers it ungradable (for example, an option index out of range).
var ErrUngradable = errors.New("cardtype: answer cannot be graded")

// Grading verdicts, corresponding one-to-one with the three verdicts of the front-end feedback.
const (
	VerdictCorrect   = "correct"
	VerdictPartial   = "partial"
	VerdictIncorrect = "incorrect"
)

// GradeOutcome is the result of a single server-side grading.
type GradeOutcome struct {
	// Rating is the mapped 1–4 rating level.
	Rating int
	// Score is the score in [0,1].
	Score float64
	// Verdict is correct / partial / incorrect.
	Verdict string
	// Given is the answer text used for display (see Grader.GivenText).
	Given string
	// Detail is the grading detail, written verbatim to reviews.grade_detail_json.
	Detail map[string]any
}

// BreakdownItem is one entry of the per-item grading result: Answer is an acceptable wording for that item (the Markdown source),
// Correct says whether the learner got that item right.
type BreakdownItem struct {
	Answer  string
	Correct bool
}

// GradeAnswer uses the card type's grader to decode and grade a single answer.
// A decode failure returns that error (callers map it to 400); an answer that decodes but cannot be graded returns ErrUngradable.
func GradeAnswer(g Grader, gc GradeContext, raw json.RawMessage) (GradeOutcome, error) {
	input, err := g.ParseAnswer(gc, raw)
	if err != nil {
		return GradeOutcome{}, err
	}
	rating, detail, ok := g.Grade(input)
	if !ok {
		return GradeOutcome{}, ErrUngradable
	}
	score, _ := detail["score"].(float64)
	m := DefaultGradeMapping()
	if gc.Mapping != nil {
		m = *gc.Mapping
	}
	return GradeOutcome{
		Rating:  rating,
		Score:   score,
		Verdict: m.Verdict(score),
		Given:   g.GivenText(gc.Fields, detail),
		Detail:  detail,
	}, nil
}

// Verdict buckets the score into all correct / partially correct / all wrong by the mapping thresholds, using the same threshold set as RatingFor.
func (m GradeMapping) Verdict(score float64) string {
	n := m.withDefaults()
	switch {
	case score >= n.FullThreshold:
		return VerdictCorrect
	case score > n.NoneThreshold:
		return VerdictPartial
	default:
		return VerdictIncorrect
	}
}

// emptyAnswer reports whether raw means "no answer" (empty or JSON null).
func emptyAnswer(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || string(t) == "null"
}

// answerString decodes a string answer; an empty answer yields an empty string.
func answerString(raw json.RawMessage, kind string) (string, error) {
	if emptyAnswer(raw) {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("%s answer must be a string", kind)
	}
	return s, nil
}

// answerIndex decodes a single 0-based option index: accepts a JSON integer, and also a string whose content is an integer.
func answerIndex(raw json.RawMessage, kind string) (int, error) {
	if emptyAnswer(raw) {
		return 0, fmt.Errorf("%s answer is required", kind)
	}
	var idx int
	if err := json.Unmarshal(raw, &idx); err == nil {
		return idx, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			return n, nil
		}
	}
	return 0, fmt.Errorf("%s answer must be an option index", kind)
}

// optionText returns the text of the idx-th option; returns an empty string when out of range or the field is malformed.
func optionText(fields map[string]any, idx int) string {
	opts, err := stringSliceField(fields, "options")
	if err != nil || idx < 0 || idx >= len(opts) {
		return ""
	}
	return opts[idx]
}
