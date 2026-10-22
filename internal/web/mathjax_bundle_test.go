package web

import (
	"regexp"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/cardtype"
)

// bundledClozeMacros lists the macros cloze rendering can write into a formula, plus their definition as it appears in the self-hosted MathJax bundle.
//
// Only the tex-svg.js main bundle is self-hosted; macros that need a separately loaded extension file (\class, \color, \bbox)
// make the runtime request a file that does not exist, aborting typesetting for the whole container. So clozes may only use macros already bundled in the main file,
// and this table is the checklist for that: changing the macros renderCloze emits means updating it in step.
var bundledClozeMacros = map[string]string{
	"boxed": `boxed:["Macro"`, // ams
	"text":  `text:"HBox"`,    // base
}

var texMacroPattern = regexp.MustCompile(`\\([A-Za-z]+)`)

// TestClozeMathMacrosAreBundled asserts that both sides of an in-formula cloze use only macros already defined in the main bundle.
func TestClozeMathMacrosAreBundled(t *testing.T) {
	bundle, err := staticFS.ReadFile("static/js/mathjax/tex-svg.js")
	if err != nil {
		t.Fatalf("read bundled MathJax: %v", err)
	}
	ct, ok := cardtype.Lookup("cloze")
	if !ok {
		t.Fatal("cloze card type is not registered")
	}
	// The original text has only delimiters and no macros at all, so every macro in the output comes from the cloze rendering itself.
	fields := map[string]any{"text": `\(x = {{c1::y}} + {{c2::z::hint}}\)`}
	used := map[string]bool{}
	for _, template := range []string{"cloze:1", "cloze:2"} {
		for _, side := range []cardtype.Side{cardtype.SideFront, cardtype.SideBack} {
			res, err := ct.Render(cardtype.Card{Template: template, Fields: fields}, side)
			if err != nil {
				t.Fatalf("Render %s: %v", template, err)
			}
			for _, m := range texMacroPattern.FindAllStringSubmatch(res.Body, -1) {
				used[m[1]] = true
			}
		}
	}
	if !used["boxed"] {
		t.Fatalf("macros used = %v, want the cloze highlight macro", used)
	}
	for macro := range used {
		def, ok := bundledClozeMacros[macro]
		if !ok {
			t.Errorf(`cloze emits \%s, which is not in bundledClozeMacros`, macro)
			continue
		}
		if !strings.Contains(string(bundle), def) {
			t.Errorf(`bundled MathJax does not define \%s (looked for %q)`, macro, def)
		}
	}
}
