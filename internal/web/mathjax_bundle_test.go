package web

import (
	"regexp"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/notetype"
)

// The self-hosted MathJax bundle contains the base and ams packages only. A formula that uses a macro from a package
// loaded on demand makes the runtime request a file this server does not serve, which aborts typesetting for the whole
// container. Cloze rendering therefore must not emit TeX macros of its own: the engine marks a deletion with markup
// (a span carrying the ordinal), and the field's own TeX stays the author's responsibility.

var texMacroPattern = regexp.MustCompile(`\\([A-Za-z]+)`)

// TestClozeRenderingUsesNoTeXMacros asserts that neither side of a cloze card introduces a TeX macro, and that the
// active deletion is marked by the engine's own markup instead.
func TestClozeRenderingUsesNoTeXMacros(t *testing.T) {
	stock, ok := notetype.EngramStockByKind("cloze")
	if !ok {
		t.Fatal("cloze note type is not defined")
	}
	// The original text has only delimiters and no macros at all, so every macro in the output would come from the renderer.
	values := map[string]string{"text": `x = {{c1::y}} + {{c2::z::hint}}`}
	for _, cardTemplate := range []string{"cloze:1", "cloze:2"} {
		template, _, err := notetype.CardTemplate(stock.Config, cardTemplate)
		if err != nil {
			t.Fatalf("resolve template %q: %v", cardTemplate, err)
		}
		for _, side := range []notetype.Side{notetype.SideFront, notetype.SideBack} {
			face, err := notetype.Render(stock.Config, template, side, notetype.RenderData{
				Fields: values, ClozeOrdinal: 1,
			})
			if err != nil {
				t.Fatalf("render %s %s: %v", cardTemplate, side, err)
			}
			if macros := texMacroPattern.FindAllString(face, -1); len(macros) > 0 {
				t.Errorf("%s %s emits TeX macros %v; cloze markup must not need a MathJax extension", cardTemplate, side, macros)
			}
		}
	}

	// The back reveals the deletion as text, so the engine's markup is what distinguishes it.
	front, err := notetype.Render(stock.Config, mustTemplate(t, stock.Config, "cloze:1"), notetype.SideFront, notetype.RenderData{
		Fields: values, ClozeOrdinal: 1,
	})
	if err != nil {
		t.Fatalf("render front: %v", err)
	}
	if !strings.Contains(front, "class=\"cloze\"") {
		t.Errorf("cloze front = %q, want the engine's cloze markup", front)
	}
}

// mustTemplate resolves one template of a config, failing the test when it is absent.
func mustTemplate(t *testing.T, cfg notetype.Config, cardTemplate string) notetype.Template {
	t.Helper()
	template, _, err := notetype.CardTemplate(cfg, cardTemplate)
	if err != nil {
		t.Fatalf("resolve template %q: %v", cardTemplate, err)
	}
	return template
}
