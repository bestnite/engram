package notetype

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// TestValidateAnswerSpecPlaceholder pins the rule that a template carrying an
// answer spec has exactly one type placeholder on its front.
func TestValidateAnswerSpecPlaceholder(t *testing.T) {
	base := func(front string, spec *AnswerSpec) Config {
		return Config{
			Kind:   KindNormal,
			Fields: []Field{{ID: "f1", Name: "answer"}, {ID: "f2", Name: "text"}},
			Templates: []Template{{
				ID: "t1", Name: "Card 1", QFormat: front, Answer: spec,
			}},
		}
	}
	spec := &AnswerSpec{Control: "text", Bindings: map[string]string{"answer": "f1"}}

	tests := []struct {
		name  string
		front string
		spec  *AnswerSpec
		rule  Rule
	}{
		{name: "one placeholder", front: "{{answer}}{{type:answer}}", spec: spec},
		{name: "one placeholder with a leading filter", front: "{{answer}}{{cloze:type:text}}", spec: spec},
		{name: "one non-combining placeholder", front: "{{answer}}{{type-nc:answer}}", spec: spec},
		{name: "no placeholder", front: "{{answer}}", spec: spec, rule: RuleAnswerTypePlaceholder},
		{name: "two placeholders", front: "{{answer}}{{type:answer}}{{type:answer}}", spec: spec, rule: RuleAnswerTypePlaceholder},
		{
			name:  "two placeholders in different written forms",
			front: "{{type:answer}}{{cloze:type:text}}",
			spec:  spec,
			rule:  RuleAnswerTypePlaceholder,
		},
		{name: "a template without a spec is not checked", front: "{{answer}}{{type:answer}}{{type:answer}}"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := base(test.front, test.spec).Validate()
			if test.rule == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			var validation *ValidationError
			if !errors.As(err, &validation) || validation.Rule != test.rule {
				t.Fatalf("Validate() = %v, want rule %q", err, test.rule)
			}
			if validation.Detail == "" || !strings.Contains(validation.Detail, `"Card 1"`) {
				t.Errorf("detail = %q, want the template name", validation.Detail)
			}
		})
	}
}

// TestExtractClozeBlanks pins the per-occurrence form used by a card with one
// input per blank: a repeated number keeps one entry per deletion, where
// ExtractClozeForTyping collapses and joins them.
func TestExtractClozeBlanks(t *testing.T) {
	const field = "one {{c1::two}} three {{c1::four::a hint}} five {{c2::six}}"
	tests := []struct {
		number int
		want   []string
	}{
		{1, []string{"two", "four"}},
		{2, []string{"six"}},
		{3, nil},
	}
	for _, test := range tests {
		got := ExtractClozeBlanks(field, test.number)
		if !reflect.DeepEqual(got, test.want) {
			t.Errorf("ExtractClozeBlanks(field, %d) = %q, want %q", test.number, got, test.want)
		}
	}
	if got := ExtractClozeForTyping(field, 1); got != "two, four" {
		t.Errorf("ExtractClozeForTyping(field, 1) = %q, want the collapsed form", got)
	}
}
