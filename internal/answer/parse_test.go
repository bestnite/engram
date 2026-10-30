package answer

import (
	"reflect"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/notetype"
)

// TestParseText pins the text parse: PlainText, trimmed.
func TestParseText(t *testing.T) {
	tests := []struct {
		name string
		html string
		want string
	}{
		{"plain", "  Paris  ", "Paris"},
		{"markup is stripped", "<b>Paris</b>", "Paris"},
		{"a line break becomes a space", "a<br>b", "a b"},
		{"entities are decoded", "a&amp;b", "a&b"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ParseText(test.html); got != test.want {
				t.Errorf("ParseText(%q) = %q, want %q", test.html, got, test.want)
			}
		})
	}
}

// TestParseLines pins the lines parse: <br>, <div>/<p> boundaries and literal
// newlines start a line, each line is plain text and trimmed, empty lines are
// dropped.
func TestParseLines(t *testing.T) {
	tests := []struct {
		name string
		html string
		want []string
	}{
		{"br", "one<br>two", []string{"one", "two"}},
		{"self-closing br", "one<br/>two<BR />three", []string{"one", "two", "three"}},
		{"div boundaries", "one<div>two</div><div>three</div>", []string{"one", "two", "three"}},
		{"paragraph boundaries", "<p>one</p><p>two</p>", []string{"one", "two"}},
		{"literal newlines", "one\ntwo\r\nthree", []string{"one", "two", "three"}},
		{"empty lines are dropped", "one<br><br> <br>two", []string{"one", "two"}},
		{"each line is trimmed", " one <br> two ", []string{"one", "two"}},
		{"inline markup stays on the line", "<b>one</b> two<br>three", []string{"one two", "three"}},
		{"empty input", "", []string{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ParseLines(test.html)
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("ParseLines(%q) = %q, want %q", test.html, got, test.want)
			}
		})
	}
}

// TestParseBool pins the bool parse and its negative cases.
func TestParseBool(t *testing.T) {
	tests := []struct {
		html    string
		want    bool
		wantErr bool
	}{
		{"true", true, false},
		{"false", false, false},
		{" TRUE ", true, false},
		{"False", false, false},
		{"", false, true},
		{"yes", false, true},
		{"1", false, true},
	}
	for _, test := range tests {
		t.Run(test.html, func(t *testing.T) {
			got, err := ParseBool(test.html)
			if test.wantErr {
				if err == nil {
					t.Fatalf("ParseBool(%q) = %v, want an error", test.html, got)
				}
				return
			}
			if err != nil || got != test.want {
				t.Errorf("ParseBool(%q) = %v, %v, want %v", test.html, got, err, test.want)
			}
		})
	}
}

// TestParseNumber pins the decimal parse and its negative cases.
func TestParseNumber(t *testing.T) {
	tests := []struct {
		html    string
		want    float64
		wantErr bool
	}{
		{"3.14", 3.14, false},
		{"  -2 ", -2, false},
		{"1e3", 1000, false},
		{"", 0, true},
		{"five", 0, true},
		{"NaN", 0, true},
		{"Inf", 0, true},
	}
	for _, test := range tests {
		t.Run(test.html, func(t *testing.T) {
			got, err := ParseNumber(test.html)
			if test.wantErr {
				if err == nil {
					t.Fatalf("ParseNumber(%q) = %v, want an error", test.html, got)
				}
				return
			}
			if err != nil || got != test.want {
				t.Errorf("ParseNumber(%q) = %v, %v, want %v", test.html, got, err, test.want)
			}
		})
	}
}

// TestValidateSpec covers the control lookup and the required-role rule.
func TestValidateSpec(t *testing.T) {
	tests := []struct {
		name   string
		spec   notetype.AnswerSpec
		want   string
		wantOK bool
	}{
		{
			name:   "valid",
			spec:   notetype.AnswerSpec{Control: "numeric", Bindings: map[string]string{"value": "f1"}},
			wantOK: true,
		},
		{
			name:   "optional roles may be omitted",
			spec:   notetype.AnswerSpec{Control: "short_answer"},
			wantOK: true,
		},
		{
			name: "missing required role",
			spec: notetype.AnswerSpec{Control: "numeric"},
			want: `missing required role "value"`,
		},
		{
			name: "required role bound to an empty field id",
			spec: notetype.AnswerSpec{Control: "choice_single", Bindings: map[string]string{"options": "f1", "answer": " "}},
			want: `missing required role "answer"`,
		},
		{
			name: "unknown control",
			spec: notetype.AnswerSpec{Control: "multiple_choice"},
			want: `unknown answer control "multiple_choice"`,
		},
		{
			name: "empty control",
			spec: notetype.AnswerSpec{},
			want: "answer spec has no control",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateSpec(test.spec)
			if test.wantOK {
				if err != nil {
					t.Fatalf("ValidateSpec() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateSpec() = %v, want substring %q", err, test.want)
			}
		})
	}
}

// TestResolveBindings covers the parse of bound fields and the missing-role
// error path.
func TestResolveBindings(t *testing.T) {
	spec := notetype.AnswerSpec{Control: "text", Bindings: map[string]string{
		"answer": "a", "accept": "b", "ignore_case": "c",
	}}
	fields := map[string]string{"a": "Paris", "b": "City of Paris<br>PARIS", "c": "false"}
	bindings, err := ResolveBindings(spec, fields)
	if err != nil {
		t.Fatalf("ResolveBindings: %v", err)
	}
	if got, _ := bindings.RequiredText("answer"); got != "Paris" {
		t.Errorf("answer = %q, want Paris", got)
	}
	if got := bindings.OptionalLines("accept"); !reflect.DeepEqual(got, []string{"City of Paris", "PARIS"}) {
		t.Errorf("accept = %q", got)
	}
	if got := bindings.OptionalBool("ignore_case", true); got {
		t.Error("ignore_case = true, want false")
	}

	if _, err := ResolveBindings(notetype.AnswerSpec{Control: "numeric"}, fields); err == nil {
		t.Fatal("ResolveBindings without value = nil error, want an error")
	}
	if _, err := ResolveBindings(notetype.AnswerSpec{Control: "true_false", Bindings: map[string]string{"answer": "bad"}},
		map[string]string{"bad": "yes"}); err == nil {
		t.Fatal("ResolveBindings with an unparseable bool = nil error, want an error")
	}
	if _, err := ResolveBindings(notetype.AnswerSpec{Control: "numeric", Bindings: map[string]string{"value": "bad"}},
		map[string]string{"bad": "five"}); err == nil {
		t.Fatal("ResolveBindings with an unparseable number = nil error, want an error")
	}

	// An optional role bound to a blank field is not bound: a note leaves an optional tolerance empty, and parsing
	// "" as a number would make the whole card ungradable.
	numeric := notetype.AnswerSpec{Control: "numeric", Bindings: map[string]string{
		"value": "v", "tolerance_absolute": "ta", "unit": "u",
	}}
	bindings, err = ResolveBindings(numeric, map[string]string{"v": "50", "ta": "", "u": ""})
	if err != nil {
		t.Fatalf("ResolveBindings with blank optional roles: %v", err)
	}
	if bindings.Bound("tolerance_absolute") || bindings.Bound("unit") {
		t.Errorf("blank optional roles resolved: %+v", bindings)
	}
	if got, _ := bindings.RequiredNumber("value"); got != 50 {
		t.Errorf("value = %v, want 50", got)
	}
}
