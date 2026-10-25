package render

import (
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/cardtype"
)

// TestClozeCardRendersWithoutMarkup drives the real card pipeline (card type render -> Markdown ->
// allowlist sanitizing) and asserts that the cloze highlight span survives sanitizing, that Markdown
// inside the content still renders, and that the {{cN:: markers do not appear in the HTML.
func TestClozeCardRendersWithoutMarkup(t *testing.T) {
	ct, ok := cardtype.Lookup("cloze")
	if !ok {
		t.Fatal("cloze card type is not registered")
	}
	fields := map[string]any{"text": "The {{c1::**mitochondrion**}} carries {{c2::DNA::genetic material}}. It makes {{c3::ATP|adenosine triphosphate}} via {{c4::A\\|B}}."}
	cases := []struct {
		name     string
		template string
		side     cardtype.Side
		required []string
	}{
		{"c1 front", "cloze:1", cardtype.SideFront, []string{`<span class="cloze">[…]</span>`, "carries DNA."}},
		{"c1 back", "cloze:1", cardtype.SideBack, []string{`<span class="cloze"><strong>mitochondrion</strong></span>`, "carries DNA."}},
		{"c2 front", "cloze:2", cardtype.SideFront, []string{"<strong>mitochondrion</strong>", `<span class="cloze">[genetic material]</span>`}},
		{"c2 back", "cloze:2", cardtype.SideBack, []string{`<span class="cloze">DNA</span>`}},
		// Alternatives: the back lists all of them; an escaped pipe renders as a literal |, with no
		// leftover backslash.
		{"c3 back lists alternatives", "cloze:3", cardtype.SideBack, []string{`<span class="cloze">ATP / adenosine triphosphate</span>`}},
		{"c4 back escaped pipe", "cloze:4", cardtype.SideBack, []string{`<span class="cloze">A|B</span>`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := ct.Render(cardtype.Card{Template: tc.template, Fields: fields}, tc.side)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			out, err := RenderMarkdown(res.Body)
			if err != nil {
				t.Fatalf("RenderMarkdown: %v", err)
			}
			if strings.Contains(out, "{{c") {
				t.Errorf("html = %q, want no raw cloze markup", out)
			}
			for _, want := range tc.required {
				if !strings.Contains(out, want) {
					t.Errorf("html = %q, want substring %q", out, want)
				}
			}
		})
	}
}

// TestClozeInsideMathRendersAsBoxed drives the real pipeline and asserts that a cloze inside a formula
// is handed to MathJax as \boxed{...}, instead of becoming an escaped literal span inside the formula;
// a cloze outside a formula is still a span.
func TestClozeInsideMathRendersAsBoxed(t *testing.T) {
	ct, ok := cardtype.Lookup("cloze")
	if !ok {
		t.Fatal("cloze card type is not registered")
	}
	cases := []struct {
		name      string
		text      string
		side      cardtype.Side
		required  []string
		forbidden []string
	}{
		{
			name:      "front inside inline math",
			text:      `增长量 \(= A - {{c1::B}}\)`,
			side:      cardtype.SideFront,
			required:  []string{`\(= A - \boxed{\text{[…]}}\)`},
			forbidden: []string{"&lt;span", "{{c"},
		},
		{
			name:      "back in a tex group",
			text:      `增长率 \(r = \dfrac{A - B}{{{c1::B}}}\)`,
			side:      cardtype.SideBack,
			required:  []string{`\(r = \dfrac{A - B}{\boxed{B}}\)`},
			forbidden: []string{"&lt;span", "{{c"},
		},
		{
			// \alt is not a TeX macro, so a math blank can list several accepted answers; the back boxes all of them.
			name:      "back lists every alternative inside one box",
			text:      `\({{c1::r_1 \times r_2 \alt r1*r2}}\)`,
			side:      cardtype.SideBack,
			required:  []string{`\(\boxed{r_1 \times r_2\ /\ r1*r2}\)`},
			forbidden: []string{"&lt;span", "{{c", `\alt`},
		},
		{
			name:      "math and text deletions in one note",
			text:      `{{c1::基期}} \(= {{c1::A}}\)`,
			side:      cardtype.SideBack,
			required:  []string{`<span class="cloze">基期</span>`, `\(= \boxed{A}\)`},
			forbidden: []string{"&lt;span"},
		},
		{
			// A hint inside a formula does not go through the allowlist, only restoreMath escaping; the
			// injected tag must stay text.
			name:      "html in a hint inside math stays escaped",
			text:      `\(x = {{c1::2::<img src=x onerror=alert(1)>}}\)`,
			side:      cardtype.SideFront,
			required:  []string{`\boxed{\text{[&lt;img src=x onerror=alert(1)&gt;]}}`},
			forbidden: []string{"<img"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := ct.Render(cardtype.Card{Template: "cloze:1", Fields: map[string]any{"text": tc.text}}, tc.side)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			out, err := RenderMarkdown(res.Body)
			if err != nil {
				t.Fatalf("RenderMarkdown: %v", err)
			}
			for _, want := range tc.required {
				if !strings.Contains(out, want) {
					t.Errorf("html = %q, want substring %q", out, want)
				}
			}
			for _, bad := range tc.forbidden {
				if strings.Contains(out, bad) {
					t.Errorf("html = %q, must not contain %q", out, bad)
				}
			}
		})
	}
}

// TestListBackRendersAsList drives the real pipeline and asserts that the back of a list card renders as
// <ol>/<ul> according to ordered, survives sanitizing, and no longer repeats the prompt.
func TestListBackRendersAsList(t *testing.T) {
	ct, ok := cardtype.Lookup("list")
	if !ok {
		t.Fatal("list card type is not registered")
	}
	for _, tc := range []struct {
		ordered bool
		tag     string
	}{{false, "<ul>"}, {true, "<ol>"}} {
		fields := map[string]any{"prompt": "the-prompt", "items": []any{"red", "blue|violet"}, "ordered": tc.ordered}
		res, err := ct.Render(cardtype.Card{Template: "forward", Fields: fields}, cardtype.SideBack)
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		out, err := RenderMarkdown(res.Body)
		if err != nil {
			t.Fatalf("RenderMarkdown: %v", err)
		}
		if !strings.Contains(out, tc.tag) || !strings.Contains(out, "<li>blue / violet</li>") || strings.Contains(out, "the-prompt") {
			t.Errorf("ordered=%v html = %q, want %s with every alternative and no prompt", tc.ordered, out, tc.tag)
		}
	}
}
