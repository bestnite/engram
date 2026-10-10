package cardtype

import (
	"encoding/json"
	"sort"
	"testing"
)

// allowedControls 是 FieldSpec.Control 的允许取值集合（与前端编辑控件一一对应）。
var allowedControls = map[string]bool{
	ControlText:     true,
	ControlTextarea: true,
	ControlLines:    true,
	ControlNumber:   true,
	ControlBool:     true,
	ControlIndex:    true,
	ControlIndexes:  true,
}

// allowedAnswerControls 是 Description.AnswerControl 的允许取值集合。
var allowedAnswerControls = map[string]bool{
	AnswerNone:   true,
	AnswerText:   true,
	AnswerNumber: true,
	AnswerSingle: true,
	AnswerMulti:  true,
	AnswerBool:   true,
	AnswerBlanks: true,
	AnswerItems:  true,
}

// expectedGraded 是逐 kind 的硬事实：哪些题型实现 Grader。
// 故意写成字面量而不是「重算一遍接口断言」，否则断言被误删时测试会跟着一起漂移。
var expectedGraded = map[string]bool{
	"basic":         false,
	"basic_both":    false,
	"cloze":         true,
	"list":          true,
	"typed":         true,
	"numeric":       true,
	"choice_single": true,
	"choice_multi":  true,
	"true_false":    true,
	"short_answer":  false,
}

// expectedFieldKeys 是逐 kind 的字段键集合（含通用可选字段 source_url/extra）。
// 这是取代前端 card-fields.ts 那张表的契约：任何静默漂移都必须在这里显形。
var expectedFieldKeys = map[string][]string{
	"basic":         {"front", "back", "source_url", "extra"},
	"basic_both":    {"front", "back", "source_url", "extra"},
	"cloze":         {"text", "source_url", "extra"},
	"list":          {"prompt", "items", "ordered", "source_url", "extra"},
	"typed":         {"prompt", "answer", "accept", "ignore_case", "ignore_whitespace", "source_url", "extra"},
	"numeric":       {"prompt", "value", "unit", "tolerance_absolute", "tolerance_relative", "source_url", "extra"},
	"choice_single": {"question", "options", "answer", "source_url", "extra"},
	"choice_multi":  {"question", "options", "answers", "source_url", "extra"},
	"true_false":    {"statement", "answer", "source_url", "extra"},
	"short_answer":  {"prompt", "reference", "source_url", "extra"},
}

// TestDescriptionsSelfConsistent 断言每个已注册题型的自描述自洽：
// Kind 与注册键一致、LabelKey 非空、Control/AnswerControl 取值合法、
// 正反面与题面/选项字段都出现在字段表里。
func TestDescriptionsSelfConsistent(t *testing.T) {
	kinds := Kinds()
	if len(kinds) == 0 {
		t.Fatal("Kinds() is empty; the registry was not loaded")
	}
	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			typ, ok := Lookup(kind)
			if !ok {
				t.Fatalf("Lookup(%q) not found", kind)
			}
			d := typ.Describe()
			if d.Kind != kind {
				t.Errorf("Describe().Kind = %q, want the registered kind %q", d.Kind, kind)
			}
			if d.LabelKey == "" {
				t.Error("Describe().LabelKey is empty")
			}
			if !allowedAnswerControls[d.AnswerControl] {
				t.Errorf("AnswerControl = %q, not in the allowed set", d.AnswerControl)
			}
			if d.FrontField == "" {
				t.Error("FrontField is empty")
			}
			if d.BackField == "" {
				t.Error("BackField is empty")
			}
			keys := make(map[string]bool, len(d.Fields))
			for _, f := range d.Fields {
				if f.Key == "" {
					t.Error("a field has an empty key")
				}
				if keys[f.Key] {
					t.Errorf("field %q is declared twice", f.Key)
				}
				keys[f.Key] = true
				if !allowedControls[f.Control] {
					t.Errorf("field %q control = %q, not in the allowed set", f.Key, f.Control)
				}
			}
			for _, ref := range []struct{ name, key string }{
				{"FrontField", d.FrontField},
				{"BackField", d.BackField},
				{"PromptField", d.PromptField},
				{"OptionsField", d.OptionsField},
			} {
				if ref.key == "" {
					continue
				}
				if !keys[ref.key] {
					t.Errorf("%s = %q is not among the declared fields", ref.name, ref.key)
				}
			}
			// 自评题型没有题面字段，作答类必须有题面。
			if d.AnswerControl == AnswerNone && d.PromptField != "" {
				t.Errorf("self-assess type has PromptField %q, want empty", d.PromptField)
			}
			// 逐空作答题例外：题面字段原文里就有答案，题面只能用服务端掩盖后的正面。
			if d.AnswerControl != AnswerNone && d.AnswerControl != AnswerBlanks && d.PromptField == "" {
				t.Error("graded type has an empty PromptField")
			}
			// 只有选择题有选项字段。
			if d.OptionsField != "" && d.AnswerControl != AnswerSingle && d.AnswerControl != AnswerMulti {
				t.Errorf("non-choice type has OptionsField %q", d.OptionsField)
			}
		})
	}
}

// TestDescriptionsGradedMatchesGrader 断言每个 kind 的 Graded 与 Grader 接口断言一致，
// 并与硬事实表 expectedGraded 逐条相等。
func TestDescriptionsGradedMatchesGrader(t *testing.T) {
	descByKind := make(map[string]Description)
	for _, d := range Descriptions() {
		descByKind[d.Kind] = d
	}
	for _, kind := range Kinds() {
		d, ok := descByKind[kind]
		if !ok {
			t.Fatalf("Descriptions() is missing kind %q", kind)
		}
		want, ok := expectedGraded[kind]
		if !ok {
			t.Fatalf("kind %q has no entry in expectedGraded; add the hard fact", kind)
		}
		if d.Graded != want {
			t.Errorf("kind %q Graded = %v, want %v", kind, d.Graded, want)
		}
	}
}

// TestDescriptionsFieldKeysMatchContract 断言每个 kind 的字段键集合与逐 kind 期望表逐字相等。
func TestDescriptionsFieldKeysMatchContract(t *testing.T) {
	for _, kind := range Kinds() {
		t.Run(kind, func(t *testing.T) {
			typ, _ := Lookup(kind)
			got := make([]string, 0, len(typ.Describe().Fields))
			for _, f := range typ.Describe().Fields {
				got = append(got, f.Key)
			}
			want, ok := expectedFieldKeys[kind]
			if !ok {
				t.Fatalf("kind %q has no entry in expectedFieldKeys; add the contract", kind)
			}
			sort.Strings(got)
			sortedWant := append([]string(nil), want...)
			sort.Strings(sortedWant)
			if len(got) != len(sortedWant) {
				t.Fatalf("field keys = %v, want %v", got, sortedWant)
			}
			for i := range sortedWant {
				if got[i] != sortedWant[i] {
					t.Fatalf("field keys = %v, want %v", got, sortedWant)
				}
			}
		})
	}
}

// TestDescriptionsOrderMatchesKinds 断言枚举结果按 kind 字典序且与 Kinds() 一一对应。
func TestDescriptionsOrderMatchesKinds(t *testing.T) {
	ds := Descriptions()
	kinds := Kinds()
	if len(ds) != len(kinds) {
		t.Fatalf("Descriptions() returned %d types, want %d", len(ds), len(kinds))
	}
	for i := range kinds {
		if ds[i].Kind != kinds[i] {
			t.Errorf("Descriptions()[%d].Kind = %q, want %q", i, ds[i].Kind, kinds[i])
		}
	}
}

// TestDescriptionExamplesAreValidNotes 断言每个题型都带示例，且示例是一条真能用的 note：
// 经 JSON 往返（agent 送来的数字是 float64、数组是 []any）后仍通过 Validate、至少产出
// 一张卡；示例只用字段表里声明过的键，并给齐全部必填字段。示例是写给 agent 照抄的，
// 一条过不了校验的示例比没有示例更糟。
func TestDescriptionExamplesAreValidNotes(t *testing.T) {
	for _, d := range Descriptions() {
		t.Run(d.Kind, func(t *testing.T) {
			if len(d.Example) == 0 {
				t.Fatal("Describe().Example is empty")
			}
			declared := make(map[string]bool, len(d.Fields))
			for _, f := range d.Fields {
				declared[f.Key] = true
				if _, ok := d.Example[f.Key]; f.Required && !ok {
					t.Errorf("example lacks required field %q", f.Key)
				}
			}
			for key := range d.Example {
				if !declared[key] {
					t.Errorf("example uses field %q that the field table does not declare", key)
				}
			}
			raw, err := json.Marshal(d.Example)
			if err != nil {
				t.Fatalf("marshal example: %v", err)
			}
			var fields map[string]any
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatalf("unmarshal example: %v", err)
			}
			typ, _ := Lookup(d.Kind)
			if err := typ.Validate(fields); err != nil {
				t.Fatalf("example fails Validate: %v", err)
			}
			if cards := typ.Cards(Note{Kind: d.Kind, Fields: fields}); len(cards) == 0 {
				t.Fatal("example produces no cards")
			}
		})
	}
}
