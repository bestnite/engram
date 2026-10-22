package cardtype

import (
	"encoding/json"
	"sort"
	"testing"
)

// allowedControls is the set of allowed values for FieldSpec.Control (corresponding one-to-one with the front-end edit controls).
var allowedControls = map[string]bool{
	ControlText:     true,
	ControlTextarea: true,
	ControlLines:    true,
	ControlNumber:   true,
	ControlBool:     true,
	ControlIndex:    true,
	ControlIndexes:  true,
}

// allowedAnswerControls is the set of allowed values for Description.AnswerControl.
var allowedAnswerControls = map[string]bool{
	AnswerNone:   true,
	AnswerText:   true,
	AnswerNumber: true,
	AnswerSingle: true,
	AnswerMulti:  true,
	AnswerBool:   true,
	AnswerBlanks: true,
	AnswerItems:  true,
	AnswerEssay:  true,
}

// expectedGraded is the per-kind hard fact: which card types implement Grader.
// It is deliberately written as literals rather than "recomputing the interface assertion", or the test would drift along with the assertion if the assertion were mistakenly deleted.
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

// expectedFieldKeys is the per-kind set of field keys (including the common optional fields source_url/extra).
// This is the contract that replaces the front end's card-fields.ts table: any silent drift must show up here.
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

// TestDescriptionsSelfConsistent asserts every registered card type's self-description is self-consistent:
// Kind matches the registration key, LabelKey is non-empty, Control/AnswerControl values are legal, and
// the front/back and prompt/option fields all appear in the field table.
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
			// A self-graded card type has no prompt field; an answer-based one must have a prompt.
			if d.AnswerControl == AnswerNone && d.PromptField != "" {
				t.Errorf("self-assess type has PromptField %q, want empty", d.PromptField)
			}
			// Per-blank answer card types are the exception: the raw prompt field already contains the answers, so the prompt can only be the server-side masked front.
			if d.AnswerControl != AnswerNone && d.AnswerControl != AnswerBlanks && d.PromptField == "" {
				t.Error("graded type has an empty PromptField")
			}
			// Only choice cards have option fields.
			if d.OptionsField != "" && d.AnswerControl != AnswerSingle && d.AnswerControl != AnswerMulti {
				t.Errorf("non-choice type has OptionsField %q", d.OptionsField)
			}
		})
	}
}

// TestDescriptionsGradedMatchesGrader asserts each kind's Graded matches the Grader interface assertion,
// and equals the hard fact table expectedGraded entry by entry.
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

// TestDescriptionsFieldKeysMatchContract asserts each kind's set of field keys equals the per-kind expectation table verbatim.
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

// TestDescriptionsOrderMatchesKinds asserts the enumeration is in lexicographic kind order and maps one-to-one onto Kinds().
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

// TestDescriptionExamplesAreValidNotes asserts every card type carries an example, and that the example is a genuinely usable note:
// after a JSON round trip (numbers from an agent are float64, arrays are []any) it still passes Validate and produces at least
// one card; the example uses only keys declared in the field table and supplies every required field. The example is meant for an agent to copy,
// and an example that fails validation is worse than no example at all.
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
