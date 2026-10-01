package render

import (
	"strings"
	"testing"
)

// TestRenderMarkdownStripsUnsafe 是 M2-6 的反面用例：
// script、事件属性、javascript:/data: URL、非白名单元素属性都必须被剥离，
// 而链接文字等内容要保留（剥离标签不等于吞掉内容）。
func TestRenderMarkdownStripsUnsafe(t *testing.T) {
	cases := []struct {
		name      string
		src       string
		forbidden []string
		required  []string
	}{
		{
			name:      "script block",
			src:       "<script>alert('xss')</script>\n\n正文",
			forbidden: []string{"<script", "alert(", "xss"},
			required:  []string{"正文"},
		},
		{
			name:      "javascript url and event attribute on link",
			src:       `<a href="javascript:alert(1)" onclick="alert(2)">click</a>`,
			forbidden: []string{"javascript:", "onclick"},
			required:  []string{"click"},
		},
		{
			name:      "javascript url and event attribute on image",
			src:       `<img src="javascript:alert(3)" onerror="alert(4)">`,
			forbidden: []string{"javascript:", "onerror", "alert("},
		},
		{
			name:      "event attribute on a stripped element",
			src:       `<div onmouseover="alert(5)">text</div>`,
			forbidden: []string{"onmouseover", "<div", "alert("},
			required:  []string{"text"},
		},
		{
			name:      "data url on link",
			src:       `<a href="data:text/html;base64,PHNjcmlwdD4=">x</a>`,
			forbidden: []string{"data:", "base64"},
			required:  []string{"x"},
		},
		{
			name:      "http image src is not https",
			src:       `<img src="http://example.com/a.png" alt="a">`,
			forbidden: []string{"http://example.com"},
		},
		{
			name:      "protocol relative image src",
			src:       `<img src="//example.com/a.png">`,
			forbidden: []string{"example.com/a.png"},
		},
		{
			name:      "iframe content is dropped entirely",
			src:       `<iframe src="https://example.com/x"></iframe>`,
			forbidden: []string{"<iframe", "example.com/x"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := RenderMarkdown(tc.src)
			if err != nil {
				t.Fatalf("RenderMarkdown() error = %v", err)
			}
			for _, bad := range tc.forbidden {
				if strings.Contains(out, bad) {
					t.Errorf("output still contains forbidden %q:\n%s", bad, out)
				}
			}
			for _, want := range tc.required {
				if !strings.Contains(out, want) {
					t.Errorf("output lost required %q:\n%s", want, out)
				}
			}
		})
	}
}

// TestRenderMarkdownKeepsAllowedMarkup 是 M2-6 的正面用例：
// 表格、代码块、常用内联/块级标记必须存活，且 URL 允许名单内的链接与图片要保留。
func TestRenderMarkdownKeepsAllowedMarkup(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		required []string
	}{
		{
			name:     "table",
			src:      "| A | B |\n| --- | --- |\n| 1 | 2 |\n",
			required: []string{"<table>", "<thead>", "<th>", "<tbody>", "<td>"},
		},
		{
			name:     "fenced code block",
			src:      "```go\nfmt.Println(\"ok\")\n```\n",
			required: []string{"<pre><code", "fmt.Println"},
		},
		{
			name:     "inline code",
			src:      "use `go test` now",
			required: []string{"<code>go test</code>"},
		},
		{
			name:     "blockquote and emphasis",
			src:      "> quoted **bold** and *em*\n",
			required: []string{"<blockquote>", "<strong>bold</strong>", "<em>em</em>"},
		},
		{
			name:     "strikethrough",
			src:      "~~gone~~",
			required: []string{"<del>gone</del>"},
		},
		{
			name:     "unordered and ordered lists",
			src:      "- a\n- b\n\n1. one\n2. two\n",
			required: []string{"<ul>", "<li>a</li>", "<ol>", "<li>one</li>"},
		},
		{
			name:     "https link",
			src:      "[t](https://example.com/x)",
			required: []string{`<a href="https://example.com/x">t</a>`},
		},
		{
			name:     "relative link",
			src:      "[t](/notes/1)",
			required: []string{`<a href="/notes/1">t</a>`},
		},
		{
			name:     "mailto link",
			src:      "[m](mailto:a@example.com)",
			required: []string{"mailto:a@example.com"},
		},
		{
			name:     "span with class",
			src:      `<span class="highlight">x</span>`,
			required: []string{`<span class="highlight">x</span>`},
		},
		{
			name:     "https image",
			src:      `<img src="https://example.com/i.png" alt="pic">`,
			required: []string{`src="https://example.com/i.png"`, `alt="pic"`},
		},
		{
			name:     "relative image",
			src:      `<img src="/media/ab.png">`,
			required: []string{`src="/media/ab.png"`},
		},
		{
			name:     "ordered list start attribute",
			src:      "3. three\n4. four\n",
			required: []string{`<ol start="3">`},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := RenderMarkdown(tc.src)
			if err != nil {
				t.Fatalf("RenderMarkdown() error = %v", err)
			}
			for _, want := range tc.required {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q:\n%s", want, out)
				}
			}
		})
	}
}

// TestRenderMarkdownPreservesMath 是 M2-6 的公式存活用例：
// \(...\) 与 \[...\] 的分隔符、反斜杠命令、花括号必须原样出现在 HTML 里，
// 不能被 goldmark 的反斜杠转义吃掉，也不能被清洗器换成 HTML 实体。
func TestRenderMarkdownPreservesMath(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		required []string
	}{
		{
			name:     "inline dfrac",
			src:      `行内 \(\dfrac{a}{b}\) 结束`,
			required: []string{`\(\dfrac{a}{b}\)`, `\dfrac`, `{b}`},
		},
		{
			name:     "inline greek with markdown around it",
			src:      `**bold** 与 \(\alpha+\beta\)`,
			required: []string{"<strong>bold</strong>", `\(\alpha+\beta\)`},
		},
		{
			name:     "display math with integral",
			src:      `\[ \int_0^1 x\,dx \]`,
			required: []string{`\[ \int_0^1 x\,dx \]`},
		},
		{
			name:     "multiline display math",
			src:      "\\[\n\\frac{1}{2}\n\\]",
			required: []string{"\\[\n\\frac{1}{2}\n\\]"},
		},
		{
			name:     "two formulas in one paragraph",
			src:      `\(a\) and \(b\)`,
			required: []string{`\(a\)`, `\(b\)`},
		},
	}

	// 反斜杠被转义成实体会直接破坏 MathJax，单独断言这类实体不出现。
	entityForms := []string{"&#92;", "&#x5c;", "&bsol;", "&Backslash;"}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := RenderMarkdown(tc.src)
			if err != nil {
				t.Fatalf("RenderMarkdown() error = %v", err)
			}
			for _, want := range tc.required {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q:\n%s", want, out)
				}
			}
			for _, bad := range entityForms {
				if strings.Contains(out, bad) {
					t.Errorf("backslash was escaped as %q:\n%s", bad, out)
				}
			}
		})
	}
}

// TestRenderMarkdownEscapesHTMLInsideMath 锁定一条安全边界：
// 公式还原发生在清洗之后，所以公式内部的 < > & 必须由本包转义，
// 否则等于给不可信内容开了一个绕过白名单的入口。
func TestRenderMarkdownEscapesHTMLInsideMath(t *testing.T) {
	out, err := RenderMarkdown(`\(a<b \& c\)`)
	if err != nil {
		t.Fatalf("RenderMarkdown() error = %v", err)
	}
	if strings.Contains(out, "<b") {
		t.Errorf("raw '<' inside math must be escaped:\n%s", out)
	}
	if strings.Contains(out, "& c") {
		t.Errorf("raw '&' inside math must be escaped:\n%s", out)
	}
	if !strings.Contains(out, `\(a&lt;b \&amp; c\)`) {
		t.Errorf("expected escaped math, got:\n%s", out)
	}
}
