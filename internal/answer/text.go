package answer

import (
	"fmt"
	"regexp"
	"strings"
)

// textControl is the free-text control: the learner types an answer and it is
// compared with the canonical answer and, optionally, the accepted wordings.
// The comparison ignores case and whitespace unless the spec turns that off,
// and an optional regular expression takes precedence over literal comparison.
type textControl struct{}

func (textControl) Name() string { return "text" }

// Roles declares the text answer and its tolerance rules.
func (textControl) Roles() []Role {
	return []Role{
		{Name: "answer", Kind: KindText, Required: true},
		{Name: "accept", Kind: KindLines},
		{Name: "ignore_case", Kind: KindBool},
		{Name: "ignore_whitespace", Kind: KindBool},
		{Name: "regex", Kind: KindText},
	}
}

func init() { mustRegister("text", func() Control { return textControl{} }) }

// RenderFront gives a single-line text input.
func (textControl) RenderFront(ctx RenderContext) (string, error) {
	return inputMarkup(ctx, "text", `type="text" autocomplete="off"`), nil
}

// RenderBack lists the canonical answer and every accepted wording on one
// line, separated by " / ".
func (textControl) RenderBack(ctx RenderContext) (string, error) {
	answer, err := ctx.Bindings.RequiredText("answer")
	if err != nil {
		return "", err
	}
	wordings := append([]string{answer}, ctx.Bindings.OptionalLines("accept")...)
	return boundaryMarkup("text", strings.Join(wordings, " / ")), nil
}

// textSettings is one grading pass's comparison rules.
type textSettings struct {
	answer           string
	accept           []string
	ignoreCase       bool
	ignoreWhitespace bool
	regex            *regexp.Regexp
}

// textSettings resolves the comparison rules from the bindings. ignore_case and
// ignore_whitespace default to true; an empty regex role means no regex.
func resolveTextSettings(bindings Bindings) (textSettings, error) {
	answer, err := bindings.RequiredText("answer")
	if err != nil {
		return textSettings{}, err
	}
	settings := textSettings{
		answer:           answer,
		accept:           bindings.OptionalLines("accept"),
		ignoreCase:       bindings.OptionalBool("ignore_case", true),
		ignoreWhitespace: bindings.OptionalBool("ignore_whitespace", true),
	}
	pattern := bindings.OptionalText("regex")
	if pattern == "" {
		return settings, nil
	}
	if settings.ignoreCase {
		pattern = "(?i)" + pattern
	}
	compiled, err := regexp.Compile(`\A(?:` + pattern + `)\z`)
	if err != nil {
		return textSettings{}, fmt.Errorf("role %q is not a valid regular expression: %w", "regex", err)
	}
	settings.regex = compiled
	return settings, nil
}

// Grade compares the typed text. A regex match scores 1, otherwise a literal
// match against accept[] or answer scores 1, and no match scores 0.
func (textControl) Grade(ctx GradeContext) (GradeResult, error) {
	settings, err := resolveTextSettings(ctx.Bindings)
	if err != nil {
		return GradeResult{}, err
	}
	answer, err := answerString(ctx.Answer, "text")
	if err != nil {
		return GradeResult{}, err
	}
	given := normalizeText(answer, settings.ignoreCase, settings.ignoreWhitespace)
	detail := map[string]any{"answer": settings.answer, "given": answer}

	if settings.regex != nil {
		detail["matched"] = "regex"
		score := 0.0
		if settings.regex.MatchString(given) {
			score = 1
		}
		result := finish(ctx, score, detail)
		result.Given = answer
		return result, nil
	}
	for i, candidate := range settings.accept {
		if normalizeText(candidate, settings.ignoreCase, settings.ignoreWhitespace) == given {
			detail["matched"] = fmt.Sprintf("accept[%d]", i)
			result := finish(ctx, 1, detail)
			result.Given = answer
			return result, nil
		}
	}
	if normalizeText(settings.answer, settings.ignoreCase, settings.ignoreWhitespace) == given {
		detail["matched"] = "answer"
		result := finish(ctx, 1, detail)
		result.Given = answer
		return result, nil
	}
	result := finish(ctx, 0, detail)
	result.Given = answer
	return result, nil
}

// normalizeText applies the ignore_case / ignore_whitespace rules.
func normalizeText(text string, ignoreCase, ignoreWhitespace bool) string {
	if ignoreWhitespace {
		text = strings.Join(strings.Fields(text), " ")
	}
	if ignoreCase {
		text = strings.ToLower(text)
	}
	return text
}
