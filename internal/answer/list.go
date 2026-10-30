package answer

import (
	"fmt"
	"strings"
)

// listControl is the list control: the learner writes the items out one input
// per item, and the score is the number of items recalled divided by the total.
// A wrong item costs nothing, so the learner is encouraged to write everything
// they can think of.
//
// With ordered=false the order does not matter and each answer covers at most
// one item; a maximum matching is used so overlapping alternative wordings are
// counted correctly. With ordered=true the i-th answer is compared with the
// i-th item. An item may list several acceptable wordings separated by | or
// \alt.
type listControl struct{}

func (listControl) Name() string { return "list" }

// Roles declares the item list and the order rule.
func (listControl) Roles() []Role {
	return []Role{
		{Name: "items", Kind: KindLines, Required: true},
		{Name: "ordered", Kind: KindBool},
	}
}

func init() { mustRegister("list", func() Control { return listControl{} }) }

// RenderFront renders one input per item.
func (listControl) RenderFront(ctx RenderContext) (string, error) {
	items, err := ctx.Bindings.RequiredLines("items")
	if err != nil {
		return "", err
	}
	return inputListMarkup(ctx, "list", len(items)), nil
}

// RenderBack lists the items, numbering them when the list is ordered.
func (listControl) RenderBack(ctx RenderContext) (string, error) {
	items, err := ctx.Bindings.RequiredLines("items")
	if err != nil {
		return "", err
	}
	ordered := ctx.Bindings.OptionalBool("ordered", false)
	tag := "ul"
	if ordered {
		tag = "ol"
	}
	var b strings.Builder
	b.WriteString(`<` + tag + ` class="answer-breakdown" data-control="list">`)
	var correct []bool
	if ctx.Result != nil {
		correct, _ = ctx.Result.Detail["correct"].([]bool)
	}
	for i, item := range items {
		b.WriteString(`<li`)
		if i < len(correct) {
			b.WriteString(fmt.Sprintf(` data-correct="%t"`, correct[i]))
		}
		b.WriteString(`>` + escapeHTML(strings.Join(splitAlternatives(item), " / ")) + `</li>`)
	}
	b.WriteString(`</` + tag + `>`)
	return b.String(), nil
}

// Grade matches the written items against the expected items.
func (listControl) Grade(ctx GradeContext) (GradeResult, error) {
	items, err := listParts(ctx.Bindings)
	if err != nil {
		return GradeResult{}, err
	}
	answers, err := answerStringArray(ctx.Answer, "list")
	if err != nil {
		return GradeResult{}, err
	}
	if len(answers) > len(items) {
		return GradeResult{}, fmt.Errorf("%w: list has %d answers for %d items", ErrUngradable, len(answers), len(items))
	}
	given := make([]string, len(items))
	copy(given, answers)
	alternatives := make([][]string, len(items))
	for i, item := range items {
		alternatives[i] = splitAlternatives(item)
	}
	correct := make([]bool, len(items))
	if ctx.Bindings.OptionalBool("ordered", false) {
		for i := range items {
			correct[i] = matchesAny(given[i], alternatives[i])
		}
	} else {
		correct = matchUnordered(given, alternatives)
	}
	hits := 0
	for _, ok := range correct {
		if ok {
			hits++
		}
	}
	detail := map[string]any{
		"items":   alternatives,
		"given":   given,
		"correct": correct,
		"ordered": ctx.Bindings.OptionalBool("ordered", false),
	}
	result := finish(ctx, float64(hits)/float64(len(items)), detail)
	written := make([]string, 0, len(given))
	for _, answer := range given {
		if strings.TrimSpace(answer) != "" {
			written = append(written, answer)
		}
	}
	result.Given = strings.Join(written, ", ")
	return result, nil
}

// listParts resolves the item list and the order rule. An empty list cannot be
// graded because the score divides by the item count.
func listParts(bindings Bindings) ([]string, error) {
	items, err := bindings.RequiredLines("items")
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf(`role "items" must not be empty`)
	}
	return items, nil
}

// matchesAny reports whether the answer equals one of the acceptable wordings.
func matchesAny(given string, alternatives []string) bool {
	for _, alternative := range alternatives {
		if matchesExpected(given, alternative, true) {
			return true
		}
	}
	return false
}

// matchUnordered finds the maximum matching between answers and items, so an
// answer covers at most one item and an item is covered at most once.
func matchUnordered(given []string, alternatives [][]string) []bool {
	edges := make([][]int, len(given))
	for g, answer := range given {
		for i := range alternatives {
			if matchesAny(answer, alternatives[i]) {
				edges[g] = append(edges[g], i)
			}
		}
	}
	owner := make([]int, len(alternatives))
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
		augment(g, make([]bool, len(alternatives)))
	}
	correct := make([]bool, len(alternatives))
	for i, g := range owner {
		correct[i] = g >= 0
	}
	return correct
}
