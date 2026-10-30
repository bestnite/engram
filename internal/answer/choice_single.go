package answer

import (
	"fmt"
)

// choiceSingleControl is the single-choice control: the learner picks one of
// the option lines, and the answer is the zero-based index of that option.
type choiceSingleControl struct{}

func (choiceSingleControl) Name() string { return "choice_single" }

// Roles declares the option list and the correct index.
func (choiceSingleControl) Roles() []Role {
	return []Role{
		{Name: "options", Kind: KindLines, Required: true},
		{Name: "answer", Kind: KindNumber, Required: true},
	}
}

func init() { mustRegister("choice_single", func() Control { return choiceSingleControl{} }) }

// RenderFront renders one radio button per option.
func (choiceSingleControl) RenderFront(ctx RenderContext) (string, error) {
	options, _, err := choiceSingleParts(ctx.Bindings)
	if err != nil {
		return "", err
	}
	return choiceMarkup(ctx, "choice_single", options, "radio"), nil
}

// RenderBack gives the correct option's text.
func (choiceSingleControl) RenderBack(ctx RenderContext) (string, error) {
	options, answer, err := choiceSingleParts(ctx.Bindings)
	if err != nil {
		return "", err
	}
	return boundaryMarkup("choice_single", options[answer]), nil
}

// Grade scores 1 for the correct option and 0 for another option. An index
// outside the option list cannot be graded.
func (choiceSingleControl) Grade(ctx GradeContext) (GradeResult, error) {
	options, answer, err := choiceSingleParts(ctx.Bindings)
	if err != nil {
		return GradeResult{}, err
	}
	selected, err := answerIndex(ctx.Answer, "choice_single")
	if err != nil {
		return GradeResult{}, err
	}
	if selected < 0 || selected >= len(options) {
		return GradeResult{}, fmt.Errorf("%w: choice_single option index %d is out of range", ErrUngradable, selected)
	}
	detail := map[string]any{"answer": answer, "selected": selected}
	score := 0.0
	if selected == answer {
		score = 1
	}
	result := finish(ctx, score, detail)
	result.Given = options[selected]
	return result, nil
}

// choiceSingleParts resolves the option list and the correct index.
func choiceSingleParts(bindings Bindings) ([]string, int, error) {
	options, err := bindings.RequiredLines("options")
	if err != nil {
		return nil, 0, err
	}
	if len(options) == 0 {
		return nil, 0, fmt.Errorf(`role "options" must not be empty`)
	}
	value, err := bindings.RequiredNumber("answer")
	if err != nil {
		return nil, 0, err
	}
	answer, err := optionIndex(value, "answer")
	if err != nil {
		return nil, 0, err
	}
	if answer >= len(options) {
		return nil, 0, fmt.Errorf(`role "answer" index %d is out of range for %d options`, answer, len(options))
	}
	return options, answer, nil
}
