package cardtype

import (
	"encoding/json"
	"fmt"
	"strings"
)

// This file implements the two choice cards:
//   choice_single: question / options[] (≥2, deduplicated) / answer (0-based index)
//   choice_multi : question / options[] (≥2, deduplicated) / answers[] (0-based index, ≥1, deduplicated)
// JSON Schema cannot express the constraints on options and answer indices, so Validate enforces them here and names the field.

// choiceSingleType is the single-choice card.
type choiceSingleType struct{}

// Label returns the language pack key.
func (choiceSingleType) Label() string { return "cardtype.choice_single" }

// Describe self-describes: question on the front, options on the back; the answer control is single-select (takes an options index).
func (choiceSingleType) Describe() Description {
	return Description{
		Kind:          "choice_single",
		LabelKey:      "cardtype.choice_single",
		AnswerControl: AnswerSingle,
		FrontField:    "question",
		BackField:     "options",
		PromptField:   "question",
		OptionsField:  "options",
		Fields: fieldsWithCommon(
			FieldSpec{Key: "question", Control: ControlTextarea, Required: true},
			FieldSpec{Key: "options", Control: ControlLines, Required: true},
			FieldSpec{Key: "answer", Control: ControlIndex, Required: true},
		),
		Example: map[string]any{
			"question": "Which planet is closest to the Sun?",
			"options":  []any{"Venus", "Mercury", "Mars"},
			"answer":   1,
		},
	}
}

// Validate checks question / options / answer and ensures the index falls within the options range.
func (choiceSingleType) Validate(fields map[string]any) error {
	if _, err := stringField(fields, "question"); err != nil {
		return fmt.Errorf("choice_single: %w", err)
	}
	options, err := stringSliceField(fields, "options")
	if err != nil {
		return fmt.Errorf("choice_single: %w", err)
	}
	if len(options) < 2 {
		return fmt.Errorf("choice_single: field %q needs at least 2 options", "options")
	}
	if !uniqueStrings(options) {
		return fmt.Errorf("choice_single: field %q must not contain duplicates", "options")
	}
	answer, err := intField(fields, "answer")
	if err != nil {
		return fmt.Errorf("choice_single: %w", err)
	}
	if answer >= len(options) {
		return fmt.Errorf("choice_single: field %q index %d is out of range for %d options", "answer", answer, len(options))
	}
	if err := validateCommonOptional(fields); err != nil {
		return fmt.Errorf("choice_single: %w", err)
	}
	return nil
}

// Cards produces one forward card.
func (choiceSingleType) Cards(note Note) []Card {
	return []Card{{Template: "forward", Ordinal: 0, Fields: note.Fields}}
}

// Render gives the question and all options on the front; the back gives only the text of the correct option.
func (choiceSingleType) Render(card Card, side Side) (RenderResult, error) {
	if card.Template != "forward" {
		return RenderResult{}, errUnknownTemplate(card.Template)
	}
	question, options, answer, err := choiceSingleParts(card.Fields)
	if err != nil {
		return RenderResult{}, err
	}
	if side == SideFront {
		return RenderResult{Body: question, Extra: options}, nil
	}
	return RenderResult{Body: options[answer]}, nil
}

// ChoiceSingleInput is the input of choiceSingleType.Grade; Selected is the 0-based option index.
type ChoiceSingleInput struct {
	GradeContext
	Selected int
}

// ParseAnswer decodes the answer: a single 0-based option index (an integer, or a string whose content is an integer); no answer returns an error.
func (choiceSingleType) ParseAnswer(gc GradeContext, raw json.RawMessage) (any, error) {
	idx, err := answerIndex(raw, "choice_single")
	if err != nil {
		return nil, err
	}
	return ChoiceSingleInput{GradeContext: gc, Selected: idx}, nil
}

// GivenText returns the text of the selected option.
func (choiceSingleType) GivenText(fields map[string]any, detail map[string]any) string {
	idx, ok := detail["selected"].(int)
	if !ok {
		return ""
	}
	return optionText(fields, idx)
}

// Grade grades: selecting the correct option scores 1, selecting another valid option scores 0; an out-of-range index counts as ungradable.
func (choiceSingleType) Grade(input any) (int, map[string]any, bool) {
	in, ok := input.(ChoiceSingleInput)
	if !ok {
		return 0, nil, false
	}
	_, options, answer, err := choiceSingleParts(in.Fields)
	if err != nil {
		return 0, nil, false
	}
	if in.Selected < 0 || in.Selected >= len(options) {
		return 0, nil, false
	}
	detail := map[string]any{"answer": answer, "selected": in.Selected}
	if in.Selected == answer {
		return in.rating(1, detail)
	}
	return in.rating(0, detail)
}

// PromptContexter supplies the question and reference answer for future LLM scoring.
func (choiceSingleType) PromptContext(note Note) PromptContext {
	question, options, answer, err := choiceSingleParts(note.Fields)
	if err != nil {
		return PromptContext{}
	}
	return PromptContext{Question: question + "\n" + strings.Join(options, "\n"), Reference: options[answer]}
}

// choiceSingleParts reads and validates the three elements of a single-choice card.
func choiceSingleParts(fields map[string]any) (string, []string, int, error) {
	question, err := stringField(fields, "question")
	if err != nil {
		return "", nil, 0, fmt.Errorf("choice_single: %w", err)
	}
	options, err := stringSliceField(fields, "options")
	if err != nil {
		return "", nil, 0, fmt.Errorf("choice_single: %w", err)
	}
	answer, err := intField(fields, "answer")
	if err != nil {
		return "", nil, 0, fmt.Errorf("choice_single: %w", err)
	}
	if answer >= len(options) {
		return "", nil, 0, fmt.Errorf("choice_single: field %q index %d is out of range for %d options", "answer", answer, len(options))
	}
	return question, options, answer, nil
}

// choiceMultiType is the multiple-choice card.
type choiceMultiType struct{}

// Label returns the language pack key.
func (choiceMultiType) Label() string { return "cardtype.choice_multi" }

// Describe self-describes: question on the front, options on the back; the answer control is multi-select (takes a set of options indices).
func (choiceMultiType) Describe() Description {
	return Description{
		Kind:          "choice_multi",
		LabelKey:      "cardtype.choice_multi",
		AnswerControl: AnswerMulti,
		FrontField:    "question",
		BackField:     "options",
		PromptField:   "question",
		OptionsField:  "options",
		Fields: fieldsWithCommon(
			FieldSpec{Key: "question", Control: ControlTextarea, Required: true},
			FieldSpec{Key: "options", Control: ControlLines, Required: true},
			FieldSpec{Key: "answers", Control: ControlIndexes, Required: true},
		),
		Example: map[string]any{
			"question": "Which of these numbers are prime?",
			"options":  []any{"2", "4", "5", "9"},
			"answers":  []any{0, 2},
		},
	}
}

// Validate checks question / options / answers and ensures the indices are in range with no duplicates.
func (choiceMultiType) Validate(fields map[string]any) error {
	if _, err := stringField(fields, "question"); err != nil {
		return fmt.Errorf("choice_multi: %w", err)
	}
	options, err := stringSliceField(fields, "options")
	if err != nil {
		return fmt.Errorf("choice_multi: %w", err)
	}
	if len(options) < 2 {
		return fmt.Errorf("choice_multi: field %q needs at least 2 options", "options")
	}
	if !uniqueStrings(options) {
		return fmt.Errorf("choice_multi: field %q must not contain duplicates", "options")
	}
	answers, err := intSliceField(fields, "answers")
	if err != nil {
		return fmt.Errorf("choice_multi: %w", err)
	}
	if !uniqueInts(answers) {
		return fmt.Errorf("choice_multi: field %q must not contain duplicates", "answers")
	}
	for _, idx := range answers {
		if idx >= len(options) {
			return fmt.Errorf("choice_multi: field %q index %d is out of range for %d options", "answers", idx, len(options))
		}
	}
	if err := validateCommonOptional(fields); err != nil {
		return fmt.Errorf("choice_multi: %w", err)
	}
	return nil
}

// Cards produces one forward card.
func (choiceMultiType) Cards(note Note) []Card {
	return []Card{{Template: "forward", Ordinal: 0, Fields: note.Fields}}
}

// Render gives the question and all options on the front; the back lists all correct options in display order.
func (choiceMultiType) Render(card Card, side Side) (RenderResult, error) {
	if card.Template != "forward" {
		return RenderResult{}, errUnknownTemplate(card.Template)
	}
	question, options, answers, err := choiceMultiParts(card.Fields)
	if err != nil {
		return RenderResult{}, err
	}
	if side == SideFront {
		return RenderResult{Body: question, Extra: options}, nil
	}
	correct := make([]string, 0, len(answers))
	if isPermutation(card.OptionOrder, len(options)) {
		// The back follows the display order the user saw, so the joined list of correct options reads in the same
		// sequence as the controls. The front keeps stored order: its Extra stays parallel with fields.options.
		picked := make(map[int]bool, len(answers))
		for _, idx := range answers {
			picked[idx] = true
		}
		for _, idx := range card.OptionOrder {
			if picked[idx] {
				correct = append(correct, options[idx])
			}
		}
	} else {
		// No display order supplied (or one that does not fit the stored options): keep stored order.
		for _, idx := range answers {
			correct = append(correct, options[idx])
		}
	}
	return RenderResult{Body: strings.Join(correct, ", ")}, nil
}

// ChoiceMultiInput is the input of choiceMultiType.Grade; Selected is the set of 0-based option indices.
type ChoiceMultiInput struct {
	GradeContext
	Selected []int
}

// ParseAnswer decodes the answer: an array of 0-based option indices; no answer is equivalent to selecting nothing (a legitimate wrong answer).
func (choiceMultiType) ParseAnswer(gc GradeContext, raw json.RawMessage) (any, error) {
	selected := []int{}
	if !emptyAnswer(raw) {
		if err := json.Unmarshal(raw, &selected); err != nil {
			return nil, fmt.Errorf("choice_multi answer must be an array of option indices")
		}
	}
	return ChoiceMultiInput{GradeContext: gc, Selected: selected}, nil
}

// GivenText returns the texts of the selected options joined by commas.
func (choiceMultiType) GivenText(fields map[string]any, detail map[string]any) string {
	idxs, _ := detail["selected"].([]int)
	picked := make([]string, 0, len(idxs))
	for _, idx := range idxs {
		if text := optionText(fields, idx); text != "" {
			picked = append(picked, text)
		}
	}
	return strings.Join(picked, ", ")
}

// Grade grades and produces a partial score: score = max(0, hits - misses) / correct count, capped at 1.
// So "select every correct item" = 1 (Good), "select only some" = partial score (Hard by default),
// and "all wrong" = 0 (Again), satisfying the default mapping for partial scores.
// Any selected index out of range counts as ungradable.
func (choiceMultiType) Grade(input any) (int, map[string]any, bool) {
	in, ok := input.(ChoiceMultiInput)
	if !ok {
		return 0, nil, false
	}
	_, options, answers, err := choiceMultiParts(in.Fields)
	if err != nil {
		return 0, nil, false
	}
	correct := make(map[int]bool, len(answers))
	for _, idx := range answers {
		correct[idx] = true
	}
	selected := map[int]bool{}
	for _, idx := range in.Selected {
		if idx < 0 || idx >= len(options) {
			return 0, nil, false
		}
		selected[idx] = true
	}
	hits, misses := 0, 0
	for idx := range selected {
		if correct[idx] {
			hits++
		} else {
			misses++
		}
	}
	score := float64(hits-misses) / float64(len(answers))
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}
	detail := map[string]any{"answers": answers, "selected": in.Selected, "hits": hits, "misses": misses}
	return in.rating(score, detail)
}

// PromptContexter supplies the question and reference answer for future LLM scoring.
func (choiceMultiType) PromptContext(note Note) PromptContext {
	question, options, answers, err := choiceMultiParts(note.Fields)
	if err != nil {
		return PromptContext{}
	}
	correct := make([]string, 0, len(answers))
	for _, idx := range answers {
		correct = append(correct, options[idx])
	}
	return PromptContext{Question: question + "\n" + strings.Join(options, "\n"), Reference: strings.Join(correct, ", ")}
}

// choiceMultiParts reads and validates the three elements of a multiple-choice card.
func choiceMultiParts(fields map[string]any) (string, []string, []int, error) {
	question, err := stringField(fields, "question")
	if err != nil {
		return "", nil, nil, fmt.Errorf("choice_multi: %w", err)
	}
	options, err := stringSliceField(fields, "options")
	if err != nil {
		return "", nil, nil, fmt.Errorf("choice_multi: %w", err)
	}
	answers, err := intSliceField(fields, "answers")
	if err != nil {
		return "", nil, nil, fmt.Errorf("choice_multi: %w", err)
	}
	for _, idx := range answers {
		if idx >= len(options) {
			return "", nil, nil, fmt.Errorf("choice_multi: field %q index %d is out of range for %d options", "answers", idx, len(options))
		}
	}
	return question, options, answers, nil
}

// uniqueStrings reports whether a string array has no duplicates.
func uniqueStrings(ss []string) bool {
	seen := make(map[string]bool, len(ss))
	for _, s := range ss {
		if seen[s] {
			return false
		}
		seen[s] = true
	}
	return true
}

// uniqueInts reports whether an integer array has no duplicates.
func uniqueInts(is []int) bool {
	seen := make(map[int]bool, len(is))
	for _, i := range is {
		if seen[i] {
			return false
		}
		seen[i] = true
	}
	return true
}

// isPermutation reports whether order is a permutation of 0..n-1; anything else cannot be used as a display order.
func isPermutation(order []int, n int) bool {
	if len(order) != n {
		return false
	}
	seen := make([]bool, n)
	for _, idx := range order {
		if idx < 0 || idx >= n || seen[idx] {
			return false
		}
		seen[idx] = true
	}
	return true
}
