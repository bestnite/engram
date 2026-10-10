package cardtype

import (
	"strings"
	"testing"
)

// 验收：容差测试覆盖大小写、空白、多答案、数值的绝对/相对容差、部分得分映射到 Hard。

func boolPtr(b bool) *bool { return &b }
func mapPtr(m GradeMapping) *GradeMapping {
	return &m
}

// TestGradedTypesRegistered 断言五种作答类题型都已注册且实现了 Grader。
func TestGradedTypesRegistered(t *testing.T) {
	for _, kind := range []string{"typed", "numeric", "choice_single", "choice_multi", "true_false"} {
		typ, ok := Lookup(kind)
		if !ok {
			t.Fatalf("kind %q not registered", kind)
		}
		if _, ok := typ.(Grader); !ok {
			t.Errorf("kind %q does not implement Grader", kind)
		}
	}
}

// TestTypedGrading 覆盖大小写、空白、多答案与正则。
func TestTypedGrading(t *testing.T) {
	base := map[string]any{"prompt": "Capital of France?", "answer": "Paris"}
	cases := []struct {
		name   string
		fields map[string]any
		answer string
		want   int
		wantOk bool
	}{
		{"exact", base, "Paris", RatingGood, true},
		{"case ignored by default", base, "paris", RatingGood, true},
		{"whitespace collapsed by default", base, "  Paris  ", RatingGood, true},
		{"inner whitespace collapsed", map[string]any{"prompt": "p", "answer": "New York"}, "New   York", RatingGood, true},
		{"wrong answer is Again", base, "London", RatingAgain, true},
		{
			"case sensitive when ignore_case=false",
			map[string]any{"prompt": "p", "answer": "Paris", "ignore_case": false},
			"paris", RatingAgain, true,
		},
		{
			"whitespace significant when ignore_whitespace=false",
			map[string]any{"prompt": "p", "answer": "Paris", "ignore_whitespace": false},
			" Paris", RatingAgain, true,
		},
		{
			"accept list alternative",
			map[string]any{"prompt": "p", "answer": "Paris", "accept": []any{"City of Paris", "PARIS"}},
			"City of Paris", RatingGood, true,
		},
		{
			"accept list second entry",
			map[string]any{"prompt": "p", "answer": "Paris", "accept": []any{"City of Paris"}},
			"CITY OF PARIS", RatingGood, true,
		},
		{
			"regex takes precedence over literal",
			map[string]any{"prompt": "p", "answer": "unused", "regex": `\d+/\d+`},
			"3/4", RatingGood, true,
		},
		{
			"regex full match required",
			map[string]any{"prompt": "p", "answer": "unused", "regex": `\d+`},
			"12a", RatingAgain, true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rating, detail, ok := typedType{}.Grade(TypedInput{GradeContext: GradeContext{Fields: tc.fields}, Answer: tc.answer})
			if ok != tc.wantOk {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOk)
			}
			if rating != tc.want {
				t.Fatalf("rating = %d, want %d (detail %v)", rating, tc.want, detail)
			}
			if _, has := detail["score"]; tc.wantOk && !has {
				t.Errorf("detail is missing score: %v", detail)
			}
		})
	}
}

// TestNumericGrading 覆盖绝对值/相对容差与单位。
func TestNumericGrading(t *testing.T) {
	cases := []struct {
		name   string
		fields map[string]any
		answer string
		want   int
		wantOk bool
	}{
		{"exact", map[string]any{"prompt": "p", "value": 50}, "50", RatingGood, true},
		{
			"within absolute tolerance",
			map[string]any{"prompt": "p", "value": 100, "tolerance_absolute": 2},
			"101.5", RatingGood, true,
		},
		{
			"outside absolute tolerance",
			map[string]any{"prompt": "p", "value": 100, "tolerance_absolute": 2},
			"103", RatingAgain, true,
		},
		{
			"within relative tolerance",
			map[string]any{"prompt": "p", "value": 200, "tolerance_relative": 0.01},
			"201", RatingGood, true,
		},
		{
			"outside relative tolerance",
			map[string]any{"prompt": "p", "value": 200, "tolerance_relative": 0.01},
			"205", RatingAgain, true,
		},
		{
			"unit suffix is stripped",
			map[string]any{"prompt": "p", "value": 5, "unit": "kg", "tolerance_absolute": 0.1},
			"5 kg", RatingGood, true,
		},
		{
			"non-numeric input is wrong",
			map[string]any{"prompt": "p", "value": 5},
			"five", RatingAgain, true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rating, detail, ok := numericType{}.Grade(NumericInput{GradeContext: GradeContext{Fields: tc.fields}, Answer: tc.answer})
			if ok != tc.wantOk {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOk)
			}
			if rating != tc.want {
				t.Fatalf("rating = %d, want %d (detail %v)", rating, tc.want, detail)
			}
		})
	}
}

// TestChoiceSingleGrading 覆盖对/错与越界。
func TestChoiceSingleGrading(t *testing.T) {
	fields := map[string]any{"question": "q", "options": []any{"a", "b", "c"}, "answer": 2}
	if rating, _, ok := (choiceSingleType{}).Grade(ChoiceSingleInput{GradeContext: GradeContext{Fields: fields}, Selected: 2}); !ok || rating != RatingGood {
		t.Fatalf("correct selection = (%d, %v), want (%d, true)", rating, ok, RatingGood)
	}
	if rating, _, ok := (choiceSingleType{}).Grade(ChoiceSingleInput{GradeContext: GradeContext{Fields: fields}, Selected: 0}); !ok || rating != RatingAgain {
		t.Fatalf("wrong selection = (%d, %v), want (%d, true)", rating, ok, RatingAgain)
	}
	if _, _, ok := (choiceSingleType{}).Grade(ChoiceSingleInput{GradeContext: GradeContext{Fields: fields}, Selected: 9}); ok {
		t.Fatal("out-of-range selection must not be gradable")
	}
}

// TestChoiceMultiGrading 覆盖全对、部分得分映射到 Hard、全错与误选。
func TestChoiceMultiGrading(t *testing.T) {
	fields := map[string]any{"question": "q", "options": []any{"a", "b", "c"}, "answers": []any{0, 2}}
	cases := []struct {
		name     string
		selected []int
		want     int
	}{
		{"all correct is Good", []int{0, 2}, RatingGood},
		{"partial credit maps to Hard", []int{0}, RatingHard},
		{"partial with one wrong extra still Hard", []int{0, 1, 2}, RatingHard},
		{"all wrong is Again", []int{1}, RatingAgain},
		{"empty selection is Again", []int{}, RatingAgain},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rating, detail, ok := choiceMultiType{}.Grade(ChoiceMultiInput{GradeContext: GradeContext{Fields: fields}, Selected: tc.selected})
			if !ok {
				t.Fatalf("ok = false, want true")
			}
			if rating != tc.want {
				t.Fatalf("rating = %d, want %d (detail %v)", rating, tc.want, detail)
			}
		})
	}
}

// TestTrueFalseGrading 覆盖对/错与未作答。
func TestTrueFalseGrading(t *testing.T) {
	fields := map[string]any{"statement": "s", "answer": true}
	if rating, _, ok := (trueFalseType{}).Grade(TrueFalseInput{GradeContext: GradeContext{Fields: fields}, Answer: boolPtr(true)}); !ok || rating != RatingGood {
		t.Fatalf("correct = (%d, %v), want (%d, true)", rating, ok, RatingGood)
	}
	if rating, _, ok := (trueFalseType{}).Grade(TrueFalseInput{GradeContext: GradeContext{Fields: fields}, Answer: boolPtr(false)}); !ok || rating != RatingAgain {
		t.Fatalf("wrong = (%d, %v), want (%d, true)", rating, ok, RatingAgain)
	}
	if _, _, ok := (trueFalseType{}).Grade(TrueFalseInput{GradeContext: GradeContext{Fields: fields}}); ok {
		t.Fatal("unanswered must not be gradable")
	}
}

// TestGradeMappingDefaults 断言默认映射：全对 Good、部分 Hard、全错 Again。
func TestGradeMappingDefaults(t *testing.T) {
	m := DefaultGradeMapping()
	cases := []struct {
		score float64
		want  int
	}{
		{1, RatingGood},
		{0.5, RatingHard},
		{0.01, RatingHard},
		{0, RatingAgain},
	}
	for _, tc := range cases {
		if got := m.RatingFor(tc.score); got != tc.want {
			t.Errorf("RatingFor(%v) = %d, want %d", tc.score, got, tc.want)
		}
	}
}

// TestGradeMappingCustom 断言存在的映射可覆盖默认档位与阈值，并且判分器会使用它。
func TestGradeMappingCustom(t *testing.T) {
	custom := GradeMapping{Version: GradeMappingVersion, Full: RatingEasy, Partial: RatingAgain, None: RatingAgain}
	if got := custom.RatingFor(1); got != RatingEasy {
		t.Errorf("custom full = %d, want %d", got, RatingEasy)
	}
	if got := custom.RatingFor(0.5); got != RatingAgain {
		t.Errorf("custom partial = %d, want %d", got, RatingAgain)
	}

	// 判分器传入自定义映射后必须生效：部分得分降到 Again。
	fields := map[string]any{"question": "q", "options": []any{"a", "b", "c"}, "answers": []any{0, 2}}
	rating, _, ok := choiceMultiType{}.Grade(ChoiceMultiInput{
		GradeContext: GradeContext{Fields: fields, Mapping: mapPtr(custom)},
		Selected:     []int{0},
	})
	if !ok || rating != RatingAgain {
		t.Fatalf("custom mapping not applied: rating = %d, ok = %v, want %d", rating, ok, RatingAgain)
	}
}

// TestParseGradeMapping 覆盖解析、缺省回退、非法输入与往返。
func TestParseGradeMapping(t *testing.T) {
	if m, err := ParseGradeMapping(""); err != nil || m.RatingFor(0.5) != RatingHard {
		t.Fatalf("empty mapping should fall back to default, got %+v err %v", m, err)
	}
	raw := `{"version":1,"full":4,"partial":2,"none":1}`
	if m, err := ParseGradeMapping(raw); err != nil || m.RatingFor(1) != RatingEasy {
		t.Fatalf("parse custom = %+v, err %v", m, err)
	}
	if _, err := ParseGradeMapping(`{"version":9}`); err == nil {
		t.Fatal("unsupported version must be rejected")
	}
	if _, err := ParseGradeMapping(`{"full":7}`); err == nil {
		t.Fatal("out-of-range rating must be rejected")
	}
	if _, err := ParseGradeMapping(`{not json}`); err == nil {
		t.Fatal("invalid JSON must be rejected")
	}
	m, err := MarshalGradeMapping(GradeMapping{Full: RatingEasy})
	if err != nil {
		t.Fatalf("MarshalGradeMapping: %v", err)
	}
	back, err := ParseGradeMapping(m)
	if err != nil || back.RatingFor(1) != RatingEasy {
		t.Fatalf("round-trip = %+v, err %v", back, err)
	}
}

// TestGradedValidateErrors 覆盖字段缺失、越界索引、重复项等负例。
func TestGradedValidateErrors(t *testing.T) {
	cases := []struct {
		name   string
		kind   string
		fields map[string]any
		want   string
	}{
		{"typed missing answer", "typed", map[string]any{"prompt": "p"}, `missing required field "answer"`},
		{"typed bad regex", "typed", map[string]any{"prompt": "p", "answer": "a", "regex": "("}, "not a valid regular expression"},
		{"numeric missing value", "numeric", map[string]any{"prompt": "p"}, `missing required field "value"`},
		{"numeric negative tolerance", "numeric", map[string]any{"prompt": "p", "value": 1, "tolerance_absolute": -1}, "must not be negative"},
		{"numeric relative over 1", "numeric", map[string]any{"prompt": "p", "value": 1, "tolerance_relative": 1.5}, "between 0 and 1"},
		{"choice_single too few options", "choice_single", map[string]any{"question": "q", "options": []any{"a"}, "answer": 0}, "at least 2 options"},
		{"choice_single duplicate options", "choice_single", map[string]any{"question": "q", "options": []any{"a", "a"}, "answer": 0}, "must not contain duplicates"},
		{"choice_single answer out of range", "choice_single", map[string]any{"question": "q", "options": []any{"a", "b"}, "answer": 2}, "out of range"},
		{"choice_multi duplicate answers", "choice_multi", map[string]any{"question": "q", "options": []any{"a", "b"}, "answers": []any{0, 0}}, "must not contain duplicates"},
		{"choice_multi answer out of range", "choice_multi", map[string]any{"question": "q", "options": []any{"a", "b"}, "answers": []any{0, 5}}, "out of range"},
		{"true_false non-bool answer", "true_false", map[string]any{"statement": "s", "answer": "yes"}, `field "answer" must be a boolean`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.kind, tc.fields)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate = %v, want substring %q", err, tc.want)
			}
		})
	}
}

// TestGradedValidateAndRenderHappyPath 断言合法字段能通过并产出卡片与两侧内容。
func TestGradedValidateAndRenderHappyPath(t *testing.T) {
	cases := []struct {
		kind   string
		fields map[string]any
	}{
		{"typed", map[string]any{"prompt": "p", "answer": "a"}},
		{"numeric", map[string]any{"prompt": "p", "value": 1.5, "unit": "kg"}},
		{"choice_single", map[string]any{"question": "q", "options": []any{"a", "b"}, "answer": 1}},
		{"choice_multi", map[string]any{"question": "q", "options": []any{"a", "b"}, "answers": []any{0, 1}}},
		{"true_false", map[string]any{"statement": "s", "answer": false}},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			if err := Validate(tc.kind, tc.fields); err != nil {
				t.Fatalf("Validate: %v", err)
			}
			cards, err := Cards(Note{Kind: tc.kind, Fields: tc.fields})
			if err != nil || len(cards) != 1 || cards[0].Template != "forward" {
				t.Fatalf("Cards = %+v, err %v", cards, err)
			}
			typ, _ := Lookup(tc.kind)
			if _, err := typ.Render(cards[0], SideFront); err != nil {
				t.Errorf("Render front: %v", err)
			}
			if _, err := typ.Render(cards[0], SideBack); err != nil {
				t.Errorf("Render back: %v", err)
			}
		})
	}
}

// TestTypedBackJoinsAcceptedAnswers 断言输入作答卡的背面把标准答案和其它可接受答案写在同一行，
// 用「 / 」分隔；没有其它可接受答案时只有标准答案。
func TestTypedBackJoinsAcceptedAnswers(t *testing.T) {
	cases := []struct {
		name   string
		fields map[string]any
		want   string
	}{
		{"with accepted answers", map[string]any{"prompt": "capital?", "answer": "Paris", "accept": []any{"巴黎", "paris city"}}, "Paris / 巴黎 / paris city"},
		{"answer only", map[string]any{"prompt": "capital?", "answer": "Paris"}, "Paris"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			back, err := typedType{}.Render(Card{Template: "forward", Fields: tc.fields}, SideBack)
			if err != nil {
				t.Fatalf("Render back: %v", err)
			}
			if back.Body != tc.want || len(back.Extra) != 0 {
				t.Errorf("back = %+v, want body %q and no extra", back, tc.want)
			}
		})
	}
}
