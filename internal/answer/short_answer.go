package answer

import (
	"strings"
)

// shortAnswerControl is the self-assessed control: the learner writes a free
// answer, compares it with an optional reference, and then chooses the rating
// themselves. Grade therefore returns no machine rating; it returns the written
// answer for the review record and marks the result self-assessed, and the
// caller keeps the rating the learner chose.
type shortAnswerControl struct{}

func (shortAnswerControl) Name() string { return "short_answer" }

// Roles declares the optional reference answer.
func (shortAnswerControl) Roles() []Role {
	return []Role{{Name: "reference", Kind: KindText}}
}

func init() { mustRegister("short_answer", func() Control { return shortAnswerControl{} }) }

// RenderFront renders a free-form text area.
func (shortAnswerControl) RenderFront(ctx RenderContext) (string, error) {
	return `<textarea id="` + escapeAttribute("short_answer-"+elementID(ctx)) +
		`" class="answer-input" data-control="short_answer"` + styleAttribute(ctx) + `></textarea>`, nil
}

// RenderBack shows the reference answer, when one is bound.
func (shortAnswerControl) RenderBack(ctx RenderContext) (string, error) {
	return boundaryMarkup("short_answer", ctx.Bindings.OptionalText("reference")), nil
}

// Grade returns the neutral self-assessment result: no score and rating 0,
// which no FSRS rating can take, so the caller cannot mistake it for a machine
// rating. The written answer is kept in the detail when it is not blank.
func (shortAnswerControl) Grade(ctx GradeContext) (GradeResult, error) {
	answer, err := answerString(ctx.Answer, "short_answer")
	if err != nil {
		return GradeResult{}, err
	}
	detail := map[string]any{"self_assessed": true}
	if strings.TrimSpace(answer) != "" {
		detail["answer"] = answer
	}
	return GradeResult{Detail: detail}, nil
}
