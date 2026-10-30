package answer

import (
	"fmt"
	"strconv"
)

// trueFalseControl is the true/false control: the learner judges a statement,
// and the answer is the JSON boolean true or false.
type trueFalseControl struct{}

func (trueFalseControl) Name() string { return "true_false" }

// Roles declares the correct boolean.
func (trueFalseControl) Roles() []Role {
	return []Role{{Name: "answer", Kind: KindBool, Required: true}}
}

func init() { mustRegister("true_false", func() Control { return trueFalseControl{} }) }

// RenderFront renders one radio button for true and one for false.
func (trueFalseControl) RenderFront(ctx RenderContext) (string, error) {
	if _, err := ctx.Bindings.RequiredBool("answer"); err != nil {
		return "", err
	}
	id := elementID(ctx)
	var markup string
	for _, value := range []bool{true, false} {
		markup += `<label class="answer-choice"><input type="radio" name="` + escapeAttribute(id) +
			`" value="` + strconv.FormatBool(value) + `" data-control="true_false"` + styleAttribute(ctx) +
			`> ` + strconv.FormatBool(value) + `</label>`
	}
	return `<div class="answer-choices" data-control="true_false">` + markup + `</div>`, nil
}

// RenderBack writes the correct boolean.
func (trueFalseControl) RenderBack(ctx RenderContext) (string, error) {
	answer, err := ctx.Bindings.RequiredBool("answer")
	if err != nil {
		return "", err
	}
	return boundaryMarkup("true_false", strconv.FormatBool(answer)), nil
}

// Grade scores 1 for the correct judgement and 0 for the wrong one. A missing
// or non-boolean answer is a decode error.
func (trueFalseControl) Grade(ctx GradeContext) (GradeResult, error) {
	answer, err := ctx.Bindings.RequiredBool("answer")
	if err != nil {
		return GradeResult{}, err
	}
	if emptyAnswer(ctx.Answer) {
		return GradeResult{}, fmt.Errorf("%w: true_false answer is required", ErrInvalidAnswer)
	}
	selected, err := answerBool(ctx.Answer)
	if err != nil {
		return GradeResult{}, err
	}
	detail := map[string]any{"answer": answer, "selected": selected}
	score := 0.0
	if selected == answer {
		score = 1
	}
	result := finish(ctx, score, detail)
	result.Given = strconv.FormatBool(selected)
	return result, nil
}
