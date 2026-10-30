package answer

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// numericControl is the numeric-answer control: the learner types a number and
// it is graded by absolute and relative tolerance. The relative tolerance is a
// ratio of the absolute value, and either tolerance may carry the configured
// unit suffix, which is stripped before comparison.
type numericControl struct{}

func (numericControl) Name() string { return "numeric" }

// Roles declares the value and its tolerance rules.
func (numericControl) Roles() []Role {
	return []Role{
		{Name: "value", Kind: KindNumber, Required: true},
		{Name: "tolerance_absolute", Kind: KindNumber},
		{Name: "tolerance_relative", Kind: KindNumber},
		{Name: "unit", Kind: KindText},
	}
}

func init() { mustRegister("numeric", func() Control { return numericControl{} }) }

// RenderFront gives a text input that asks for a decimal.
func (numericControl) RenderFront(ctx RenderContext) (string, error) {
	return inputMarkup(ctx, "numeric", `type="text" inputmode="decimal" autocomplete="off"`), nil
}

// RenderBack writes the value and its unit.
func (numericControl) RenderBack(ctx RenderContext) (string, error) {
	value, err := ctx.Bindings.RequiredNumber("value")
	if err != nil {
		return "", err
	}
	body := formatNumber(value)
	if unit := ctx.Bindings.OptionalText("unit"); unit != "" {
		body += " " + unit
	}
	return boundaryMarkup("numeric", body), nil
}

// numericSettings is one grading pass's tolerance rules.
type numericSettings struct {
	value    float64
	absolute float64
	relative float64
	unit     string
}

// numericSettings resolves the tolerance rules from the bindings.
func resolveNumericSettings(bindings Bindings) (numericSettings, error) {
	value, err := bindings.RequiredNumber("value")
	if err != nil {
		return numericSettings{}, err
	}
	settings := numericSettings{value: value, unit: bindings.OptionalText("unit")}
	if absolute := bindings.OptionalNumber("tolerance_absolute"); absolute != nil {
		if *absolute < 0 {
			return numericSettings{}, fmt.Errorf(`role "tolerance_absolute" must not be negative`)
		}
		settings.absolute = *absolute
	}
	if relative := bindings.OptionalNumber("tolerance_relative"); relative != nil {
		if *relative < 0 || *relative > 1 {
			return numericSettings{}, fmt.Errorf(`role "tolerance_relative" must be between 0 and 1`)
		}
		settings.relative = *relative
	}
	return settings, nil
}

// Grade compares the typed number with the value. The answer may be a JSON
// string or a bare number; an unparseable answer scores 0.
func (numericControl) Grade(ctx GradeContext) (GradeResult, error) {
	settings, err := resolveNumericSettings(ctx.Bindings)
	if err != nil {
		return GradeResult{}, err
	}
	answer, err := numericAnswer(ctx.Answer)
	if err != nil {
		return GradeResult{}, err
	}
	detail := map[string]any{"value": settings.value, "given": answer}
	if settings.absolute > 0 {
		detail["tolerance_absolute"] = settings.absolute
	}
	if settings.relative > 0 {
		detail["tolerance_relative"] = settings.relative
	}
	if settings.unit != "" {
		detail["unit"] = settings.unit
	}
	parsed, ok := parseNumericAnswer(answer, settings.unit)
	if !ok {
		detail["parsed"] = false
		result := finish(ctx, 0, detail)
		result.Given = answer
		return result, nil
	}
	detail["parsed_answer"] = parsed
	allowed := settings.absolute
	if relative := settings.relative * math.Abs(settings.value); relative > allowed {
		allowed = relative
	}
	score := 0.0
	if math.Abs(parsed-settings.value) <= allowed {
		score = 1
	}
	result := finish(ctx, score, detail)
	result.Given = answer
	return result, nil
}

// numericAnswer decodes a numeric answer: a string, or a bare JSON number whose
// source text is kept. No answer is the empty string.
func numericAnswer(raw json.RawMessage) (string, error) {
	if emptyAnswer(raw) {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return "", fmt.Errorf("%w: numeric answer must be a string or a number", ErrInvalidAnswer)
	}
	return number.String(), nil
}

// parseNumericAnswer trims the input and strips the optional unit suffix
// (case-insensitively) before parsing a decimal.
func parseNumericAnswer(raw, unit string) (float64, bool) {
	text := strings.TrimSpace(raw)
	if unit != "" && strings.HasSuffix(strings.ToLower(text), strings.ToLower(unit)) {
		text = strings.TrimSpace(text[:len(text)-len(unit)])
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return value, true
}

// formatNumber prints a number in its simplest form.
func formatNumber(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}
