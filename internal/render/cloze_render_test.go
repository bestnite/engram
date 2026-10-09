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
	fields := map[string]any{"text": "The {{c1::**mitochondrion**}} carries {{c2::DNA::genetic material}}."}
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
