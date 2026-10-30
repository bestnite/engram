package answer

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/notetype"
)

// The grading cases in this file are ported from internal/cardtype's
// graded_test.go, answer_test.go and cloze_test.go. The field values are HTML
// and the bindings name the same roles as field ids. A cloze case whose field
// used | or \alt follows the removed rule: both are ordinary content, so each
// deletion has exactly one wording.

// specFor binds every field to the role of the same name.
func specFor(control string, fields map[string]string) notetype.AnswerSpec {
	bindings := make(map[string]string, len(fields))
	for role := range fields {
		bindings[role] = role
	}
	return notetype.AnswerSpec{Control: control, Bindings: bindings}
}

// runGrade resolves the bindings and grades one answer.
func runGrade(t *testing.T, control string, fields map[string]string, raw string, ordinal int) (GradeResult, error) {
	t.Helper()
	bindings, err := ResolveBindings(specFor(control, fields), fields)
	if err != nil {
		return GradeResult{}, err
	}
	implementation, ok := Lookup(control)
	if !ok {
		t.Fatalf("control %q is not registered", control)
	}
	return implementation.Grade(GradeContext{
		Bindings:     bindings,
		Answer:       json.RawMessage(raw),
		ClozeOrdinal: ordinal,
	})
}

// assertDetail compares the named subset of a detail map.
func assertDetail(t *testing.T, detail map[string]any, want map[string]any) {
	t.Helper()
	for key, expected := range want {
		if !reflect.DeepEqual(detail[key], expected) {
			t.Errorf("detail[%q] = %#v, want %#v", key, detail[key], expected)
		}
	}
}

// TestTextGrading ports TestTypedGrading.
func TestTextGrading(t *testing.T) {
	cases := []struct {
		name       string
		fields     map[string]string
		answer     string
		wantRating int
		wantGiven  string
		wantDetail map[string]any
	}{
		{
			"exact", map[string]string{"answer": "Paris"}, "Paris", RatingGood, "Paris",
			map[string]any{"answer": "Paris", "given": "Paris", "matched": "answer"},
		},
		{
			"case ignored by default", map[string]string{"answer": "Paris"}, "paris", RatingGood, "paris", nil,
		},
		{
			"whitespace collapsed by default", map[string]string{"answer": "Paris"}, "  Paris  ", RatingGood, "  Paris  ", nil,
		},
		{
			"inner whitespace collapsed", map[string]string{"answer": "New York"}, "New   York", RatingGood, "New   York", nil,
		},
		{
			"wrong answer is Again", map[string]string{"answer": "Paris"}, "London", RatingAgain, "London",
			map[string]any{"score": float64(0), "rating": RatingAgain},
		},
		{
			"case sensitive when ignore_case=false",
			map[string]string{"answer": "Paris", "ignore_case": "false"}, "paris", RatingAgain, "paris", nil,
		},
		{
			"whitespace significant when ignore_whitespace=false",
			map[string]string{"answer": "Paris", "ignore_whitespace": "false"}, " Paris", RatingAgain, " Paris", nil,
		},
		{
			"accept list alternative",
			map[string]string{"answer": "Paris", "accept": "City of Paris<br>PARIS"}, "City of Paris", RatingGood, "City of Paris",
			map[string]any{"matched": "accept[0]"},
		},
		{
			"accept list second entry",
			map[string]string{"answer": "Paris", "accept": "City of Paris"}, "CITY OF PARIS", RatingGood, "CITY OF PARIS",
			map[string]any{"matched": "accept[0]"},
		},
		{
			"regex takes precedence over literal",
			map[string]string{"answer": "unused", "regex": `\d+/\d+`}, "3/4", RatingGood, "3/4",
			map[string]any{"matched": "regex"},
		},
		{
			"regex full match required",
			map[string]string{"answer": "unused", "regex": `\d+`}, "12a", RatingAgain, "12a",
			map[string]any{"matched": "regex"},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			result, err := runGrade(t, "text", test.fields, mustJSON(test.answer), 0)
			if err != nil {
				t.Fatalf("Grade: %v", err)
			}
			if result.Rating != test.wantRating {
				t.Fatalf("rating = %d, want %d (detail %v)", result.Rating, test.wantRating, result.Detail)
			}
			if result.Given != test.wantGiven {
				t.Errorf("given = %q, want %q", result.Given, test.wantGiven)
			}
			if result.Detail["rating"] != result.Rating {
				t.Errorf("detail rating = %v, want %d", result.Detail["rating"], result.Rating)
			}
			assertDetail(t, result.Detail, test.wantDetail)
		})
	}

	t.Run("non-string answer rejected", func(t *testing.T) {
		if _, err := runGrade(t, "text", map[string]string{"answer": "Paris"}, `42`, 0); !errors.Is(err, ErrInvalidAnswer) {
			t.Fatalf("Grade error = %v, want ErrInvalidAnswer", err)
		}
	})
	t.Run("no answer is graded as the empty string", func(t *testing.T) {
		result, err := runGrade(t, "text", map[string]string{"answer": "Paris"}, ``, 0)
		if err != nil {
			t.Fatalf("Grade: %v", err)
		}
		if result.Rating != RatingAgain || result.Given != "" {
			t.Fatalf("result = %+v, want Again with an empty given", result)
		}
	})
	t.Run("invalid regex names the role", func(t *testing.T) {
		_, err := runGrade(t, "text", map[string]string{"answer": "a", "regex": "("}, `"a"`, 0)
		if err == nil || !strings.Contains(err.Error(), `role "regex"`) {
			t.Fatalf("Grade error = %v, want an invalid-regex error naming the role", err)
		}
	})
}

// TestNumericGrading ports TestNumericGrading.
func TestNumericGrading(t *testing.T) {
	cases := []struct {
		name       string
		fields     map[string]string
		answer     string
		wantRating int
		wantGiven  string
		wantDetail map[string]any
	}{
		{
			"exact", map[string]string{"value": "50"}, `"50"`, RatingGood, "50",
			map[string]any{"value": float64(50), "parsed_answer": float64(50)},
		},
		{
			"within absolute tolerance", map[string]string{"value": "100", "tolerance_absolute": "2"},
			`"101.5"`, RatingGood, "101.5", map[string]any{"tolerance_absolute": float64(2)},
		},
		{
			"outside absolute tolerance", map[string]string{"value": "100", "tolerance_absolute": "2"},
			`"103"`, RatingAgain, "103", nil,
		},
		{
			"within relative tolerance", map[string]string{"value": "200", "tolerance_relative": "0.01"},
			`"201"`, RatingGood, "201", map[string]any{"tolerance_relative": 0.01},
		},
		{
			"outside relative tolerance", map[string]string{"value": "200", "tolerance_relative": "0.01"},
			`"205"`, RatingAgain, "205", nil,
		},
		{
			"unit suffix is stripped", map[string]string{"value": "5", "unit": "kg", "tolerance_absolute": "0.1"},
			`"5 kg"`, RatingGood, "5 kg", map[string]any{"unit": "kg", "parsed_answer": float64(5)},
		},
		{
			"non-numeric input is wrong", map[string]string{"value": "5"},
			`"five"`, RatingAgain, "five", map[string]any{"parsed": false},
		},
		{
			"a bare JSON number keeps its source text", map[string]string{"value": "9.8"},
			`9.8`, RatingGood, "9.8", nil,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			result, err := runGrade(t, "numeric", test.fields, test.answer, 0)
			if err != nil {
				t.Fatalf("Grade: %v", err)
			}
			if result.Rating != test.wantRating {
				t.Fatalf("rating = %d, want %d (detail %v)", result.Rating, test.wantRating, result.Detail)
			}
			if result.Given != test.wantGiven {
				t.Errorf("given = %q, want %q", result.Given, test.wantGiven)
			}
			assertDetail(t, result.Detail, test.wantDetail)
		})
	}

	t.Run("object answer rejected", func(t *testing.T) {
		if _, err := runGrade(t, "numeric", map[string]string{"value": "1"}, `{}`, 0); !errors.Is(err, ErrInvalidAnswer) {
			t.Fatalf("Grade error = %v, want ErrInvalidAnswer", err)
		}
	})
	t.Run("negative tolerance is rejected", func(t *testing.T) {
		_, err := runGrade(t, "numeric", map[string]string{"value": "1", "tolerance_absolute": "-1"}, `"1"`, 0)
		if err == nil || !strings.Contains(err.Error(), "tolerance_absolute") {
			t.Fatalf("Grade error = %v, want a negative-tolerance error", err)
		}
	})
	t.Run("relative tolerance above one is rejected", func(t *testing.T) {
		_, err := runGrade(t, "numeric", map[string]string{"value": "1", "tolerance_relative": "1.5"}, `"1"`, 0)
		if err == nil || !strings.Contains(err.Error(), "tolerance_relative") {
			t.Fatalf("Grade error = %v, want a relative-tolerance error", err)
		}
	})
}

// TestChoiceSingleGrading ports TestChoiceSingleGrading and the corresponding
// JSON cases from TestGradeAnswerFromJSON.
func TestChoiceSingleGrading(t *testing.T) {
	fields := map[string]string{"options": "a<br>b<br>c", "answer": "2"}
	cases := []struct {
		name       string
		raw        string
		wantRating int
		wantGiven  string
	}{
		{"correct selection", `2`, RatingGood, "c"},
		{"wrong selection", `0`, RatingAgain, "a"},
		{"index as string", `"0"`, RatingAgain, "a"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			result, err := runGrade(t, "choice_single", fields, test.raw, 0)
			if err != nil {
				t.Fatalf("Grade: %v", err)
			}
			if result.Rating != test.wantRating || result.Given != test.wantGiven {
				t.Fatalf("result = %d %q, want %d %q", result.Rating, result.Given, test.wantRating, test.wantGiven)
			}
			if result.Detail["answer"] != 2 {
				t.Errorf("detail = %v", result.Detail)
			}
		})
	}

	t.Run("out of range selection is ungradable", func(t *testing.T) {
		if _, err := runGrade(t, "choice_single", fields, `9`, 0); !errors.Is(err, ErrUngradable) {
			t.Fatalf("Grade error = %v, want ErrUngradable", err)
		}
	})
	t.Run("missing answer rejected", func(t *testing.T) {
		if _, err := runGrade(t, "choice_single", fields, `null`, 0); !errors.Is(err, ErrInvalidAnswer) {
			t.Fatalf("Grade error = %v, want ErrInvalidAnswer", err)
		}
	})
	t.Run("answer index out of range", func(t *testing.T) {
		if _, err := runGrade(t, "choice_single", map[string]string{"options": "a<br>b", "answer": "2"}, `0`, 0); err == nil {
			t.Fatal("Grade error = nil, want an out-of-range field error")
		}
	})
}

// TestChoiceMultiGrading ports TestChoiceMultiGrading, including the partial
// score that maps to Hard.
func TestChoiceMultiGrading(t *testing.T) {
	fields := map[string]string{"options": "a<br>b<br>c", "answers": "0<br>2"}
	cases := []struct {
		name       string
		raw        string
		wantRating int
	}{
		{"all correct is Good", `[0,2]`, RatingGood},
		{"partial credit maps to Hard", `[0]`, RatingHard},
		{"partial with one wrong extra still Hard", `[0,1,2]`, RatingHard},
		{"all wrong is Again", `[1]`, RatingAgain},
		{"empty selection is Again", ``, RatingAgain},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			result, err := runGrade(t, "choice_multi", fields, test.raw, 0)
			if err != nil {
				t.Fatalf("Grade: %v", err)
			}
			if result.Rating != test.wantRating {
				t.Fatalf("rating = %d, want %d (detail %v)", result.Rating, test.wantRating, result.Detail)
			}
		})
	}

	t.Run("hits and misses are reported", func(t *testing.T) {
		result, err := runGrade(t, "choice_multi", fields, `[0,1,2]`, 0)
		if err != nil {
			t.Fatalf("Grade: %v", err)
		}
		assertDetail(t, result.Detail, map[string]any{
			"answers": []int{0, 2}, "selected": []int{0, 1, 2}, "hits": 2, "misses": 1,
		})
	})
	t.Run("out of range selection is ungradable", func(t *testing.T) {
		if _, err := runGrade(t, "choice_multi", fields, `[9]`, 0); !errors.Is(err, ErrUngradable) {
			t.Fatalf("Grade error = %v, want ErrUngradable", err)
		}
	})
	t.Run("non-array answer rejected", func(t *testing.T) {
		if _, err := runGrade(t, "choice_multi", fields, `"0,2"`, 0); !errors.Is(err, ErrInvalidAnswer) {
			t.Fatalf("Grade error = %v, want ErrInvalidAnswer", err)
		}
	})
	t.Run("unparseable answer index", func(t *testing.T) {
		if _, err := runGrade(t, "choice_multi", map[string]string{"options": "a<br>b", "answers": "0<br>x"}, `[0]`, 0); err == nil {
			t.Fatal("Grade error = nil, want an unparseable-index error")
		}
	})
}

// TestChoiceMultiBackFollowsOptionOrder ports TestChoiceMultiBackFollowsOptionOrder.
func TestChoiceMultiBackFollowsOptionOrder(t *testing.T) {
	fields := map[string]string{"options": "alpha<br>beta<br>gamma<br>delta", "answers": "0<br>2"}
	bindings, err := ResolveBindings(specFor("choice_multi", fields), fields)
	if err != nil {
		t.Fatalf("ResolveBindings: %v", err)
	}
	control, _ := Lookup("choice_multi")

	back, err := control.RenderBack(RenderContext{Bindings: bindings, OptionOrder: []int{2, 0, 3, 1}})
	if err != nil {
		t.Fatalf("RenderBack: %v", err)
	}
	if !strings.Contains(back, "gamma, alpha") {
		t.Errorf("back = %q, want the correct options in display order", back)
	}
	back, err = control.RenderBack(RenderContext{Bindings: bindings})
	if err != nil {
		t.Fatalf("RenderBack: %v", err)
	}
	if !strings.Contains(back, "alpha, gamma") {
		t.Errorf("back = %q, want stored order", back)
	}
}

// TestTrueFalseGrading ports TestTrueFalseGrading.
func TestTrueFalseGrading(t *testing.T) {
	fields := map[string]string{"answer": "true"}
	cases := []struct {
		raw        string
		wantRating int
		wantGiven  string
	}{
		{`true`, RatingGood, "true"},
		{`false`, RatingAgain, "false"},
	}
	for _, test := range cases {
		t.Run(test.raw, func(t *testing.T) {
			result, err := runGrade(t, "true_false", fields, test.raw, 0)
			if err != nil {
				t.Fatalf("Grade: %v", err)
			}
			if result.Rating != test.wantRating || result.Given != test.wantGiven {
				t.Fatalf("result = %d %q, want %d %q", result.Rating, result.Given, test.wantRating, test.wantGiven)
			}
		})
	}

	t.Run("unanswered is a decode error", func(t *testing.T) {
		if _, err := runGrade(t, "true_false", fields, ``, 0); !errors.Is(err, ErrInvalidAnswer) {
			t.Fatalf("Grade error = %v, want ErrInvalidAnswer", err)
		}
	})
	t.Run("string answer rejected", func(t *testing.T) {
		if _, err := runGrade(t, "true_false", fields, `"true"`, 0); !errors.Is(err, ErrInvalidAnswer) {
			t.Fatalf("Grade error = %v, want ErrInvalidAnswer", err)
		}
	})
	t.Run("unparseable bool field", func(t *testing.T) {
		if _, err := runGrade(t, "true_false", map[string]string{"answer": "yes"}, `true`, 0); err == nil {
			t.Fatal("Grade error = nil, want an unparseable-bool error")
		}
	})
}

// TestListGrading ports the list cases of TestGradeAnswerFromJSON and
// TestListBreakdown.
func TestListGrading(t *testing.T) {
	colours := map[string]string{"items": "red<br>green<br>blue|violet"}
	steps := map[string]string{"items": "wash<br>cut<br>cook", "ordered": "true"}
	overlap := map[string]string{"items": "blue|violet<br>violet"}
	cases := []struct {
		name       string
		fields     map[string]string
		raw        string
		wantRating int
		wantGiven  string
	}{
		{"all items in any order", colours, `["Blue","red","GREEN"]`, RatingGood, "Blue, red, GREEN"},
		{"two of three is partial", colours, `["red","","violet"]`, RatingHard, "red, violet"},
		{"wrong entries do not cost points", colours, `["red","pink","green"]`, RatingHard, "red, pink, green"},
		{"repeating one item counts once", colours, `["red","red","red"]`, RatingHard, "red, red, red"},
		{"nothing written is wrong", colours, ``, RatingAgain, ""},
		{"overlapping alternatives use the best matching", overlap, `["violet","blue"]`, RatingGood, "violet, blue"},
		{"ordered list in order", steps, `["wash","cut","cook"]`, RatingGood, "wash, cut, cook"},
		{"ordered list out of order loses the misplaced items", steps, `["wash","cook","cut"]`, RatingHard, "wash, cook, cut"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			result, err := runGrade(t, "list", test.fields, test.raw, 0)
			if err != nil {
				t.Fatalf("Grade: %v", err)
			}
			if result.Rating != test.wantRating {
				t.Fatalf("rating = %d, want %d (detail %v)", result.Rating, test.wantRating, result.Detail)
			}
			if result.Given != test.wantGiven {
				t.Errorf("given = %q, want %q", result.Given, test.wantGiven)
			}
		})
	}

	t.Run("detail keeps the alternative wordings and per-item flags", func(t *testing.T) {
		result, err := runGrade(t, "list", colours, `["violet","red"]`, 0)
		if err != nil {
			t.Fatalf("Grade: %v", err)
		}
		assertDetail(t, result.Detail, map[string]any{
			"items":   [][]string{{"red"}, {"green"}, {"blue", "violet"}},
			"correct": []bool{true, false, true},
			"ordered": false,
		})
	})
	t.Run("more answers than items is ungradable", func(t *testing.T) {
		if _, err := runGrade(t, "list", colours, `["a","b","c","d"]`, 0); !errors.Is(err, ErrUngradable) {
			t.Fatalf("Grade error = %v, want ErrUngradable", err)
		}
	})
	t.Run("non-array answer rejected", func(t *testing.T) {
		if _, err := runGrade(t, "list", colours, `"red"`, 0); !errors.Is(err, ErrInvalidAnswer) {
			t.Fatalf("Grade error = %v, want ErrInvalidAnswer", err)
		}
	})
	t.Run("a pipe inside a formula is content", func(t *testing.T) {
		result, err := runGrade(t, "list", map[string]string{"items": `\(|x|\)`}, `["\\(|x|\\)"]`, 0)
		if err != nil {
			t.Fatalf("Grade: %v", err)
		}
		assertDetail(t, result.Detail, map[string]any{"items": [][]string{{`\(|x|\)`}}})
	})
}

// TestClozeGrading ports the cloze grading cases of TestGradeAnswerFromJSON.
func TestClozeGrading(t *testing.T) {
	text := `{{c1::Paris}} and {{c1::Rome::city}} are capitals; {{c2::\(2x\)}} is a derivative.`
	fields := map[string]string{"text": text}
	cases := []struct {
		name       string
		ordinal    int
		raw        string
		wantRating int
		wantGiven  string
		wantDetail map[string]any
	}{
		{
			"all blanks correct ignoring case and spaces", 1, `["  paris","ROME "]`, RatingGood, "  paris, ROME ",
			map[string]any{"answers": [][]string{{"Paris"}, {"Rome"}}, "correct": []bool{true, true}},
		},
		{
			"one of two blanks is partial", 1, `["Paris","Milan"]`, RatingHard, "Paris, Milan",
			map[string]any{"correct": []bool{true, false}},
		},
		{
			"answers in swapped order are wrong", 1, `["Rome","Paris"]`, RatingAgain, "Rome, Paris",
			map[string]any{"correct": []bool{false, false}},
		},
		{
			"fewer answers count missing blanks as wrong", 1, `["Paris"]`, RatingHard, "Paris, ",
			map[string]any{"given": []string{"Paris", ""}},
		},
		{
			"missing answer grades as wrong", 1, ``, RatingAgain, ", ",
			map[string]any{"given": []string{"", ""}},
		},
		{"math answer without delimiters", 2, `["2x"]`, RatingGood, "2x", nil},
		{"math answer with delimiters", 2, `["\\(2x\\)"]`, RatingGood, `\(2x\)`, nil},
		{"blank answer never matches", 2, `["  "]`, RatingAgain, "  ", nil},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			result, err := runGrade(t, "cloze_blanks", fields, test.raw, test.ordinal)
			if err != nil {
				t.Fatalf("Grade: %v", err)
			}
			if result.Rating != test.wantRating {
				t.Fatalf("rating = %d, want %d (detail %v)", result.Rating, test.wantRating, result.Detail)
			}
			if result.Given != test.wantGiven {
				t.Errorf("given = %q, want %q", result.Given, test.wantGiven)
			}
			assertDetail(t, result.Detail, test.wantDetail)
		})
	}

	t.Run("more answers than blanks is ungradable", func(t *testing.T) {
		if _, err := runGrade(t, "cloze_blanks", fields, `["2x","extra"]`, 2); !errors.Is(err, ErrUngradable) {
			t.Fatalf("Grade error = %v, want ErrUngradable", err)
		}
	})
	t.Run("unknown cloze number is ungradable", func(t *testing.T) {
		if _, err := runGrade(t, "cloze_blanks", fields, `["x"]`, 9); !errors.Is(err, ErrUngradable) {
			t.Fatalf("Grade error = %v, want ErrUngradable", err)
		}
	})
	t.Run("non-array answer rejected", func(t *testing.T) {
		if _, err := runGrade(t, "cloze_blanks", fields, `"Paris"`, 1); !errors.Is(err, ErrInvalidAnswer) {
			t.Fatalf("Grade error = %v, want ErrInvalidAnswer", err)
		}
	})
}

// TestClozeNoAlternativeRule pins the removed rule: | and \alt are ordinary
// content, so a cloze has exactly one wording.
func TestClozeNoAlternativeRule(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		ordinal    int
		raw        string
		wantRating int
		wantDetail map[string]any
	}{
		{
			"a pipe is part of the single wording", "{{c1::Paris|巴黎}}", 1, `["巴黎"]`, RatingAgain,
			map[string]any{"answers": [][]string{{"Paris|巴黎"}}},
		},
		{
			"the whole wording with a pipe matches literally", "{{c1::Paris|巴黎}}", 1, `["Paris|巴黎"]`, RatingGood, nil,
		},
		{
			"an escaped pipe is not unescaped", "{{c1::a\\|b}}", 1, `["a|b"]`, RatingAgain,
			map[string]any{"answers": [][]string{{`a\|b`}}},
		},
		{
			"the escaped form matches itself", "{{c1::a\\|b}}", 1, `["a\\|b"]`, RatingGood, nil,
		},
		{
			"a backslash-alt is part of the wording", `\({{c1::r_1 \times r_2 \alt r1*r2}}\)`, 1, `["r1*r2"]`, RatingAgain,
			map[string]any{"answers": [][]string{{`r_1 \times r_2 \alt r1*r2`}}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := runGrade(t, "cloze_blanks", map[string]string{"text": test.text}, test.raw, test.ordinal)
			if err != nil {
				t.Fatalf("Grade: %v", err)
			}
			if result.Rating != test.wantRating {
				t.Fatalf("rating = %d, want %d (detail %v)", result.Rating, test.wantRating, result.Detail)
			}
			assertDetail(t, result.Detail, test.wantDetail)
		})
	}

	t.Run("a pipe inside a formula is the whole wording", func(t *testing.T) {
		result, err := runGrade(t, "cloze_blanks", map[string]string{"text": `\({{c1::|x|}}\)`}, `["|x|"]`, 1)
		if err != nil {
			t.Fatalf("Grade: %v", err)
		}
		if result.Rating != RatingGood {
			t.Fatalf("rating = %d, want Good (detail %v)", result.Rating, result.Detail)
		}
	})
}

// TestGradeMapping ports TestGradeMappingDefaults and the custom-mapping check
// of TestGradeMappingCustom.
func TestGradeMappingDefaults(t *testing.T) {
	mapping := DefaultGradeMapping()
	tests := []struct {
		score float64
		want  int
	}{
		{1, RatingGood},
		{0.5, RatingHard},
		{0.01, RatingHard},
		{0, RatingAgain},
	}
	for _, test := range tests {
		if got := mapping.RatingFor(test.score); got != test.want {
			t.Errorf("RatingFor(%v) = %d, want %d", test.score, got, test.want)
		}
	}
}

// TestGradeMappingCustom asserts a preset-provided mapping replaces the default.
func TestGradeMappingCustom(t *testing.T) {
	custom := GradeMapping{Full: RatingEasy, Partial: RatingAgain, None: RatingAgain}
	if got := custom.RatingFor(1); got != RatingEasy {
		t.Errorf("custom full = %d, want %d", got, RatingEasy)
	}
	if got := custom.RatingFor(0.5); got != RatingAgain {
		t.Errorf("custom partial = %d, want %d", got, RatingAgain)
	}

	fields := map[string]string{"options": "a<br>b<br>c", "answers": "0<br>2"}
	bindings, err := ResolveBindings(specFor("choice_multi", fields), fields)
	if err != nil {
		t.Fatalf("ResolveBindings: %v", err)
	}
	control, _ := Lookup("choice_multi")
	result, err := control.Grade(GradeContext{Bindings: bindings, Answer: json.RawMessage(`[0]`), Mapping: &custom})
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}
	if result.Rating != RatingAgain {
		t.Fatalf("custom mapping not applied: rating = %d, want %d", result.Rating, RatingAgain)
	}
}

// TestShortAnswerGrading asserts the self-assessed control returns no machine
// rating and keeps the written answer.
func TestShortAnswerGrading(t *testing.T) {
	result, err := runGrade(t, "short_answer", map[string]string{"reference": "because"}, mustJSON("my answer"), 0)
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}
	if result.Rating != 0 || result.Score != 0 {
		t.Fatalf("result = %+v, want no machine rating", result)
	}
	assertDetail(t, result.Detail, map[string]any{"self_assessed": true, "answer": "my answer"})

	blank, err := runGrade(t, "short_answer", nil, `"   "`, 0)
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}
	if _, exists := blank.Detail["answer"]; exists {
		t.Errorf("blank answer was stored")
	}
	if _, err := runGrade(t, "short_answer", nil, `42`, 0); !errors.Is(err, ErrInvalidAnswer) {
		t.Fatalf("Grade error = %v, want ErrInvalidAnswer", err)
	}
}

// TestControlsRegistered asserts the eight controls are registered.
func TestControlsRegistered(t *testing.T) {
	want := []string{"choice_multi", "choice_single", "cloze_blanks", "list", "numeric", "short_answer", "text", "true_false"}
	if got := Names(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
	for _, name := range want {
		control, ok := Lookup(name)
		if !ok || control.Name() != name {
			t.Errorf("Lookup(%q) = %v, %v", name, control, ok)
		}
		if _, ok := control.(Roles); !ok {
			t.Errorf("control %q does not declare its roles", name)
		}
	}
}

// TestRenderFrontReplacesThePlaceholder covers each control's input markup and
// the element id the frame reads.
func TestRenderFrontReplacesThePlaceholder(t *testing.T) {
	fieldsByControl := map[string]map[string]string{
		"text":          {"answer": "Paris"},
		"numeric":       {"value": "1"},
		"choice_single": {"options": "a<br>b", "answer": "0"},
		"choice_multi":  {"options": "a<br>b", "answers": "0"},
		"true_false":    {"answer": "true"},
		"list":          {"items": "x<br>y"},
		"cloze_blanks":  {"text": "{{c1::x}}"},
		"short_answer":  {"reference": "r"},
	}
	tests := []struct {
		control string
		ordinal int
		want    string
	}{
		{"text", 0, `type="text"`},
		{"numeric", 0, `inputmode="decimal"`},
		{"choice_single", 0, `type="radio"`},
		{"choice_multi", 0, `type="checkbox"`},
		{"true_false", 0, `type="radio"`},
		{"list", 0, `class="answer-inputs"`},
		{"cloze_blanks", 1, `class="answer-inputs"`},
		{"short_answer", 0, `<textarea`},
	}
	for _, test := range tests {
		t.Run(test.control, func(t *testing.T) {
			fields := fieldsByControl[test.control]
			spec := specFor(test.control, fields)
			bindings, err := ResolveBindings(spec, fields)
			if err != nil {
				t.Fatalf("ResolveBindings: %v", err)
			}
			control, _ := Lookup(test.control)
			markup, err := control.RenderFront(RenderContext{
				Marker: "[[type:answer]]", Bindings: bindings, ClozeOrdinal: test.ordinal,
			})
			if err != nil {
				t.Fatalf("RenderFront: %v", err)
			}
			if !strings.Contains(markup, test.want) {
				t.Errorf("RenderFront = %q, want substring %q", markup, test.want)
			}
			if !strings.Contains(markup, `data-control="`+test.control+`"`) {
				t.Errorf("RenderFront = %q, want the data-control attribute", markup)
			}
		})
	}
}

// TestRenderSideSubstitutesThePlaceholder pins the substitution the caller
// performs: the control's markup stands where the type placeholder was.
func TestRenderSideSubstitutesThePlaceholder(t *testing.T) {
	fields := map[string]string{"answer": "Paris"}
	bindings, err := ResolveBindings(specFor("text", fields), fields)
	if err != nil {
		t.Fatalf("ResolveBindings: %v", err)
	}
	control, _ := Lookup("text")
	face := "<p>Capital?</p>[[type:answer]]"
	rendered, err := RenderSide(control, RenderContext{
		Marker: "[[type:answer]]", Side: notetype.SideFront, Bindings: bindings, ElementID: "a",
	}, face)
	if err != nil {
		t.Fatalf("RenderSide: %v", err)
	}
	if strings.Contains(rendered, "[[type:answer]]") || !strings.Contains(rendered, `<input id="text-a"`) {
		t.Errorf("RenderSide = %q, want the input in place of the marker", rendered)
	}
	if _, err := RenderSide(control, RenderContext{Marker: "[[type:other]]", Bindings: bindings}, face); !errors.Is(err, ErrPlaceholderMissing) {
		t.Fatalf("RenderSide without the marker = %v, want ErrPlaceholderMissing", err)
	}
}

// mustJSON encodes a string as JSON.
func mustJSON(text string) string {
	encoded, err := json.Marshal(text)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
