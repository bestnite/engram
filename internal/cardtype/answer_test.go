package cardtype

import (
	"encoding/json"
	"errors"
	"testing"
)

// 编译期断言：五个作答类题型实现完整的 Grader。Grader 加方法时漏实现的题型会在这里编译失败，
// 而不是在运行时被静默当成「可以自评」的题型。
var (
	_ Grader = typedType{}
	_ Grader = numericType{}
	_ Grader = choiceSingleType{}
	_ Grader = choiceMultiType{}
	_ Grader = trueFalseType{}
)

// TestGradedKindsAreExactlyTheFive 断言注册表里实现 Grader 的题型恰好是这五个：
// 自评题型被误判为作答题会让它无法复习，反之会让作答题回到客户端自评。
func TestGradedKindsAreExactlyTheFive(t *testing.T) {
	want := map[string]bool{"typed": true, "numeric": true, "choice_single": true, "choice_multi": true, "true_false": true}
	for _, kind := range []string{"basic", "basic_both", "cloze", "list", "short_answer", "typed", "numeric", "choice_single", "choice_multi", "true_false"} {
		ct, ok := Lookup(kind)
		if !ok {
			continue
		}
		_, graded := ct.(Grader)
		if graded != want[kind] {
			t.Errorf("kind %q graded = %v, want %v", kind, graded, want[kind])
		}
	}
}

// TestGradeAnswerFromJSON 覆盖各题型的 JSON 作答解码与判分（含反面：类型不对、缺作答、越界）。
func TestGradeAnswerFromJSON(t *testing.T) {
	typed := map[string]any{"prompt": "capital of France", "answer": "Paris"}
	numeric := map[string]any{"prompt": "g", "value": 9.8, "tolerance_absolute": 0.1, "unit": "m/s2"}
	single := map[string]any{"question": "q", "options": []any{"a", "b", "c"}, "answer": 1.0}
	multi := map[string]any{"question": "q", "options": []any{"a", "b", "c"}, "answers": []any{0.0, 2.0}}
	tf := map[string]any{"statement": "s", "answer": true}
	cases := []struct {
		name        string
		kind        string
		fields      map[string]any
		raw         string
		wantErr     bool
		ungradable  bool
		wantRating  int
		wantVerdict string
		wantGiven   string
	}{
		{name: "typed correct", kind: "typed", fields: typed, raw: `"Paris"`, wantRating: RatingGood, wantVerdict: VerdictCorrect, wantGiven: "Paris"},
		{name: "typed missing answer grades as wrong", kind: "typed", fields: typed, raw: ``, wantRating: RatingAgain, wantVerdict: VerdictIncorrect},
		{name: "typed non-string rejected", kind: "typed", fields: typed, raw: `42`, wantErr: true},
		{name: "numeric string with unit", kind: "numeric", fields: numeric, raw: `"9.85 m/s2"`, wantRating: RatingGood, wantVerdict: VerdictCorrect, wantGiven: "9.85 m/s2"},
		{name: "numeric bare number", kind: "numeric", fields: numeric, raw: `9.8`, wantRating: RatingGood, wantVerdict: VerdictCorrect, wantGiven: "9.8"},
		{name: "numeric object rejected", kind: "numeric", fields: numeric, raw: `{}`, wantErr: true},
		{name: "single index", kind: "choice_single", fields: single, raw: `1`, wantRating: RatingGood, wantVerdict: VerdictCorrect, wantGiven: "b"},
		{name: "single index as string", kind: "choice_single", fields: single, raw: `"0"`, wantRating: RatingAgain, wantVerdict: VerdictIncorrect, wantGiven: "a"},
		{name: "single missing answer rejected", kind: "choice_single", fields: single, raw: `null`, wantErr: true},
		{name: "single out of range is ungradable", kind: "choice_single", fields: single, raw: `9`, ungradable: true},
		{name: "multi partial", kind: "choice_multi", fields: multi, raw: `[0]`, wantRating: RatingHard, wantVerdict: VerdictPartial, wantGiven: "a"},
		{name: "multi empty is wrong", kind: "choice_multi", fields: multi, raw: ``, wantRating: RatingAgain, wantVerdict: VerdictIncorrect},
		{name: "multi non-array rejected", kind: "choice_multi", fields: multi, raw: `"0,2"`, wantErr: true},
		{name: "true_false correct", kind: "true_false", fields: tf, raw: `true`, wantRating: RatingGood, wantVerdict: VerdictCorrect, wantGiven: "true"},
		{name: "true_false missing rejected", kind: "true_false", fields: tf, raw: ``, wantErr: true},
		{name: "true_false string rejected", kind: "true_false", fields: tf, raw: `"true"`, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ct, _ := Lookup(tc.kind)
			out, err := GradeAnswer(ct.(Grader), GradeContext{Fields: tc.fields}, json.RawMessage(tc.raw))
			switch {
			case tc.wantErr:
				if err == nil || errors.Is(err, ErrUngradable) {
					t.Fatalf("GradeAnswer() error = %v, want a decode error", err)
				}
				return
			case tc.ungradable:
				if !errors.Is(err, ErrUngradable) {
					t.Fatalf("GradeAnswer() error = %v, want ErrUngradable", err)
				}
				return
			case err != nil:
				t.Fatalf("GradeAnswer() error = %v", err)
			}
			if out.Rating != tc.wantRating || out.Verdict != tc.wantVerdict || out.Given != tc.wantGiven {
				t.Errorf("outcome = rating %d verdict %q given %q, want %d %q %q",
					out.Rating, out.Verdict, out.Given, tc.wantRating, tc.wantVerdict, tc.wantGiven)
			}
			if out.Detail["rating"] != out.Rating {
				t.Errorf("detail rating = %v, want %d", out.Detail["rating"], out.Rating)
			}
		})
	}
}
