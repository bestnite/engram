package cardtype

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestShortAnswerRegistered is the acceptance: all ten card types frozen in the docs are registered,
// and short_answer is among them.
func TestShortAnswerRegistered(t *testing.T) {
	want := []string{
		"basic", "basic_both", "choice_multi", "choice_single", "cloze",
		"list", "numeric", "short_answer", "true_false", "typed",
	}
	if got := Kinds(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Kinds() = %v, want the ten documented types %v", got, want)
	}
	if _, ok := Lookup("short_answer"); !ok {
		t.Fatal("short_answer is not registered")
	}
}

// TestShortAnswerIsSelfAssessed asserts short_answer does not implement Grader: the review flow takes the self-rating path for it
// ("self-rate first"; LLM grading is left for later).
func TestShortAnswerIsSelfAssessed(t *testing.T) {
	impl, ok := Lookup("short_answer")
	if !ok {
		t.Fatal("short_answer is not registered")
	}
	if _, ok := impl.(Grader); ok {
		t.Fatal("short_answer must not implement Grader; it is self-assessed")
	}
	if got := impl.Label(); got != "cardtype.short_answer" {
		t.Fatalf("Label() = %q, want cardtype.short_answer", got)
	}
}

// TestShortAnswerValidate covers the required prompt, the optional reference, and type validation of the optional fields.
func TestShortAnswerValidate(t *testing.T) {
	valid := map[string]any{"prompt": "解释这个公式的适用条件"}
	if err := Validate("short_answer", valid); err != nil {
		t.Fatalf("Validate(prompt only) = %v, want nil", err)
	}
	if err := Validate("short_answer", map[string]any{"prompt": "q", "reference": "a"}); err != nil {
		t.Fatalf("Validate(prompt+reference) = %v, want nil", err)
	}

	bad := []struct {
		name   string
		fields map[string]any
		token  string
	}{
		{"missing prompt", map[string]any{"reference": "a"}, `"prompt"`},
		{"empty prompt", map[string]any{"prompt": ""}, `"prompt"`},
		{"prompt not string", map[string]any{"prompt": 3}, `"prompt"`},
		{"empty reference", map[string]any{"prompt": "q", "reference": ""}, `"reference"`},
		{"reference not string", map[string]any{"prompt": "q", "reference": 1}, `"reference"`},
		{"extra not object", map[string]any{"prompt": "q", "extra": "x"}, `"extra"`},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate("short_answer", tc.fields)
			if err == nil {
				t.Fatalf("Validate(%v) = nil, want error naming %s", tc.fields, tc.token)
			}
			if !strings.Contains(err.Error(), "short_answer") || !strings.Contains(err.Error(), tc.token) {
				t.Fatalf("Validate() = %v, want it to name the type and %s", err, tc.token)
			}
		})
	}
}

// TestShortAnswerCardsAndRender asserts it produces 1 forward card, covering both sides:
// the front gives only the prompt; the back gives the reference answer (when reference is present), and echoes the prompt when it is absent.
func TestShortAnswerCardsAndRender(t *testing.T) {
	note := Note{Kind: "short_answer", Fields: map[string]any{"prompt": "为什么？", "reference": "因为 X。"}}
	cards := shortAnswerType{}.Cards(note)
	if len(cards) != 1 || cards[0].Template != "forward" || cards[0].Ordinal != 0 {
		t.Fatalf("Cards() = %+v, want one forward card at ordinal 0", cards)
	}

	front, err := shortAnswerType{}.Render(cards[0], SideFront)
	if err != nil {
		t.Fatalf("Render(front) error = %v", err)
	}
	if front.Body != "为什么？" || len(front.Options) != 0 {
		t.Fatalf("Render(front) = %+v, want prompt only", front)
	}

	back, err := shortAnswerType{}.Render(cards[0], SideBack)
	if err != nil {
		t.Fatalf("Render(back) error = %v", err)
	}
	// The back gives only the reference answer, without repeating the prompt: the review page renders the back below the front.
	if back.Body != "因为 X。" || len(back.Options) != 0 {
		t.Fatalf("Render(back) = %+v, want the reference only", back)
	}

	noRef := Card{Template: "forward", Fields: map[string]any{"prompt": "为什么？"}}
	back, err = shortAnswerType{}.Render(noRef, SideBack)
	if err != nil {
		t.Fatalf("Render(back, no reference) error = %v", err)
	}
	if back.Body != "" || len(back.Options) != 0 {
		t.Fatalf("Render(back, no reference) = %+v, want an empty back", back)
	}

	if _, err := (shortAnswerType{}).Render(Card{Template: "reverse"}, SideBack); err == nil {
		t.Fatal("Render(unknown template) = nil, want error")
	}
}

// TestShortAnswerRecordAnswer asserts the answer written before self-rating is archived verbatim; nothing is stored when it is unwritten or whitespace only;
// a non-string answer is rejected.
func TestShortAnswerRecordAnswer(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    map[string]any
		wantErr bool
	}{
		{"written answer is kept verbatim", `"  因为月球绕地球转。 "`, map[string]any{"answer": "  因为月球绕地球转。 "}, false},
		{"missing answer stores nothing", ``, nil, false},
		{"blank answer stores nothing", `"   "`, nil, false},
		{"non-string rejected", `42`, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := shortAnswerType{}.RecordAnswer(json.RawMessage(tc.raw))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("RecordAnswer() = %v, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("RecordAnswer() error = %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("RecordAnswer() = %v, want %v", got, tc.want)
			}
		})
	}
}
