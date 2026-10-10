package render

import (
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/cardtype"
)

// TestClozeCardRendersWithoutMarkup 走卡面真实管线（题型渲染 → Markdown → 白名单清洗），
// 断言填空的高亮 span 能通过清洗、内容里的 Markdown 照常渲染，且 {{cN:: 标记不出现在 HTML 里。
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
		// 备选答案：背面全部列出；转义的竖线渲染成字面 |，不残留反斜杠。
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

// TestClozeInsideMathRendersAsBoxed 走真实管线，断言公式里的挖空以 \boxed{…}
// 交给 MathJax，而不是变成公式里被转义的字面 span；公式外的挖空仍是 span。
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
			name:      "math and text deletions in one note",
			text:      `{{c1::基期}} \(= {{c1::A}}\)`,
			side:      cardtype.SideBack,
			required:  []string{`<span class="cloze">基期</span>`, `\(= \boxed{A}\)`},
			forbidden: []string{"&lt;span"},
		},
		{
			// 公式里的提示不经过白名单，只靠 restoreMath 转义；注入的标签必须保持为文本。
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
