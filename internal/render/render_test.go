package render

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// TestRenderMarkdownStripsUnsafe is the negative case:
// script, event attributes, javascript:/data: URLs and non-allowlisted element attributes must all
// be stripped, while link text and similar content is kept (stripping a tag is not swallowing its
// content).
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

// TestRenderMarkdownKeepsAllowedMarkup is the positive case:
// tables, code blocks and the common inline/block markup must survive, and links and images inside
// the URL allowlist must be kept.
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

// TestRenderMarkdownPreservesMath is the formula survival case:
// the \(...\) and \[...\] delimiters, backslash commands and braces must appear in the HTML
// verbatim, neither eaten by goldmark's backslash escaping nor turned into HTML entities by the
// sanitizer.
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

	// A backslash escaped into an entity breaks MathJax outright, so assert separately that no such
	// entity form appears.
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

// TestRenderMarkdownEscapesHTMLInsideMath pins down one security boundary:
// formula restoration happens after sanitizing, so the < > & inside a formula must be escaped by
// this package, otherwise untrusted content gets an entry point around the allowlist.
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

// A formula's restore point must not depend on its final HTML context.
//
// Placeholders are inserted into the Markdown text before sanitizing, so goldmark may put one in an
// attribute value position (link target, image alt, class/href of raw HTML, etc.). bluemonday was
// validating the purely alphanumeric placeholder at that point, i.e. a safe token; after sanitizing,
// restoreMath substitutes the formula text back blindly, so a quote inside the formula closes the
// attribute and injects new attributes the allowlist never validated -- event handler attributes
// (onmouseover/onerror/onclick) or a javascript: src/href.
//
// Why the assertions parse HTML instead of searching strings: the same characters "javascript:" in a
// text node are harmless (a card may well discuss that scheme); it only becomes a script URL when
// parsed as a URL-bearing attribute whose scheme is javascript/data. String search both misses
// variants produced by entity encoding (&#106;avascript:, closing with &quot; and so on) and reports
// harmless text as a failure. So the output goes through the x/net/html parser, and every node is
// then checked for: element/attribute inside the allowlist, presence of an event attribute, and a
// dangerous scheme on a URL attribute.
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
			// A quote in the formula text breaks out of alt and injects a javascript: src the allowlist
			// has never seen -- it becomes the img's only src and the browser will load it.
			name: "alt breakout injects javascript src",
			src:  `<img alt="\(x"src="javascript:alert(1)"//\)">`,
		},
		{
			// When the placeholder occupies the link target itself, restoring yields the formula text;
			// this pins "no executable script scheme may survive the restore".
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

// allowedElementNames is the set of elements in the card allowlist (kept in sync with buildPolicy).
// A tag appearing outside this set in the parsed output means something broke out of a formula and
// created a new tag.
var allowedElementNames = map[string]bool{
	"p": true, "br": true, "hr": true,
	"strong": true, "em": true, "del": true, "blockquote": true,
	"code": true, "pre": true,
	"ul": true, "ol": true, "li": true,
	"table": true, "thead": true, "tbody": true, "tr": true, "th": true, "td": true,
	"a": true, "img": true, "span": true,
}

// allowedAttrNames is the set of attribute names the allowlist permits (consistent with buildPolicy).
var allowedAttrNames = map[string]bool{
	"class": true, "href": true, "src": true, "alt": true, "start": true,
}

// urlAttributes is the set of attribute names whose scheme must be checked with URL semantics after
// parsing.
var urlAttributes = map[string]bool{
	"href": true, "src": true, "action": true, "formaction": true,
	"poster": true, "background": true, "data": true, "srcset": true,
}

// assertSafeFragment checks an output fragment with an HTML parser:
// elements and attributes are inside the allowlist, no event handler attribute exists, and URL
// attributes carry no script-like scheme.
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

// urlScheme extracts the scheme by the browser's URL parsing rules:
// leading C0 control characters and spaces are ignored, the scheme starts with a letter, continues
// with letters/digits/+-,. and ends with ':'; input that does not fit that shape (e.g. one starting
// with '\') yields the empty string, meaning a relative URL.
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
