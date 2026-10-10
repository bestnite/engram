package web

import (
	"regexp"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/cardtype"
)

// bundledClozeMacros 列出挖空渲染在公式里会写出的宏，以及它在自托管 MathJax 包里的定义原文。
//
// 自托管的只有 tex-svg.js 主包；需要另行加载扩展文件的宏（\class、\color、\bbox）
// 在运行时会请求一个不存在的文件，导致整个容器的排版中止。所以挖空只能用已打包进主包的宏，
// 这张表就是对此的核对清单：改 renderCloze 的输出宏时必须同步更新它。
var bundledClozeMacros = map[string]string{
	"boxed": `boxed:["Macro"`, // ams
	"text":  `text:"HBox"`,    // base
}

var texMacroPattern = regexp.MustCompile(`\\([A-Za-z]+)`)

// TestClozeMathMacrosAreBundled 断言公式内挖空的正反面只用到主包里已定义的宏。
func TestClozeMathMacrosAreBundled(t *testing.T) {
	bundle, err := staticFS.ReadFile("static/js/mathjax/tex-svg.js")
	if err != nil {
		t.Fatalf("read bundled MathJax: %v", err)
	}
	ct, ok := cardtype.Lookup("cloze")
	if !ok {
		t.Fatal("cloze card type is not registered")
	}
	// 原文里只有分隔符，没有任何宏，输出里出现的宏都来自挖空渲染本身。
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
