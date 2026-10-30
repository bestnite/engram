package answer

import (
	"fmt"
	"strings"

	"git.nite07.com/nite/engram/internal/notetype"
)

// clozeBlanksControl is the cloze control: one input per deletion carrying the
// card's cloze number, graded blank by blank. There is no alternative-answer
// rule here: | and \alt are ordinary content, so each deletion has exactly one
// expected wording.
type clozeBlanksControl struct{}

func (clozeBlanksControl) Name() string { return "cloze_blanks" }

// Roles declares the field that holds the cloze text.
func (clozeBlanksControl) Roles() []Role {
	return []Role{{Name: "text", Kind: KindText, Required: true}}
}

func init() { mustRegister("cloze_blanks", func() Control { return clozeBlanksControl{} }) }

// RenderFront renders one input per target deletion, in order of appearance.
func (clozeBlanksControl) RenderFront(ctx RenderContext) (string, error) {
	expected, err := clozeExpected(ctx.Bindings, ctx.ClozeOrdinal)
	if err != nil {
		return "", err
	}
	return inputListMarkup(ctx, "cloze_blanks", len(expected)), nil
}

// RenderBack lists the expected wording of every blank.
func (clozeBlanksControl) RenderBack(ctx RenderContext) (string, error) {
	expected, err := clozeExpected(ctx.Bindings, ctx.ClozeOrdinal)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(`<ol class="answer-breakdown" data-control="cloze_blanks">`)
	var correct []bool
	if ctx.Result != nil {
		correct, _ = ctx.Result.Detail["correct"].([]bool)
	}
	for i, text := range expected {
		b.WriteString(`<li`)
		if i < len(correct) {
			b.WriteString(fmt.Sprintf(` data-correct="%t"`, correct[i]))
		}
		b.WriteString(`>` + escapeHTML(text) + `</li>`)
	}
	b.WriteString(`</ol>`)
	return b.String(), nil
}

// Grade compares each submitted answer with its deletion. More answers than
// blanks cannot be graded because the answer is out of step with the card face;
// fewer answers leave the missing blanks empty and therefore wrong.
func (clozeBlanksControl) Grade(ctx GradeContext) (GradeResult, error) {
	expected, err := clozeExpected(ctx.Bindings, ctx.ClozeOrdinal)
	if err != nil {
		return GradeResult{}, err
	}
	answers, err := answerStringArray(ctx.Answer, "cloze")
	if err != nil {
		return GradeResult{}, err
	}
	if len(answers) > len(expected) {
		return GradeResult{}, fmt.Errorf("%w: %d answers for %d blanks", ErrUngradable, len(answers), len(expected))
	}
	given := make([]string, len(expected))
	copy(given, answers)
	correct := make([]bool, len(expected))
	hits := 0
	wordings := make([][]string, len(expected))
	for i, wording := range expected {
		wordings[i] = []string{wording}
		if matchesExpected(given[i], wording, false) {
			correct[i] = true
			hits++
		}
	}
	detail := map[string]any{"answers": wordings, "given": given, "correct": correct}
	result := finish(ctx, float64(hits)/float64(len(expected)), detail)
	result.Given = strings.Join(given, ", ")
	return result, nil
}

// clozeExpected returns the text of every deletion carrying the card's cloze
// number. A card with no such deletion cannot be graded.
func clozeExpected(bindings Bindings, ordinal int) ([]string, error) {
	if ordinal <= 0 {
		return nil, fmt.Errorf("%w: cloze_blanks needs the card's cloze number", ErrUngradable)
	}
	text, err := bindings.RequiredText("text")
	if err != nil {
		return nil, err
	}
	expected := notetype.ExtractClozeBlanks(text, ordinal)
	if len(expected) == 0 {
		return nil, fmt.Errorf("%w: cloze number %d occurs in no deletion", ErrUngradable, ordinal)
	}
	return expected, nil
}
