package cardtype

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// listType is the list card: given a prompt, the learner writes out the items one by one and the server grades item by item.
// Fields: prompt (required), items[] (required, non-empty), ordered (optional bool, defaults to false),
// extra / source_url (optional).
//
// The list card tests "how many items were recalled"; self-rating can only give an overall impression, so score partially by the number of items recalled.
type listType struct{}

// Label returns the language pack key.
func (listType) Label() string { return "cardtype.list" }

// Describe self-describes: prompt on the front, items on the back; the answer control is per-item input (one text box per item).
func (listType) Describe() Description {
	return Description{
		Kind:          "list",
		LabelKey:      "cardtype.list",
		AnswerControl: AnswerItems,
		FrontField:    "prompt",
		BackField:     "items",
		PromptField:   "prompt",
		Fields: fieldsWithCommon(
			FieldSpec{Key: "prompt", Control: ControlTextarea, Required: true},
			FieldSpec{Key: "items", Control: ControlLines, Required: true},
			// The server-side default for ordered is false (the third argument of cardtype.listType's boolField).
			FieldSpec{Key: "ordered", Control: ControlBool, Default: false},
		),
		Example: map[string]any{
			"prompt":  "Name the additive primary colours of light.",
			"items":   []any{"red", "green", "blue|blue-violet"},
			"ordered": false,
		},
	}
}

// Validate checks prompt / items / ordered; alternative answers separated by | inside an item must not leave empty segments.
func (listType) Validate(fields map[string]any) error {
	if _, err := stringField(fields, "prompt"); err != nil {
		return fmt.Errorf("list: %w", err)
	}
	items, err := stringSliceField(fields, "items")
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}
	for i, item := range items {
		for _, alt := range splitAlternatives(item, false) {
			if strings.TrimSpace(alt) == "" {
				return fmt.Errorf("list: field %q[%d] has an empty answer alternative around \"|\"", "items", i)
			}
		}
	}
	// ordered defaults to false (frozen table).
	if _, err := boolField(fields, "ordered", false); err != nil {
		return fmt.Errorf("list: %w", err)
	}
	if err := validateCommonOptional(fields); err != nil {
		return fmt.Errorf("list: %w", err)
	}
	return nil
}

// Cards produces one card.
func (listType) Cards(note Note) []Card {
	return []Card{{Template: "forward", Ordinal: 0, Fields: note.Fields}}
}

// Render gives only the prompt on the front; the back gives only the item list, without repeating the prompt (the review page renders the back below the front).
// ordered decides whether the back is a numbered or a bulleted list; when an item has several acceptable wordings, all are listed.
func (listType) Render(card Card, side Side) (RenderResult, error) {
	if card.Template != "forward" {
		return RenderResult{}, errUnknownTemplate(card.Template)
	}
	prompt, items, ordered, err := listParts(card.Fields)
	if err != nil {
		return RenderResult{}, err
	}
	if side == SideFront {
		return RenderResult{Body: prompt}, nil
	}
	var b strings.Builder
	for i, item := range items {
		if ordered {
			fmt.Fprintf(&b, "%d. ", i+1)
		} else {
			b.WriteString("- ")
		}
		b.WriteString(strings.Join(clozeAlternatives(item, false), " / "))
		b.WriteString("\n")
	}
	return RenderResult{Body: b.String()}, nil
}

// ListInput is the input of listType.Grade: Answers are the items the learner wrote out one by one (their order is the input box order).
type ListInput struct {
	GradeContext
	Answers []string
}

// ParseAnswer decodes the answer: a string array; no answer is equivalent to writing none of the items (graded all wrong).
func (listType) ParseAnswer(gc GradeContext, raw json.RawMessage) (any, error) {
	answers := []string{}
	if !emptyAnswer(raw) {
		if err := json.Unmarshal(raw, &answers); err != nil {
			return nil, errors.New("list answer must be an array of strings, one per item")
		}
	}
	return ListInput{GradeContext: gc, Answers: answers}, nil
}

// GivenText returns the non-empty items the learner wrote, joined by commas.
func (listType) GivenText(_ map[string]any, detail map[string]any) string {
	given, _ := detail["given"].([]string)
	written := make([]string, 0, len(given))
	for _, g := range given {
		if strings.TrimSpace(g) != "" {
			written = append(written, g)
		}
	}
	return strings.Join(written, ", ")
}

// Grade grades item by item: score = items recalled / total items. A wrong item simply scores nothing, with no penalty,
// which encourages the learner to write out everything they can think of.
//
//   - ordered=false: order does not matter, each answer can cover at most one item and each item can be covered at most once. Use bipartite maximum matching
//     rather than a greedy pass, or with overlapping alternative answers ("blue|purple" and "purple") first-come-first-served would undercount.
//   - ordered=true: the i-th answer is compared only with the i-th item, and a wrong position does not count.
//
// The comparison rules match the cloze card (see matchesAlternatives). More answers than items counts as ungradable.
func (listType) Grade(input any) (int, map[string]any, bool) {
	in, ok := input.(ListInput)
	if !ok {
		return 0, nil, false
	}
	_, items, ordered, err := listParts(in.Fields)
	if err != nil || len(in.Answers) > len(items) {
		return 0, nil, false
	}
	given := make([]string, len(items))
	copy(given, in.Answers)
	alts := make([][]string, len(items))
	for i, item := range items {
		alts[i] = clozeAlternatives(item, false)
	}
	correct := make([]bool, len(items))
	if ordered {
		for i := range items {
			correct[i] = matchesAlternatives(given[i], alts[i])
		}
	} else {
		correct = matchUnordered(given, alts)
	}
	hits := 0
	for _, c := range correct {
		if c {
			hits++
		}
	}
	detail := map[string]any{"items": alts, "given": given, "correct": correct, "ordered": ordered}
	return in.rating(float64(hits)/float64(len(items)), detail)
}

// Breakdown turns the grading detail back into per-item results: it gives each item's wording and whether it was recalled, in item order.
func (listType) Breakdown(detail map[string]any) []BreakdownItem {
	alts, _ := detail["items"].([][]string)
	correct, _ := detail["correct"].([]bool)
	if len(alts) != len(correct) {
		return nil
	}
	out := make([]BreakdownItem, len(alts))
	for i := range alts {
		out[i] = BreakdownItem{Answer: strings.Join(alts[i], " / "), Correct: correct[i]}
	}
	return out
}

// BlankHints gives the review page the same number of input boxes as there are items; list items have no hints, so all are empty strings.
func (listType) BlankHints(card Card) []string {
	items, err := stringSliceField(card.Fields, "items")
	if err != nil {
		return nil
	}
	return make([]string, len(items))
}

// matchUnordered finds the maximum matching between answers and items, returning whether each item is covered by some answer.
// The item count is in the single digits to tens, so an augmenting-path algorithm suffices; there is no need for a more elaborate matching implementation.
func matchUnordered(given []string, alts [][]string) []bool {
	// edges[g] lists the items the g-th answer can cover.
	edges := make([][]int, len(given))
	for g, answer := range given {
		for i := range alts {
			if matchesAlternatives(answer, alts[i]) {
				edges[g] = append(edges[g], i)
			}
		}
	}
	owner := make([]int, len(alts))
	for i := range owner {
		owner[i] = -1
	}
	var augment func(g int, seen []bool) bool
	augment = func(g int, seen []bool) bool {
		for _, i := range edges[g] {
			if seen[i] {
				continue
			}
			seen[i] = true
			if owner[i] < 0 || augment(owner[i], seen) {
				owner[i] = g
				return true
			}
		}
		return false
	}
	for g := range given {
		augment(g, make([]bool, len(alts)))
	}
	correct := make([]bool, len(alts))
	for i, g := range owner {
		correct[i] = g >= 0
	}
	return correct
}

// listParts reads and validates the three elements of a list card.
func listParts(fields map[string]any) (string, []string, bool, error) {
	prompt, err := stringField(fields, "prompt")
	if err != nil {
		return "", nil, false, fmt.Errorf("list: %w", err)
	}
	items, err := stringSliceField(fields, "items")
	if err != nil {
		return "", nil, false, fmt.Errorf("list: %w", err)
	}
	ordered, err := boolField(fields, "ordered", false)
	if err != nil {
		return "", nil, false, fmt.Errorf("list: %w", err)
	}
	return prompt, items, ordered, nil
}
