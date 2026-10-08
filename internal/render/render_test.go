package render

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// TestRenderMarkdownStripsUnsafe 是反面用例：
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

// TestRenderMarkdownKeepsAllowedMarkup 是正面用例：
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

// TestRenderMarkdownPreservesMath 是公式存活用例：
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

// 公式的还原点必须与它最终的 HTML 上下文无关。
//
// 占位符是在清洗之前插进 Markdown 文本里的，goldmark 可能把它放进属性值位置
// （链接目标、图片 alt、raw HTML 的 class/href 等）。bluemonday 当时校验的是
// 纯字母数字的占位符，也就是一个安全 token；清洗之后 restoreMath 再把公式原文
// 盲替换回去，公式里的引号就会闭合属性并注入白名单从未校验过的新属性——
// 事件处理属性（onmouseover/onerror/onclick）或一个 javascript: 的 src/href。
//
// 为什么断言基于 HTML 解析而不是字符串搜索：同一段字符 "javascript:" 出现在
// 文本节点里是无害的（例如一道题讨论这个协议），只有被解析成 URL 类属性且其
// scheme 是 javascript/data 才构成脚本 URL。字符串搜索既会漏掉实体编码出来的
// 变体（&#106;avascript:、&quot; 收尾等），也会把无害文本误报。所以这里把输出
// 交给 x/net/html 解析，再逐个节点检查元素/属性是否在白名单内、事件属性是否存在、
// URL 属性的 scheme 是否危险。
func TestRenderMarkdownNeutralizesMathAttributeEscape(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		required []string
	}{
		{
			name:     "link destination with quote escapes attribute",
			src:      `[x](\(a" onmouseover="alert(1)//\))`,
			required: []string{"x"},
		},
		{
			name: "image alt with quote escapes attribute",
			src:  `![\(a"onerror="alert(1)//\)](x)`,
		},
		{
			name:     "raw span class with quote escapes attribute",
			src:      `<span class="\(x"onmouseover="alert(1)//\)">t</span>`,
			required: []string{"t"},
		},
		{
			// 公式原文里的引号断开 alt 后，注入一个白名单从未见过的
			// javascript: src——它成为该 img 唯一的 src，浏览器会去加载它。
			name: "alt breakout injects javascript src",
			src:  `<img alt="\(x"src="javascript:alert(1)"//\)">`,
		},
		{
			// 占位符直接占据链接目标时，还原后仍是公式原文；这条锁定
			// “还原后不得出现可执行的脚本协议”。
			name:     "link destination javascript scheme",
			src:      `[x](\(javascript:alert(1)\))`,
			required: []string{"x"},
		},
		{
			name:     "raw link href with quote escapes attribute",
			src:      `<a href="\(a"onclick="alert(1)//\)">x</a>`,
			required: []string{"x"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := RenderMarkdown(tc.src)
			if err != nil {
				t.Fatalf("RenderMarkdown() error = %v", err)
			}
			assertSafeFragment(t, out)
			for _, want := range tc.required {
				if !strings.Contains(out, want) {
					t.Errorf("output lost required %q:\n%s", want, out)
				}
			}
		})
	}
}

// allowedElementNames 是卡面白名单里的元素（与 buildPolicy 保持一致）。
// 解析输出后出现在这里之外的标签，说明有东西从公式里穿出来新建了标签。
var allowedElementNames = map[string]bool{
	"p": true, "br": true, "hr": true,
	"strong": true, "em": true, "del": true, "blockquote": true,
	"code": true, "pre": true,
	"ul": true, "ol": true, "li": true,
	"table": true, "thead": true, "tbody": true, "tr": true, "th": true, "td": true,
	"a": true, "img": true, "span": true,
}

// allowedAttrNames 是白名单允许的属性名（与 buildPolicy 一致）。
var allowedAttrNames = map[string]bool{
	"class": true, "href": true, "src": true, "alt": true, "start": true,
}

// urlAttributes 是解析后需要按 URL 语义检查 scheme 的属性名。
var urlAttributes = map[string]bool{
	"href": true, "src": true, "action": true, "formaction": true,
	"poster": true, "background": true, "data": true, "srcset": true,
}

// assertSafeFragment 用 HTML 解析器检查一段输出片段：
// 元素与属性都在白名单内、没有事件处理属性、URL 属性没有脚本类 scheme。
func assertSafeFragment(t *testing.T, out string) {
	t.Helper()

	nodes, err := html.ParseFragment(strings.NewReader(out), &html.Node{Type: html.ElementNode, DataAtom: atom.Div, Data: "div"})
	if err != nil {
		t.Fatalf("html.ParseFragment(%q) error = %v", out, err)
	}

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if !allowedElementNames[n.Data] {
				t.Errorf("element <%s> is not in the whitelist:\n%s", n.Data, out)
			}
			for _, a := range n.Attr {
				if strings.HasPrefix(a.Key, "on") {
					t.Errorf("event handler attribute %q survived on <%s>:\n%s", a.Key, n.Data, out)
				}
				if !allowedAttrNames[a.Key] {
					t.Errorf("attribute %q on <%s> is not in the whitelist (attribute escape?):\n%s", a.Key, n.Data, out)
				}
				if urlAttributes[a.Key] {
					switch scheme := urlScheme(a.Val); scheme {
					case "javascript", "vbscript", "data":
						t.Errorf("script-like URL scheme %q in %s=%q:\n%s", scheme, a.Key, a.Val, out)
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
}

// urlScheme 按浏览器的 URL 解析规则取 scheme：
// 忽略前导的 C0 控制符与空格，scheme 以字母开头、后接字母/数字/+-.，并以 ':' 收尾；
// 不符合该形状（例如以 '\' 开头）的输入返回空串，代表相对 URL。
func urlScheme(v string) string {
	s := strings.TrimLeft(v, "	\n\r\f \x00")
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case i > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'):
		default:
			goto done
		}
		i++
	}
done:
	if i == 0 || i >= len(s) || s[i] != ':' {
		return ""
	}
	return strings.ToLower(s[:i])
}
