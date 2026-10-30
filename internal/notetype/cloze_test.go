package notetype

import (
	"reflect"
	"strings"
	"testing"
)

// The cases below are the tests from Anki's rslib/src/cloze.rs at tag 26.09.3,
// ported with the code, plus one check per fixture rule that only the golden
// records.

func TestClozeNumbersInString(t *testing.T) {
	tests := []struct {
		text string
		want []int
	}{
		{"test", nil},
		{"{{c2::te}}{{c1::s}}t{{", []int{1, 2}},
		{"{{c0::te}}s{{c2::t}}s", []int{2}},
		{"{{c1,1,2::test}}", []int{1, 2}},
		{"{{c0,1,2::test}}", []int{1, 2}},
		{"{{c1,,3::test}}", []int{1, 3}},
		{"{{c1,2::outer {{c3::inner}}}}", []int{1, 2, 3}},
		{"{{c2::te{{c1::s}}}}t{{", []int{1, 2}},
		{"{{c1::outer {{c1::inner}}}}", []int{1}},
		{"öaöaöööaö", nil},
	}
	for _, test := range tests {
		numbers := clozeNumbersIn(test.text)
		var got []int
		for ordinal := 0; ordinal <= 3; ordinal++ {
			if numbers[ordinal] {
				got = append(got, ordinal)
			}
		}
		if !reflect.DeepEqual(got, test.want) {
			t.Errorf("clozeNumbersIn(%q) = %v, want %v", test.text, got, test.want)
		}
	}
}

func TestClozeNumbersInFields(t *testing.T) {
	tests := []struct {
		name   string
		fields []string
		want   []int
	}{
		{
			name:   "no fields",
			fields: nil,
		},
		{
			name:   "no clozes",
			fields: []string{"plain", ""},
		},
		{
			name:   "one number in several markers",
			fields: []string{"{{c1::one}} {{c1::again}}", ""},
			want:   []int{1},
		},
		{
			name:   "numbers spread over fields, sorted",
			fields: []string{"{{c2::two}}", "{{c5::five}} {{c1::one}}"},
			want:   []int{1, 2, 5},
		},
		{
			name:   "zero is not a card number",
			fields: []string{"{{c0::placeholder}}"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := clozeNumbersInFields(test.fields)
			if !reflect.DeepEqual(got, test.want) && !(len(got) == 0 && len(test.want) == 0) {
				t.Errorf("clozeNumbersInFields(%q) = %v, want %v", test.fields, got, test.want)
			}
		})
	}
}

func TestRevealClozeText(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		ordinal  int
		question bool
		want     string
	}{
		{
			name:     "question hides the active deletion",
			text:     "{{c1::one}} {{c2::two::hint}}",
			ordinal:  1,
			question: true,
			want:     `<span class="cloze" data-cloze="one" data-ordinal="1">[...]</span> <span class="cloze-inactive" data-ordinal="2">two</span>`,
		},
		{
			name:    "answer reveals the active deletion",
			text:    "{{c1::one}} {{c2::two::hint}}",
			ordinal: 1,
			want:    `<span class="cloze" data-ordinal="1">one</span> <span class="cloze-inactive" data-ordinal="2">two</span>`,
		},
		{
			name:     "question shows the hint",
			text:     "{{c1::one}} {{c2::two::hint}}",
			ordinal:  2,
			question: true,
			want:     `<span class="cloze-inactive" data-ordinal="1">one</span> <span class="cloze" data-cloze="two" data-ordinal="2">[hint]</span>`,
		},
		{
			name:     "missing number renders nothing",
			text:     "{{c1::one}}",
			ordinal:  2,
			question: true,
			want:     "",
		},
		{
			name:    "text without clozes renders nothing",
			text:    "plain",
			ordinal: 1,
			want:    "",
		},
		{
			name:     "one marker, several numbers",
			text:     "{{c1,2,3::multi}}",
			ordinal:  2,
			question: true,
			want:     `<span class="cloze" data-cloze="multi" data-ordinal="1,2,3">[...]</span>`,
		},
		{
			name:     "hint of a multi-number marker",
			text:     "{{c1,2::answer::hint}}",
			ordinal:  2,
			question: true,
			want:     `<span class="cloze" data-cloze="answer" data-ordinal="1,2">[hint]</span>`,
		},
		{
			name:    "multi-number marker on the answer",
			text:    "{{c1,2::answer::hint}}",
			ordinal: 1,
			want:    `<span class="cloze" data-ordinal="1,2">answer</span>`,
		},
		{
			name:     "nested cloze hides only its own text",
			text:     "foo {{c1::bar {{c2::baz}}}}",
			ordinal:  1,
			question: true,
			want: "foo <span class=\"cloze\" data-cloze=\"" +
				encodeAttribute(`bar <span class="cloze-inactive" data-ordinal="2">baz</span>`) +
				"\" data-ordinal=\"1\">[...]</span>",
		},
		{
			name:    "nested cloze on the answer",
			text:    "foo {{c1::bar {{c2::baz}}}}",
			ordinal: 1,
			want:    `foo <span class="cloze" data-ordinal="1">bar <span class="cloze-inactive" data-ordinal="2">baz</span></span>`,
		},
		{
			name:     "nested cloze is its own card",
			text:     "foo {{c1::bar {{c2::baz}}}}",
			ordinal:  2,
			question: true,
			want:     `foo <span class="cloze-inactive" data-ordinal="1">bar <span class="cloze" data-cloze="baz" data-ordinal="2">[...]</span></span>`,
		},
		{
			name:     "nested cloze keeps the outer hint",
			text:     "foo {{c1::bar {{c2::baz}}::qux}}",
			ordinal:  1,
			question: true,
			want: "foo <span class=\"cloze\" data-cloze=\"" +
				encodeAttribute(`bar <span class="cloze-inactive" data-ordinal="2">baz</span>`) +
				"\" data-ordinal=\"1\">[qux]</span>",
		},
		{
			name:    "nested cloze answer keeps both texts",
			text:    "foo {{c1::bar {{c2::baz}}::qux}}",
			ordinal: 1,
			want:    `foo <span class="cloze" data-ordinal="1">bar <span class="cloze-inactive" data-ordinal="2">baz</span></span>`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := revealClozeText(test.text, test.ordinal, test.question)
			if got != test.want {
				t.Errorf("revealClozeText() = %q, want %q", got, test.want)
			}
		})
	}

	t.Run("same number nested in itself", func(t *testing.T) {
		inner := `<span class="cloze" data-cloze="inner" data-ordinal="1">[...]</span>`
		want := `<span class="cloze" data-cloze="` + encodeAttribute("outer "+inner) + `" data-ordinal="1">[...]</span>`
		if got := revealClozeText("{{c1::outer {{c1::inner}}}}", 1, true); got != want {
			t.Errorf("revealClozeText() = %q, want %q", got, want)
		}
	})

	t.Run("plain text of nested clozes", func(t *testing.T) {
		tests := []struct {
			text     string
			ordinal  int
			question bool
			want     string
		}{
			{"foo {{c1::bar {{c2::baz}}}}", 1, true, "foo [...]"},
			{"foo {{c1::bar {{c2::baz}}}}", 1, false, "foo bar baz"},
			{"foo {{c1::bar {{c2::baz}}::qux}}", 2, true, "foo bar [...]"},
			{"foo {{c1::bar {{c2::baz}}::qux}}", 2, false, "foo bar baz"},
			{"foo {{c1::bar {{c2::baz}}::qux}}", 1, true, "foo [qux]"},
			{"foo {{c1::bar {{c2::baz}}::qux}}", 1, false, "foo bar baz"},
			{"{{c1::outer {{c3::inner}}}}", 3, true, "outer [...]"},
			{"{{c1::outer {{c3::inner}}}}", 1, true, "[...]"},
		}
		for _, test := range tests {
			got := stripHTML(revealClozeText(test.text, test.ordinal, test.question))
			if got != test.want {
				t.Errorf("stripHTML(revealClozeText(%q, %d, %v)) = %q, want %q", test.text, test.ordinal, test.question, got, test.want)
			}
		}
	})
}

func TestRevealClozeTextOnly(t *testing.T) {
	tests := []struct {
		text     string
		ordinal  int
		question bool
		want     string
	}{
		{"foo", 1, true, ""},
		{"foo {{c1::bar}}", 1, true, "..."},
		{"foo {{c1::bar::baz}}", 1, true, "baz"},
		{"foo {{c1::bar}}", 1, false, "bar"},
		{"foo {{c1::bar}}", 2, false, ""},
		{"{{c1::foo}} {{c1::bar}}", 1, false, "foo, bar"},
		{"{{c1,2::shared}} and {{c1::first}} vs {{c2::second}}", 1, true, "..., ..."},
		{"{{c1,2::shared}} and {{c1::first}} vs {{c2::second}}", 2, true, "..., ..."},
		{"{{c1,2::shared}} and {{c1::first}} vs {{c2::second}}", 1, false, "shared, first"},
		{"{{c1,2::shared}} and {{c1::first}} vs {{c2::second}}", 2, false, "shared, second"},
	}
	for _, test := range tests {
		if got := revealClozeTextOnly(test.text, test.ordinal, test.question); got != test.want {
			t.Errorf("revealClozeTextOnly(%q, %d, %v) = %q, want %q", test.text, test.ordinal, test.question, got, test.want)
		}
	}
}

func TestExtractClozeForTyping(t *testing.T) {
	tests := []struct {
		text    string
		ordinal int
		want    string
	}{
		{"{{c2::foo}}", 1, ""},
		{"{{c1::foo}} {{c1::bar}} {{c1::foo}}", 1, "foo, bar, foo"},
		{"{{c1::foo}} {{c1::foo}} {{c1::foo}}", 1, "foo"},
		{"{{c1::foo::hint}}", 1, "foo"},
		// Recorded in testdata/anki/typeanswer.golden.json as the expected text
		// of the two cloze cards of the type-in fixture.
		{"one {{c1::two}} three {{c2::four::a hint}}", 1, "two"},
		{"one {{c1::two}} three {{c2::four::a hint}}", 2, "four"},
	}
	for _, test := range tests {
		if got := extractClozeForTyping(test.text, test.ordinal); got != test.want {
			t.Errorf("extractClozeForTyping(%q, %d) = %q, want %q", test.text, test.ordinal, got, test.want)
		}
	}
}

func TestStripClozes(t *testing.T) {
	text := `The {{c1::moon::🌛}} {{c2::orbits::this hint has "::" in it}} the {{c3::🌏}}.`
	want := "The moon orbits the 🌏."
	if got := stripClozes(text); got != want {
		t.Errorf("stripClozes() = %q, want %q", got, want)
	}

	t.Run("text without clozes is unchanged", func(t *testing.T) {
		if got := stripClozes("plain | text }}"); got != "plain | text }}" {
			t.Errorf("stripClozes() = %q", got)
		}
	})
}

func TestContainsCloze(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"plain", false},
		{"{{c0::placeholder}}", false},
		{"{{c2::two}}", true},
		{"{{c0,3::placeholder and card}}", true},
		{"{{c1::unclosed", false},
	}
	for _, test := range tests {
		if got := containsCloze(test.text); got != test.want {
			t.Errorf("containsCloze(%q) = %v, want %v", test.text, got, test.want)
		}
	}
}

func TestExpandClozesToRevealLatex(t *testing.T) {
	if got := expandClozesToRevealLatex("{{c1::foo}} {{c2::bar::baz}}"); got != "" {
		t.Errorf("expandClozesToRevealLatex() without LaTeX = %q, want the empty string", got)
	}

	expanded := stripHTML(expandClozesToRevealLatex("[latex]{{c1::foo}} {{c2::bar::baz}}[/latex]"))
	for _, want := range []string{"foo [baz]", "[...] bar", "foo bar"} {
		if !strings.Contains(expanded, want) {
			t.Errorf("expanded LaTeX text %q does not contain %q", expanded, want)
		}
	}
}

func TestContainsLatex(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"plain", false},
		{"[latex]x[/latex]", true},
		{"[$]x[/$]", true},
		{"[$$]x[/$$]", true},
		{"[latex]unclosed", false},
		{"{{c1::[$]x[/$]}}", true},
	}
	for _, test := range tests {
		if got := containsLatex(test.text); got != test.want {
			t.Errorf("containsLatex(%q) = %v, want %v", test.text, got, test.want)
		}
	}
}

func TestClozeMathAndPipe(t *testing.T) {
	// The math fixture places "}}" inside "\(...\)" and "\[...\]" outside the
	// deletion. Anki's tokenizer is not math-aware, so those characters are
	// ordinary text there; the golden pins them verbatim.
	math := `{{c1::x}} \(a}}b\) \[c}}d\]`
	if got := revealClozeText(math, 1, true); got != `<span class="cloze" data-cloze="x" data-ordinal="1">[...]</span> \(a}}b\) \[c}}d\]` {
		t.Errorf("math text = %q", got)
	}

	// A pipe is ordinary text: it is not a separator, and only the attribute
	// escaping changes it on the question side.
	pipe := "{{c1::left|right}}"
	if got := revealClozeText(pipe, 1, true); got != `<span class="cloze" data-cloze="left&#x7C;right" data-ordinal="1">[...]</span>` {
		t.Errorf("pipe question = %q", got)
	}
	if got := revealClozeText(pipe, 1, false); got != `<span class="cloze" data-ordinal="1">left|right</span>` {
		t.Errorf("pipe answer = %q", got)
	}
}

func TestClozeMathJaxMarkup(t *testing.T) {
	// Markup inside the math delimiters is stripped; the entities stay.
	want := `\(&lt;&gt;\)`
	if got := stripHTMLInsideMathJax(`\(<foo>&lt;&gt;</foo>\)`); got != want {
		t.Errorf("stripHTMLInsideMathJax() = %q, want %q", got, want)
	}
}

func TestImageOcclusionReveal(t *testing.T) {
	text := "{{c1::image-occlusion:rect:left=10.0:top=20:width=30:height=10}}"
	want := `<div class="cloze" data-ordinal="1" data-shape="rect" data-left="10.0" data-top="20" data-width="30" data-height="10" ></div>`
	if got := revealClozeText(text, 1, true); got != want {
		t.Errorf("image occlusion question = %q, want %q", got, want)
	}

	highlight := `<div class="cloze-highlight" data-ordinal="1" data-shape="rect" data-left="10.0" data-top="20" data-width="30" data-height="10" ></div>`
	if got := revealClozeText(text, 1, false); got != highlight {
		t.Errorf("image occlusion answer = %q, want %q", got, highlight)
	}

	// A shape of a different card is inactive.
	inactive := `<div class="cloze-inactive" data-ordinal="1" data-shape="rect" data-left="10.0" data-top="20" data-width="30" data-height="10" ></div>`
	other := revealClozeText(text+"{{c2::x}}", 2, true)
	if !strings.Contains(other, inactive) {
		t.Errorf("other card's shape = %q, want it to contain %q", other, inactive)
	}
}

func TestParseImageOcclusions(t *testing.T) {
	t.Run("one shape per number", func(t *testing.T) {
		text := "{{c1,2::image-occlusion:rect:left=10:top=20:width=30:height=40}}"
		occlusions := ParseImageOcclusions(text)
		if len(occlusions) != 2 {
			t.Fatalf("ParseImageOcclusions() returned %d entries, want 2", len(occlusions))
		}
		if occlusions[0].Ordinal != 1 || occlusions[1].Ordinal != 2 {
			t.Errorf("ordinals = %d, %d, want 1, 2", occlusions[0].Ordinal, occlusions[1].Ordinal)
		}
		for _, occlusion := range occlusions {
			if len(occlusion.Shapes) != 1 || occlusion.Shapes[0].Shape != "rect" {
				t.Errorf("occlusion %d = %+v, want one rect", occlusion.Ordinal, occlusion)
			}
		}
	})

	t.Run("escaped text attribute", func(t *testing.T) {
		text := `{{c1::image-occlusion:text:left=10:top=20:text=\:l\\ol\::width=30:height=40}}`
		occlusions := ParseImageOcclusions(text)
		if len(occlusions) != 1 || len(occlusions[0].Shapes) != 1 {
			t.Fatalf("ParseImageOcclusions() = %+v, want one shape", occlusions)
		}
		shape := occlusions[0].Shapes[0]
		if shape.Shape != "text" {
			t.Errorf("shape = %q, want %q", shape.Shape, "text")
		}
		if len(shape.Properties) != 5 {
			t.Errorf("properties = %+v, want 5", shape.Properties)
		}
		want := ImageOcclusionProperty{Name: "text", Value: `:l\ol:`}
		if !containsProperty(shape.Properties, want) {
			t.Errorf("properties %+v do not contain %+v", shape.Properties, want)
		}
		if !containsProperty(shape.Properties, ImageOcclusionProperty{Name: "width", Value: "30"}) {
			t.Errorf("properties %+v do not contain the width", shape.Properties)
		}
	})

	t.Run("plain clozes have no shapes", func(t *testing.T) {
		if got := ParseImageOcclusions("{{c1::text}}"); len(got) != 0 {
			t.Errorf("ParseImageOcclusions() = %+v, want no entries", got)
		}
	})

	t.Run("entries do not share their properties", func(t *testing.T) {
		text := "{{c1,2::image-occlusion:rect:left=10}}"
		occlusions := ParseImageOcclusions(text)
		if len(occlusions) != 2 {
			t.Fatalf("ParseImageOcclusions() = %+v, want two entries", occlusions)
		}
		occlusions[0].Shapes[0].Properties[0].Value = "changed"
		if got := occlusions[1].Shapes[0].Properties[0].Value; got != "10" {
			t.Errorf("editing one entry changed another: value is %q", got)
		}
	})
}

func containsProperty(properties []ImageOcclusionProperty, want ImageOcclusionProperty) bool {
	for _, property := range properties {
		if property == want {
			return true
		}
	}
	return false
}

// TestClozeGolden checks the note-level cloze numbers the fixture records, which
// card generation depends on and the template engine does not show.
func TestClozeGolden(t *testing.T) {
	golden := loadTemplateGolden(t, "cloze")
	configs := make(map[string]Config, len(golden.NoteTypes))
	for _, noteType := range golden.NoteTypes {
		configs[noteType.Name] = noteType.config(t)
	}
	notes := 0
	for _, note := range golden.Notes {
		cfg, exists := configs[note.NoteTypeName]
		if !exists {
			t.Fatalf("note %q uses the unknown note type %q", note.GUID, note.NoteTypeName)
		}
		values := make([]string, 0, len(cfg.Fields))
		for _, field := range cfg.Fields {
			values = append(values, note.Fields[field.Name])
		}
		got := clozeNumbersInFields(values)
		if !reflect.DeepEqual(got, note.ClozeNumbers) {
			t.Errorf("%s: clozeNumbersInFields() = %v, want %v", note.GUID, got, note.ClozeNumbers)
		}
		// A cloze note type creates one card per cloze number, and a card's
		// template index is its number minus one, so the recorded card set must
		// be the number set shifted by one. Generating the cards themselves is
		// card generation's job; this only checks that the numbers agree.
		if cfg.Kind == KindCloze {
			wantOrdinals := make([]int, len(got))
			for i, ordinal := range got {
				wantOrdinals[i] = ordinal - 1
			}
			if !reflect.DeepEqual(wantOrdinals, note.CardgenExpectedOrdinals) {
				t.Errorf("%s: card ordinals from the cloze numbers = %v, want %v", note.GUID, wantOrdinals, note.CardgenExpectedOrdinals)
			}
		}
		notes++
	}
	t.Logf("cloze: %d notes, cloze numbers compared", notes)
}
