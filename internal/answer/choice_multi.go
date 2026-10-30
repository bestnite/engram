package answer

import (
	"fmt"
	"strings"
)

// choiceMultiControl is the multiple-choice control: the learner picks a set of
// options, and the answer is the array of zero-based indices they selected.
// The score is max(0, hits - misses) / number of correct options, capped at 1,
// so selecting every correct option is full credit and a wrong extra option
// cancels one hit.
type choiceMultiControl struct{}

func (choiceMultiControl) Name() string { return "choice_multi" }

// Roles declares the option list and the correct indices.
func (choiceMultiControl) Roles() []Role {
	return []Role{
		{Name: "options", Kind: KindLines, Required: true},
		{Name: "answers", Kind: KindLines, Required: true},
	}
}

func init() { mustRegister("choice_multi", func() Control { return choiceMultiControl{} }) }

// RenderFront renders one checkbox per option.
func (choiceMultiControl) RenderFront(ctx RenderContext) (string, error) {
	options, _, err := choiceMultiParts(ctx.Bindings)
	if err != nil {
		return "", err
	}
	return choiceMarkup(ctx, "choice_multi", options, "checkbox"), nil
}

// RenderBack lists the correct options in the display order the learner saw,
// or in stored order when no valid display order was supplied.
func (choiceMultiControl) RenderBack(ctx RenderContext) (string, error) {
	options, answers, err := choiceMultiParts(ctx.Bindings)
	if err != nil {
		return "", err
	}
	correct := make([]string, 0, len(answers))
	if isPermutation(ctx.OptionOrder, len(options)) {
		picked := make(map[int]bool, len(answers))
		for _, index := range answers {
			picked[index] = true
		}
		for _, index := range ctx.OptionOrder {
			if picked[index] {
				correct = append(correct, options[index])
			}
		}
	} else {
		for _, index := range answers {
			correct = append(correct, options[index])
		}
	}
	return boundaryMarkup("choice_multi", strings.Join(correct, ", ")), nil
}

// Grade applies the partial-credit rule. A selected index outside the option
// list cannot be graded.
func (choiceMultiControl) Grade(ctx GradeContext) (GradeResult, error) {
	options, answers, err := choiceMultiParts(ctx.Bindings)
	if err != nil {
		return GradeResult{}, err
	}
	selected, err := answerIntArray(ctx.Answer, "choice_multi")
	if err != nil {
		return GradeResult{}, err
	}
	correct := make(map[int]bool, len(answers))
	for _, index := range answers {
		correct[index] = true
	}
	unique := make(map[int]bool, len(selected))
	for _, index := range selected {
		if index < 0 || index >= len(options) {
			return GradeResult{}, fmt.Errorf("%w: choice_multi option index %d is out of range", ErrUngradable, index)
		}
		unique[index] = true
	}
	hits, misses := 0, 0
	for index := range unique {
		if correct[index] {
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
	detail := map[string]any{"answers": answers, "selected": selected, "hits": hits, "misses": misses}
	result := finish(ctx, score, detail)
	written := make([]string, 0, len(selected))
	for _, index := range selected {
		if index >= 0 && index < len(options) {
			written = append(written, options[index])
		}
	}
	result.Given = strings.Join(written, ", ")
	return result, nil
}

// choiceMultiParts resolves the option list and the correct indices.
func choiceMultiParts(bindings Bindings) ([]string, []int, error) {
	options, err := bindings.RequiredLines("options")
	if err != nil {
		return nil, nil, err
	}
	if len(options) == 0 {
		return nil, nil, fmt.Errorf(`role "options" must not be empty`)
	}
	lines, err := bindings.RequiredLines("answers")
	if err != nil {
		return nil, nil, err
	}
	answers, err := optionIndices(lines, "answers")
	if err != nil {
		return nil, nil, err
	}
	if len(answers) == 0 {
		return nil, nil, fmt.Errorf(`role "answers" must not be empty`)
	}
	for _, index := range answers {
		if index >= len(options) {
			return nil, nil, fmt.Errorf(`role "answers" index %d is out of range for %d options`, index, len(options))
		}
	}
	return options, answers, nil
}

// isPermutation reports whether order is a permutation of 0..n-1; anything else
// cannot be used as a display order.
func isPermutation(order []int, n int) bool {
	if len(order) != n {
		return false
	}
	seen := make([]bool, n)
	for _, index := range order {
		if index < 0 || index >= n || seen[index] {
			return false
		}
		seen[index] = true
	}
	return true
}
