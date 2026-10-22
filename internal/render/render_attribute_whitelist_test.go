package render

import (
	"strings"
	"testing"
)

// TestRenderMarkdownLocksAttributeWhitelist locks the card attribute allowlist (buildPolicy).
//
// Why this set of cases is required: the sanitized HTML goes straight into card rendering, and the
// attribute allowlist is the only barrier. Carriers that can execute code are not limited to on* --
// the hx-*/data-hx-* prefixes (libraries like htmx hand them to new Function to compile), style
// (overriding site styling) and srcdoc (carrying a whole document) have all been real injection
// points, and loosening one entry in the allowlist opens one hole. So each is pinned down one by
// one, rather than relying on "we remember the allowlist is strict".
//
// Every case asserts two things at once: the executable form in forbidden must not appear in the
// output; and the legitimate tags and text in required must be kept, proving that stripping an
// attribute is not swallowing the content.
// Additionally assertSafeFragment is called on every output, re-checking through HTML parsing that
// the elements and attributes are inside the allowlist, that no on* event attribute is present and
// that no URL attribute carries a script-like scheme -- a structural lockdown of the four attribute
// classes hx-*/data-hx-*/on*/style, which still fails even if a string assertion is bypassed by
// entity encoding.
func TestRenderMarkdownLocksAttributeWhitelist(t *testing.T) {
	cases := []struct {
		name      string
		src       string
		forbidden []string
		required  []string
	}{
		{
			// 2. The event handler attribute onerror on img.
			// Unlike the `src="javascript:..." + onerror` case in render_test.go: this one requires that
			// after stripping onerror the img and its legitimate src still survive.
			name:      "img onerror event attribute",
			src:       `<img src=x onerror=alert(1)>`,
			forbidden: []string{"onerror", "alert("},
			required:  []string{"<img", `src="x"`},
		},
		{
			// 3. div is not in the allowlist, so its onclick must disappear with it, but the text "x"
			// stays.
			name:      "div onclick with non-whitelisted element",
			src:       `<div onclick="alert(1)">x</div>`,
			forbidden: []string{"onclick", "<div", "alert("},
			required:  []string{"x"},
		},
		{
			// 4a. htmx's hx-on: single-colon form: the attribute must be stripped.
			name:      "span hx-on colon attribute",
			src:       `<span hx-on:click="alert(1)">x</span>`,
			forbidden: []string{"hx-", "alert("},
			required:  []string{"<span>x</span>"},
		},
		{
			// 4b. htmx's hx-on:: double-colon form (lifecycle events such as after-swap).
			name:      "span hx-on double colon attribute",
			src:       `<span hx-on::after-swap="alert(1)">x</span>`,
			forbidden: []string{"hx-", "alert("},
			required:  []string{"<span>x</span>"},
		},
		{
			// 5. htmx also recognizes the data-hx-* prefix, which must be sealed off together with hx-*.
			name:      "span data-hx-get attribute",
			src:       `<span data-hx-get="/x">x</span>`,
			forbidden: []string{"data-hx-", "hx-", `"/x"`},
			required:  []string{"<span>x</span>"},
		},
		{
			// 6. Inline styles would let content override site styling, so they must be stripped.
			name:      "span style attribute",
			src:       `<span style="color:red">x</span>`,
			forbidden: []string{"style=", "color:red"},
			required:  []string{"<span>x</span>"},
		},
		{
			// 7a. iframe is not in the allowlist: the tag is dropped along with its content (the same
			// vector has a case in render_test.go too; this one is from the attribute allowlist angle and is
			// kept because it also asserts the src value must not remain).
			name:      "iframe not in whitelist",
			src:       `<iframe src="https://example.com"></iframe>`,
			forbidden: []string{"<iframe", "example.com"},
			required:  nil,
		},
		{
			// 7b. svg is not in the allowlist, so its onload has no way to survive.
			name:      "svg onload not in whitelist",
			src:       `<svg onload=alert(1)>`,
			forbidden: []string{"<svg", "onload", "alert("},
			required:  nil,
		},
		{
			// 7c. math is not in the allowlist (MathJax only consumes text delimiters and does not need
			// the math tag).
			name:      "math not in whitelist",
			src:       `<math><mtext></mtext></math>`,
			forbidden: []string{"<math", "<mtext"},
			required:  nil,
		},
		{
			// 8a. A javascript: target in Markdown link form.
			name:      "markdown link javascript target",
			src:       `[x](javascript:alert(1))`,
			forbidden: []string{"javascript:", "href="},
			required:  []string{"x"},
		},
		{
			// 8b. A javascript: target in Markdown image form: src is stripped, img and alt are kept.
			name:      "markdown image javascript target",
			src:       `![](javascript:alert(1))`,
			forbidden: []string{"javascript:", "src="},
			required:  []string{"<img", `alt=""`},
		},
		{
			// 9. srcdoc is another carrier that can hold a complete HTML document, and must be stripped.
			name:      "iframe srcdoc attribute",
			src:       `<iframe srcdoc="<script>alert(1)</script>"></iframe>`,
			forbidden: []string{"srcdoc", "<iframe", "<script", "alert("},
			required:  nil,
		},
		{
			// 10. Positive control: the legitimate attributes inside the allowlist must be kept verbatim,
			// proving that the "stripping" in each case above comes from tightening the allowlist and not
			// from wiping legitimate content along with it.
			name: "allowed attributes survive",
			src: `<span class="hl">a</span>` +
				` <a href="https://example.com">b</a>` +
				` <img src="/media/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" alt="a">` +
				` <ol start="3"><li>c</li></ol>`,
			forbidden: nil,
			required: []string{
				`<span class="hl">a</span>`,
				`<a href="https://example.com">b</a>`,
				`<img src="/media/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" alt="a">`,
				`<ol start="3">`,
			},
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
			assertSafeFragment(t, out)
		})
	}
}
