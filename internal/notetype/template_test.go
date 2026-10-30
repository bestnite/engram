package notetype

import (
	"errors"
	"strings"
	"testing"
)

func renderTestConfig() Config {
	return Config{
		Kind: KindNormal,
		Fields: []Field{
			{ID: "f0", Name: "Front"},
			{ID: "f1", Name: "Back"},
		},
		Templates: []Template{{
			ID:      "t0",
			Name:    "Card 1",
			QFormat: "{{Front}}",
			AFormat: "{{FrontSide}}<hr>{{Back}}",
		}},
		SortFieldID: "f0",
	}
}

func renderTestData(front, back string) RenderData {
	return RenderData{
		Fields:      map[string]string{"f0": front, "f1": back},
		Tags:        []string{"alpha", "beta"},
		TypeName:    "Test note type",
		DeckName:    "Parent::Child",
		SubdeckName: "Child",
		CardName:    "Card 1",
		CardFlag:    "flag0",
		CardID:      "42",
	}
}

func renderTemplate(t *testing.T, cfg Config, tmpl Template, data RenderData) (string, string) {
	t.Helper()
	front, err := Render(cfg, tmpl, SideFront, data)
	if err != nil {
		t.Fatalf("front: %v", err)
	}
	back, err := Render(cfg, tmpl, SideBack, data)
	if err != nil {
		t.Fatalf("back: %v", err)
	}
	return front, back
}

func TestFieldIsEmpty(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"", true},
		{" ", true},
		{"x", false},
		{"<BR>", true},
		{"<div />", true},
		{" <div> <br> </div>\n", true},
		{" <div>x</div>\n", false},
		{"<br><div></div>", true},
		{"\t\n\r\v\f", true},
		{"<span></span>", false},
	}
	for _, test := range tests {
		if got := fieldIsEmpty(test.text); got != test.want {
			t.Errorf("fieldIsEmpty(%q) = %v, want %v", test.text, got, test.want)
		}
	}
}

func TestRenderSyntax(t *testing.T) {
	tests := []struct {
		name      string
		qfmt      string
		afmt      string
		front     string
		back      string
		wantFront string
		wantBack  string
	}{
		{
			name:      "replacement",
			qfmt:      "{{Front}}",
			afmt:      "{{FrontSide}}|{{Back}}",
			front:     "a",
			back:      "b",
			wantFront: "a",
			wantBack:  "a|b",
		},
		{
			name:      "conditional with content",
			qfmt:      "[{{#Front}}{{Front}}{{/Front}}]",
			afmt:      "{{FrontSide}}",
			front:     "a",
			wantFront: "[a]",
			wantBack:  "[a]",
		},
		{
			name:      "conditional on whitespace only",
			qfmt:      "[{{#Front}}{{Front}}{{/Front}}]",
			afmt:      "{{FrontSide}}",
			front:     "   ",
			wantFront: "[]",
			wantBack:  "[]",
		},
		{
			name:      "conditional on an empty div",
			qfmt:      "[{{#Front}}{{Front}}{{/Front}}]",
			afmt:      "{{FrontSide}}",
			front:     "<div> </div>",
			wantFront: "[]",
			wantBack:  "[]",
		},
		{
			name:      "negated conditional on empty",
			qfmt:      "[{{^Front}}none{{/Front}}]",
			afmt:      "{{FrontSide}}",
			front:     "",
			wantFront: "[none]",
			wantBack:  "[none]",
		},
		{
			name:      "negated conditional on content",
			qfmt:      "[{{^Front}}none{{/Front}}]",
			afmt:      "{{FrontSide}}",
			front:     "a",
			wantFront: "[]",
			wantBack:  "[]",
		},
		{
			name:      "nested conditionals",
			qfmt:      "{{#Front}}{{#Back}}{{Front}}{{Back}}{{/Back}}{{/Front}}",
			afmt:      "{{FrontSide}}",
			front:     "a",
			back:      "b",
			wantFront: "ab",
			wantBack:  "ab",
		},
		{
			name:      "comment stays literal",
			qfmt:      "x<!-- {{Front}} -->y",
			afmt:      "{{FrontSide}}",
			front:     "a",
			wantFront: "x<!-- {{Front}} -->y",
			wantBack:  "x<!-- {{Front}} -->y",
		},
		{
			// An unterminated comment is not a comment, so the handlebar inside
			// it is still a handlebar.
			name:      "unterminated comment leaves its handlebar active",
			qfmt:      "<!--{{Front}}",
			afmt:      "{{FrontSide}}",
			front:     "a",
			wantFront: "<!--a",
			wantBack:  "<!--a",
		},
		{
			name:      "stray closing braces stay literal",
			qfmt:      "text }} more {{Front}}",
			afmt:      "{{FrontSide}}",
			front:     "a",
			wantFront: "text }} more a",
			wantBack:  "text }} more a",
		},
		{
			name:      "unterminated handlebar stays literal",
			qfmt:      "text {{Front and more",
			afmt:      "{{FrontSide}}",
			front:     "a",
			wantFront: "text {{Front and more",
			wantBack:  "text {{Front and more",
		},
		{
			name:      "alternate delimiters",
			qfmt:      "{{=<% %>=}}<%Front%>|<%Back%>",
			afmt:      "{{=<% %>=}}<%FrontSide%>",
			front:     "a",
			back:      "b",
			wantFront: "a|b",
			wantBack:  "a|b",
		},
		{
			name:      "standard delimiters become literal in alternate mode",
			qfmt:      "{{=<% %>=}}{{Front}}<%Front%>",
			afmt:      "{{=<% %>=}}<%FrontSide%>",
			front:     "a",
			wantFront: "{{Front}}a",
			wantBack:  "{{Front}}a",
		},
		{
			name:      "repeated directive is removed",
			qfmt:      "{{=<% %>=}}{{=<% %>=}}<%Front%>",
			afmt:      "{{=<% %>=}}<%FrontSide%>",
			front:     "a",
			wantFront: "a",
			wantBack:  "a",
		},
		{
			name:      "directive after leading whitespace",
			qfmt:      "\n  {{=<% %>=}}<%Front%>",
			afmt:      "{{=<% %>=}}<%FrontSide%>",
			front:     "a",
			wantFront: "a",
			wantBack:  "a",
		},
		{
			name:      "whitespace around a tag is trimmed",
			qfmt:      "{{ Front }}",
			afmt:      "{{FrontSide}}",
			front:     "a",
			wantFront: "a",
			wantBack:  "a",
		},

		{
			name:      "cloze conditional of the card",
			qfmt:      "{{#c1}}one{{/c1}}{{#c2}}two{{/c2}}",
			afmt:      "{{FrontSide}}",
			wantFront: "one",
			wantBack:  "one",
		},
		{
			name:      "negated cloze conditional",
			qfmt:      "{{^c2}}not two{{/c2}}",
			afmt:      "{{FrontSide}}",
			wantFront: "not two",
			wantBack:  "not two",
		},
	}

	cfg := renderTestConfig()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tmpl := cfg.Templates[0]
			tmpl.QFormat = test.qfmt
			tmpl.AFormat = test.afmt
			front, back := renderTemplate(t, cfg, tmpl, renderTestData(test.front, test.back))
			if front != test.wantFront {
				t.Errorf("front = %q, want %q", front, test.wantFront)
			}
			if back != test.wantBack {
				t.Errorf("back = %q, want %q", back, test.wantBack)
			}
		})
	}
}

func TestRenderSpecialFields(t *testing.T) {
	tmpl := Template{
		ID:      "t0",
		Name:    "Card 1",
		QFormat: "{{Tags}}|{{Type}}|{{Deck}}|{{Subdeck}}|{{Card}}|{{CardFlag}}|{{CardID}}",
		AFormat: "{{FrontSide}}",
	}
	cfg := renderTestConfig()
	cfg.Templates[0] = tmpl

	front, _ := renderTemplate(t, cfg, tmpl, renderTestData("a", "b"))
	want := "alpha beta|Test note type|Parent::Child|Child|Card 1|flag0|42"
	if front != want {
		t.Errorf("special fields = %q, want %q", front, want)
	}

	t.Run("note field wins", func(t *testing.T) {
		shadow := renderTestConfig()
		shadow.Fields = append(shadow.Fields,
			Field{ID: "f2", Name: "Tags"},
			Field{ID: "f3", Name: "Deck"},
			Field{ID: "f4", Name: "Card"},
		)
		shadow.Templates[0] = tmpl
		data := renderTestData("a", "b")
		data.Fields["f2"] = "field-tags"
		data.Fields["f3"] = "field-deck"
		data.Fields["f4"] = "field-card"

		front, _ := renderTemplate(t, shadow, tmpl, data)
		want := "field-tags|Test note type|field-deck|Child|field-card|flag0|42"
		if front != want {
			t.Errorf("shadowed special fields = %q, want %q", front, want)
		}
	})

	t.Run("empty note field still wins", func(t *testing.T) {
		shadow := renderTestConfig()
		shadow.Fields = append(shadow.Fields, Field{ID: "f2", Name: "Tags"})
		shadow.Templates[0] = tmpl
		data := renderTestData("a", "b")
		data.Fields["f2"] = ""
		data.Tags = nil

		front, _ := renderTemplate(t, shadow, tmpl, data)
		if !strings.HasPrefix(front, "|") {
			t.Errorf("empty note field did not win over the special field: %q", front)
		}
	})
}

func TestRenderFrontSide(t *testing.T) {
	cfg := renderTestConfig()

	t.Run("front side is empty", func(t *testing.T) {
		tmpl := Template{ID: "t0", Name: "Card 1", QFormat: "{{FrontSide}}{{Back}}", AFormat: "{{FrontSide}}"}
		front, back := renderTemplate(t, cfg, tmpl, renderTestData("a", "b"))
		if front != "b" {
			t.Errorf("front = %q, want %q", front, "b")
		}
		if back != "b" {
			t.Errorf("back = %q, want %q", back, "b")
		}
	})

	t.Run("filters on FrontSide are ignored", func(t *testing.T) {
		tmpl := Template{
			ID:      "t0",
			Name:    "Card 1",
			QFormat: "{{Front}}",
			AFormat: "{{text:FrontSide}}",
		}
		_, back := renderTemplate(t, cfg, tmpl, renderTestData("<b>a</b>", "b"))
		if back != "<b>a</b>" {
			t.Errorf("back = %q, want the unfiltered front %q", back, "<b>a</b>")
		}
	})
}

func TestRenderCardOrdinal(t *testing.T) {
	cfg := renderTestConfig()
	cfg.Templates = append(cfg.Templates, Template{
		ID:      "t1",
		Name:    "Card 2",
		QFormat: "{{#c2}}second{{/c2}}{{^c2}}not second{{/c2}}",
		AFormat: "{{FrontSide}}",
	})
	tmpl := cfg.Templates[0]
	tmpl.QFormat = "{{#c1}}first{{/c1}}{{^c1}}not first{{/c1}}"
	cfg.Templates[0] = tmpl

	t.Run("template position supplies the number", func(t *testing.T) {
		front, _ := renderTemplate(t, cfg, tmpl, renderTestData("a", "b"))
		if front != "first" {
			t.Errorf("front = %q, want %q", front, "first")
		}
		second, _ := renderTemplate(t, cfg, cfg.Templates[1], renderTestData("a", "b"))
		if second != "second" {
			t.Errorf("second template front = %q, want %q", second, "second")
		}
	})

	t.Run("cloze ordinal overrides the position", func(t *testing.T) {
		cloze := Config{
			Kind:      KindCloze,
			Fields:    []Field{{ID: "f0", Name: "Text"}},
			Templates: []Template{{ID: "t0", Name: "Cloze", QFormat: "{{#c3}}three{{/c3}}{{#c1}}one{{/c1}}"}},
		}
		data := RenderData{Fields: map[string]string{"f0": ""}, ClozeOrdinal: 3}
		front, err := Render(cloze, cloze.Templates[0], SideFront, data)
		if err != nil {
			t.Fatal(err)
		}
		if front != "three" {
			t.Errorf("front = %q, want %q", front, "three")
		}
	})
}

func TestRenderErrors(t *testing.T) {
	tests := []struct {
		name     string
		front    string
		qfmt     string
		afmt     string
		side     Side
		code     ErrorCode
		field    string
		contains string
	}{
		{
			name:     "single character tag is a field name",
			qfmt:     "{{a}}",
			afmt:     "{{FrontSide}}",
			side:     SideFront,
			code:     CodeUnknownField,
			field:    "a",
			contains: `"a"`,
		},
		{
			name:     "unknown field",
			qfmt:     "{{Missing}}",
			afmt:     "{{FrontSide}}",
			side:     SideFront,
			code:     CodeUnknownField,
			field:    "Missing",
			contains: `"Missing"`,
		},
		{
			name:     "unknown field with filters",
			qfmt:     "{{text:Missing}}",
			afmt:     "{{FrontSide}}",
			side:     SideFront,
			code:     CodeUnknownField,
			field:    "Missing",
			contains: `"Missing"`,
		},
		{
			name:     "unknown field is case sensitive",
			qfmt:     "{{front}}",
			afmt:     "{{FrontSide}}",
			side:     SideFront,
			code:     CodeUnknownField,
			field:    "front",
			contains: `"front"`,
		},
		{
			name:     "unknown conditional",
			qfmt:     "{{#Missing}}x{{/Missing}}",
			afmt:     "{{FrontSide}}",
			side:     SideFront,
			code:     CodeUnknownConditional,
			field:    "Missing",
			contains: `"#Missing"`,
		},
		{
			name:     "unknown negated conditional",
			qfmt:     "{{^Missing}}x{{/Missing}}",
			afmt:     "{{FrontSide}}",
			side:     SideFront,
			code:     CodeUnknownConditional,
			field:    "Missing",
			contains: `"^Missing"`,
		},
		{
			name:     "unclosed conditional",
			qfmt:     "{{#Front}}x",
			afmt:     "{{FrontSide}}",
			side:     SideFront,
			code:     CodeUnclosedConditional,
			field:    "Front",
			contains: `"Front"`,
		},
		{
			name:     "closing tag without a conditional",
			qfmt:     "{{/Front}}",
			afmt:     "{{FrontSide}}",
			side:     SideFront,
			code:     CodeConditionalNotOpen,
			field:    "Front",
			contains: `"{{/Front}}"`,
		},
		{
			name:     "closing tag of the wrong conditional",
			qfmt:     "{{#Front}}x{{/Back}}",
			afmt:     "{{FrontSide}}",
			side:     SideFront,
			code:     CodeConditionalNotOpen,
			field:    "Back",
			contains: `"{{#Front}}"`,
		},
		{
			name:     "unknown field inside a false conditional",
			qfmt:     "{{#Front}}{{Missing}}{{/Front}}",
			afmt:     "{{FrontSide}}",
			side:     SideFront,
			code:     CodeUnknownField,
			field:    "Missing",
			contains: `"Missing"`,
		},
		{
			name:     "unknown field on the back",
			front:    "a",
			qfmt:     "{{Front}}",
			afmt:     "{{Missing}}",
			side:     SideBack,
			code:     CodeUnknownField,
			field:    "Missing",
			contains: `back`,
		},
		{
			name:     "a comment hides template syntax",
			front:    "a",
			qfmt:     "<!-- {{Missing}} {{#Front}} -->",
			afmt:     "{{FrontSide}}",
			side:     SideFront,
			code:     "",
			contains: "",
		},
	}

	cfg := renderTestConfig()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tmpl := cfg.Templates[0]
			tmpl.QFormat = test.qfmt
			tmpl.AFormat = test.afmt
			_, err := Render(cfg, tmpl, test.side, renderTestData(test.front, "b"))
			if test.code == "" {
				if err != nil {
					t.Fatalf("Render() returned %v, want no error", err)
				}
				return
			}
			var renderErr *RenderError
			if !errors.As(err, &renderErr) {
				t.Fatalf("Render() returned %v, want a *RenderError", err)
			}
			if renderErr.Code != test.code {
				t.Errorf("code = %q, want %q", renderErr.Code, test.code)
			}
			if renderErr.Field != test.field {
				t.Errorf("field = %q, want %q", renderErr.Field, test.field)
			}
			if !strings.Contains(renderErr.Detail, test.contains) {
				t.Errorf("detail %q does not contain %q", renderErr.Detail, test.contains)
			}
			if !strings.Contains(renderErr.Detail, tmpl.Name) {
				t.Errorf("detail %q does not name the template %q", renderErr.Detail, tmpl.Name)
			}
		})
	}
}

func TestRenderBackReportsFrontErrors(t *testing.T) {
	cfg := renderTestConfig()
	tmpl := cfg.Templates[0]
	tmpl.QFormat = "{{Missing}}"
	_, err := Render(cfg, tmpl, SideBack, renderTestData("a", "b"))
	var renderErr *RenderError
	if !errors.As(err, &renderErr) || renderErr.Code != CodeUnknownField {
		t.Fatalf("Render(back) returned %v, want an unknown_field error", err)
	}
}

func TestFrontIsEmpty(t *testing.T) {
	tests := []struct {
		name  string
		qfmt  string
		front string
		back  string
		want  bool
	}{
		{"literal text only", "text", "", "b", true},
		{"replacement of a non-empty field", "{{Front}}", "a", "b", false},
		{"replacement of an empty field", "{{Front}}", "", "b", true},
		// Literal text never makes a front non-empty; only a reference to a
		// non-empty field does, which is Anki's rule for a blank front.
		{"conditional on a non-empty field with literal text", "{{#Front}}x{{/Front}}", "a", "b", true},
		{"conditional on a non-empty field that is referenced", "{{#Front}}{{Front}}{{/Front}}", "a", "b", false},
		{"conditional on an empty field", "{{#Front}}x{{/Front}}", "", "b", true},
		{"negated conditional on an empty field", "{{^Front}}x{{/Front}}", "", "b", true},
		{"negated conditional on a non-empty field", "{{^Front}}{{Back}}{{/Front}}", "a", "b", true},
		{"nested reference through an empty conditional", "{{#Front}}{{^Back}}{{Front}}{{/Back}}{{/Front}}", "a", "", false},
		{"nested reference blocked by a non-empty conditional", "{{#Front}}{{^Back}}{{Front}}{{/Back}}{{/Front}}", "a", "b", true},
		{"special field is not referenced", "{{#Front}}x{{/Front}}", "", "b", true},
	}
	cfg := renderTestConfig()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tmpl := cfg.Templates[0]
			tmpl.QFormat = test.qfmt
			if got := FrontIsEmpty(cfg, tmpl, renderTestData(test.front, test.back)); got != test.want {
				t.Errorf("FrontIsEmpty() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestFrontProblem(t *testing.T) {
	normal := renderTestConfig()
	normalEmpty := normal
	normalEmpty.Templates[0].QFormat = "{{Front}}"

	cloze := Config{
		Kind:      KindCloze,
		Fields:    []Field{{ID: "f0", Name: "Text"}},
		Templates: []Template{{ID: "t0", Name: "Cloze", QFormat: "{{cloze:Text}}"}},
	}

	tests := []struct {
		name  string
		cfg   Config
		tmpl  int
		data  RenderData
		code  ErrorCode
		field string
	}{
		{
			name: "normal card with content",
			cfg:  normal,
			data: renderTestData("a", "b"),
		},
		{
			name: "normal card with an empty front",
			cfg:  normal,
			data: renderTestData("", "b"),
			code: CodeEmptyFront,
		},
		{
			name: "normal card with a whitespace front",
			cfg:  normal,
			data: renderTestData("<br>", "b"),
			code: CodeEmptyFront,
		},
		{
			name: "cloze card with its cloze number",
			cfg:  cloze,
			data: RenderData{Fields: map[string]string{"f0": "{{c1::one}} {{c2::two}}"}, ClozeOrdinal: 2},
		},
		{
			name: "cloze card without its cloze number",
			cfg:  cloze,
			data: RenderData{Fields: map[string]string{"f0": "{{c1::one}}"}, ClozeOrdinal: 2},
			code: CodeMissingCloze,
		},
		{
			name: "cloze card without a cloze number",
			cfg:  cloze,
			data: RenderData{Fields: map[string]string{"f0": "{{c1::one}}"}},
		},
		{
			name: "image occlusion counts as a cloze",
			cfg:  cloze,
			data: RenderData{Fields: map[string]string{"f0": "{{c1::image-occlusion:rect:left=0.1}}"}, ClozeOrdinal: 1},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			problem := FrontProblem(test.cfg, test.cfg.Templates[test.tmpl], test.data)
			if test.code == "" {
				if problem != nil {
					t.Fatalf("FrontProblem() = %v, want nil", problem)
				}
				return
			}
			if problem == nil {
				t.Fatalf("FrontProblem() = nil, want code %q", test.code)
			}
			if problem.Code != test.code {
				t.Errorf("code = %q, want %q", problem.Code, test.code)
			}
			if !strings.Contains(problem.Detail, test.cfg.Templates[test.tmpl].Name) {
				t.Errorf("detail %q does not name the template", problem.Detail)
			}
		})
	}
}

func TestRenderEmptyFieldNameWithFilter(t *testing.T) {
	cfg := renderTestConfig()
	tmpl := cfg.Templates[0]
	tmpl.QFormat = "[{{text:}}]"
	tmpl.AFormat = "{{FrontSide}}"
	front, _ := renderTemplate(t, cfg, tmpl, renderTestData("a", "b"))
	if front != "[]" {
		t.Errorf("front = %q, want %q", front, "[]")
	}

	tmpl.QFormat = "[{{type:}}]"
	front, _ = renderTemplate(t, cfg, tmpl, renderTestData("a", "b"))
	if front != "[[[type:]]]" {
		t.Errorf("front = %q, want %q", front, "[[[type:]]]")
	}
}
