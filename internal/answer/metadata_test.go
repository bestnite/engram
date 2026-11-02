package answer

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/notetype"
)

func TestClozeAlternativesTolerant(t *testing.T) {
	for _, tc := range []struct {
		raw       string
		number    int
		canonical string
		want      []string
	}{
		{"", 1, "a", nil}, {`{`, 1, "a", nil}, {`[]`, 1, "a", nil},
		{`{"cloze_accept":false}`, 1, "a", nil},
		{`{"cloze_accept":{"1":false,"7":{"a":["ok"]}}}`, 7, "a", []string{"ok"}},
		{`{"future":{"x":1},"cloze_accept":{"1":{"bad":["x",1],"a":["yes","a|b"]}}}`, 1, "a", []string{"yes", "a|b"}},
		{`{"cloze_accept":{"1":{"bad":["x",1],"a":["yes"]}}}`, 1, "bad", nil},
		{`{"cloze_accept":{"1":{"a":[null]}}}`, 1, "a", nil},
		{`{"cloze_accept":{"1":{"A":["yes"]}}}`, 1, "a", nil},
	} {
		if got := ClozeAlternatives(tc.raw, tc.number, tc.canonical); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("ClozeAlternatives(%q, %d, %q) = %#v, want %#v", tc.raw, tc.number, tc.canonical, got, tc.want)
		}
	}
}

func TestClozeMetadataAssociationWithExistingExtraction(t *testing.T) {
	control := clozeBlanksControl{}
	const meta = `{"future":{"bad":[null]},"cloze_accept":{"1":{"alpha":["a|b,\"c\""],"beta":["second"],"orphan":["unused"]},"2":{"alpha":["other"]},"7":false}}`
	for _, tc := range []struct {
		text    string
		number  int
		answers string
		score   float64
	}{
		{`{{c1::alpha::hint}} {{c1::beta}}`, 1, `["a|b,\"c\"","second"]`, 1},
		{`{{c1::beta}} {{c1::new}} {{c1::alpha}}`, 1, `["second","new","a|b,\"c\""]`, 1},
		{`{{c1::Alpha}}`, 1, `["a|b,\"c\""]`, 0},
		{`{{c1:: alpha }}`, 1, `["a|b,\"c\""]`, 0},
		{`{{c1::changed}}`, 1, `["unused"]`, 0},
		{`{{c1,2::alpha::hint}}`, 1, `["a|b,\"c\""]`, 1},
		{`{{c1,2::alpha::hint}}`, 2, `["other"]`, 1},
		{`{{c1::outer {{c2::inner}}}}`, 2, `["inner"]`, 1},
	} {
		result, err := control.Grade(GradeContext{Bindings: Bindings{"text": {Value: tc.text}}, Metadata: meta, ClozeOrdinal: tc.number, Answer: json.RawMessage(tc.answers)})
		if err != nil || result.Score != tc.score {
			t.Errorf("%s number %d = %+v, %v; want %v", tc.text, tc.number, result, err, tc.score)
		}
	}
	if got := ClozeAlternatives(`<div>{&quot;cloze_accept&quot;:{}}</div>`, 1, "alpha"); got != nil {
		t.Fatalf("HTML was repaired: %v", got)
	}
}

func TestMetadataFieldValue(t *testing.T) {
	cfg := notetype.Config{Fields: []notetype.Field{{ID: "a", Name: "engram_notes"}, {ID: "b", Name: "engram_meta"}}}
	if got := MetadataFieldValue(cfg, map[string]string{"a": "wrong", "b": "right", "engram_meta": "wrong"}); got != "right" {
		t.Fatalf("got %q", got)
	}
}

func TestClozeMetadataGradeAndRender(t *testing.T) {
	bindings := Bindings{"text": {Value: `{{c1::is::hint}} {{c1::are}} {{c1::is}} {{c7::is}}`}}
	meta := `{"cloze_accept":{"1":{"is":["was","\\(x+1\\)"],"are":["were"]},"7":{"is":["seven"]}}}`
	control := clozeBlanksControl{}
	for _, tc := range []struct {
		answers string
		want    float64
	}{
		{`["is","are","is"]`, 1}, {`[" WAS ","were","was"]`, 1},
		{`["x+1","were","was"]`, 1}, {`["seven","were","was"]`, 2.0 / 3},
		{`["","were","was"]`, 2.0 / 3},
	} {
		result, err := control.Grade(GradeContext{Bindings: bindings, Metadata: meta, ClozeOrdinal: 1, Answer: json.RawMessage(tc.answers)})
		if err != nil || result.Score != tc.want {
			t.Errorf("Grade(%s) = %#v, %v; want %v", tc.answers, result, err, tc.want)
		}
	}
	result, err := control.Grade(GradeContext{Bindings: bindings, Metadata: meta, ClozeOrdinal: 1, Answer: json.RawMessage(`["was","were","was"]`)})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Detail["canonical"], []string{"is", "are", "is"}) || !reflect.DeepEqual(result.Detail["alternatives"], [][]string{{"was", `\(x+1\)`}, {"were"}, {"was", `\(x+1\)`}}) {
		t.Fatalf("unexpected detail: %#v", result.Detail)
	}
	ctx := RenderContext{Bindings: bindings, Metadata: meta, ClozeOrdinal: 1}
	front, err := control.RenderFront(ctx)
	if err != nil || strings.Contains(front, "was") {
		t.Fatalf("front %q: %v", front, err)
	}
	back, err := control.RenderBack(ctx)
	if err != nil || strings.Contains(back, "was") {
		t.Fatalf("ungraded back %q: %v", back, err)
	}
	ctx.Result = &result
	back, err = control.RenderBack(ctx)
	if err != nil || !strings.Contains(back, "was") || !strings.Contains(back, "were") {
		t.Fatalf("graded back %q: %v", back, err)
	}
}
