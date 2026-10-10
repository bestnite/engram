package cardtype

import (
	"encoding/json"
	"errors"
	"testing"
)

// 编译期断言：七个作答类题型实现完整的 Grader。Grader 加方法时漏实现的题型会在这里编译失败，
// 而不是在运行时被静默当成「可以自评」的题型。
var (
	_ Grader = typedType{}
	_ Grader = numericType{}
	_ Grader = choiceSingleType{}
	_ Grader = choiceMultiType{}
	_ Grader = trueFalseType{}
	_ Grader = clozeType{}
	_ Grader = listType{}
)

// TestGradedKindsAreExactlyTheSeven 断言注册表里实现 Grader 的题型恰好是这七个：
// 自评题型被误判为作答题会让它无法复习，反之会让作答题回到客户端自评。
func TestGradedKindsAreExactlyTheSeven(t *testing.T) {
	want := map[string]bool{"typed": true, "numeric": true, "choice_single": true, "choice_multi": true, "true_false": true, "cloze": true, "list": true}
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
	colours := map[string]any{"prompt": "p", "items": []any{"red", "green", "blue|violet"}}
	steps := map[string]any{"prompt": "p", "items": []any{"wash", "cut", "cook"}, "ordered": true}
	overlap := map[string]any{"prompt": "p", "items": []any{"blue|violet", "violet"}}
	clozeAlt := map[string]any{"text": "{{c1::Paris|巴黎}} and {{c2::a\\|b}}"}
	cloze := map[string]any{"text": "{{c1::Paris}} and {{c1::Rome::city}} are capitals; {{c2::\\(2x\\)}} is a derivative."}
	cases := []struct {
		name        string
		kind        string
		template    string
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
		{name: "cloze all blanks correct ignoring case and spaces", kind: "cloze", template: "cloze:1", fields: cloze, raw: `["  paris", "ROME "]`, wantRating: RatingGood, wantVerdict: VerdictCorrect, wantGiven: "  paris, ROME "},
		{name: "cloze one of two blanks is partial", kind: "cloze", template: "cloze:1", fields: cloze, raw: `["Paris", "Milan"]`, wantRating: RatingHard, wantVerdict: VerdictPartial, wantGiven: "Paris, Milan"},
		{name: "cloze answers in swapped order are wrong", kind: "cloze", template: "cloze:1", fields: cloze, raw: `["Rome", "Paris"]`, wantRating: RatingAgain, wantVerdict: VerdictIncorrect, wantGiven: "Rome, Paris"},
		{name: "cloze fewer answers count missing blanks as wrong", kind: "cloze", template: "cloze:1", fields: cloze, raw: `["Paris"]`, wantRating: RatingHard, wantVerdict: VerdictPartial, wantGiven: "Paris, "},
		{name: "cloze missing answer grades as wrong", kind: "cloze", template: "cloze:1", fields: cloze, raw: ``, wantRating: RatingAgain, wantVerdict: VerdictIncorrect, wantGiven: ", "},
		{name: "cloze math answer without delimiters", kind: "cloze", template: "cloze:2", fields: cloze, raw: `["2x"]`, wantRating: RatingGood, wantVerdict: VerdictCorrect, wantGiven: "2x"},
		{name: "cloze math answer with delimiters", kind: "cloze", template: "cloze:2", fields: cloze, raw: `["\\(2x\\)"]`, wantRating: RatingGood, wantVerdict: VerdictCorrect, wantGiven: `\(2x\)`},
		{name: "cloze blank answer never matches", kind: "cloze", template: "cloze:2", fields: cloze, raw: `["  "]`, wantRating: RatingAgain, wantVerdict: VerdictIncorrect, wantGiven: "  "},
		{name: "cloze more answers than blanks is ungradable", kind: "cloze", template: "cloze:2", fields: cloze, raw: `["2x", "extra"]`, ungradable: true},
		{name: "cloze unknown cloze number is ungradable", kind: "cloze", template: "cloze:9", fields: cloze, raw: `["x"]`, ungradable: true},
		{name: "cloze alternative answer accepted", kind: "cloze", template: "cloze:1", fields: clozeAlt, raw: `["巴黎"]`, wantRating: RatingGood, wantVerdict: VerdictCorrect, wantGiven: "巴黎"},
		{name: "cloze first alternative accepted", kind: "cloze", template: "cloze:1", fields: clozeAlt, raw: `["paris"]`, wantRating: RatingGood, wantVerdict: VerdictCorrect, wantGiven: "paris"},
		{name: "cloze answer outside the alternatives is wrong", kind: "cloze", template: "cloze:1", fields: clozeAlt, raw: `["Paris|巴黎"]`, wantRating: RatingAgain, wantVerdict: VerdictIncorrect, wantGiven: "Paris|巴黎"},
		{name: "cloze escaped pipe is typed as a plain pipe", kind: "cloze", template: "cloze:2", fields: clozeAlt, raw: `["a|b"]`, wantRating: RatingGood, wantVerdict: VerdictCorrect, wantGiven: "a|b"},
		{name: "list all items in any order", kind: "list", template: "forward", fields: colours, raw: `["Blue", "red", "GREEN"]`, wantRating: RatingGood, wantVerdict: VerdictCorrect, wantGiven: "Blue, red, GREEN"},
		{name: "list two of three is partial", kind: "list", template: "forward", fields: colours, raw: `["red", "", "violet"]`, wantRating: RatingHard, wantVerdict: VerdictPartial, wantGiven: "red, violet"},
		{name: "list wrong entries do not cost points", kind: "list", template: "forward", fields: colours, raw: `["red", "pink", "green"]`, wantRating: RatingHard, wantVerdict: VerdictPartial, wantGiven: "red, pink, green"},
		{name: "list repeating one item counts once", kind: "list", template: "forward", fields: colours, raw: `["red", "red", "red"]`, wantRating: RatingHard, wantVerdict: VerdictPartial, wantGiven: "red, red, red"},
		{name: "list nothing written is wrong", kind: "list", template: "forward", fields: colours, raw: ``, wantRating: RatingAgain, wantVerdict: VerdictIncorrect},
		{name: "list overlapping alternatives use the best matching", kind: "list", template: "forward", fields: overlap, raw: `["violet", "blue"]`, wantRating: RatingGood, wantVerdict: VerdictCorrect, wantGiven: "violet, blue"},
		{name: "ordered list in order", kind: "list", template: "forward", fields: steps, raw: `["wash", "cut", "cook"]`, wantRating: RatingGood, wantVerdict: VerdictCorrect, wantGiven: "wash, cut, cook"},
		{name: "ordered list out of order loses the misplaced items", kind: "list", template: "forward", fields: steps, raw: `["wash", "cook", "cut"]`, wantRating: RatingHard, wantVerdict: VerdictPartial, wantGiven: "wash, cook, cut"},
		{name: "list more answers than items is ungradable", kind: "list", template: "forward", fields: colours, raw: `["a", "b", "c", "d"]`, ungradable: true},
		{name: "list non-array rejected", kind: "list", template: "forward", fields: colours, raw: `"red"`, wantErr: true},
		{name: "cloze non-array rejected", kind: "cloze", template: "cloze:1", fields: cloze, raw: `"Paris"`, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ct, _ := Lookup(tc.kind)
			out, err := GradeAnswer(ct.(Grader), GradeContext{Fields: tc.fields, Template: tc.template}, json.RawMessage(tc.raw))
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

// TestClozeBlankHints 断言逐空提示按出现顺序只列本卡目标序号的挖空，无提示为空串；非法模板得到 nil。
func TestClozeBlankHints(t *testing.T) {
	fields := map[string]any{"text": "{{c1::Paris}} and {{c2::x}} and {{c1::Rome::city}}"}
	cases := []struct {
		template string
		want     []string
	}{
		{"cloze:1", []string{"", "city"}},
		{"cloze:2", []string{""}},
		{"cloze:9", nil},
		{"forward", nil},
	}
	for _, tc := range cases {
		got := clozeType{}.BlankHints(Card{Template: tc.template, Fields: fields})
		if len(got) != len(tc.want) {
			t.Errorf("%s: BlankHints = %q, want %q", tc.template, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: BlankHints = %q, want %q", tc.template, got, tc.want)
				break
			}
		}
	}
}

// TestListBreakdown 断言逐项结果按条目顺序给出写法与是否想起，与判分细节一致。
func TestListBreakdown(t *testing.T) {
	fields := map[string]any{"prompt": "p", "items": []any{"red", "green", "blue|violet"}}
	out, err := GradeAnswer(listType{}, GradeContext{Fields: fields, Template: "forward"}, json.RawMessage(`["violet", "red"]`))
	if err != nil {
		t.Fatalf("GradeAnswer: %v", err)
	}
	got := listType{}.Breakdown(out.Detail)
	want := []BreakdownItem{{"red", true}, {"green", false}, {"blue / violet", true}}
	if len(got) != len(want) {
		t.Fatalf("Breakdown = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Breakdown[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}
