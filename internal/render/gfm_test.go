package render

import (
	"strings"
	"testing"
)

// TestRenderMarkdownGFM pins the four GitHub Flavored Markdown features the card renderer enables
// through extension.GFM: tables, strikethrough, linkified bare URLs and task lists.
//
// The task-list case asserts the whole checkbox element, because the allowlist has to keep exactly
// the three attributes goldmark emits (an unchecked item has no checked attribute). Every output is
// also parsed by assertSafeFragment, so the checkbox cannot carry an attribute the allowlist does
// not name.
func TestRenderMarkdownGFM(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		required []string
	}{
		{
			name:     "table",
			src:      "| A | B |\n| --- | --- |\n| 1 | 2 |\n",
			required: []string{"<table>", "<thead>", "<th>A</th>", "<tbody>", "<td>1</td>"},
		},
		{
			name:     "strikethrough",
			src:      "~~gone~~",
			required: []string{"<del>gone</del>"},
		},
		{
			name:     "bare https autolink",
			src:      "see https://example.com/x now",
			required: []string{`<a href="https://example.com/x">https://example.com/x</a>`},
		},
		{
			name:     "bare www autolink",
			src:      "see www.example.com now",
			required: []string{`<a href="http://www.example.com">www.example.com</a>`},
		},
		{
			name: "task list with one checked and one unchecked item",
			src:  "- [x] done\n- [ ] todo\n",
			required: []string{
				"<ul>",
				"<li><input checked=\"\" disabled=\"\" type=\"checkbox\"> done</li>",
				"<li><input disabled=\"\" type=\"checkbox\"> todo</li>",
			},
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
			assertSafeFragment(t, out)
		})
	}
}

// TestRenderMarkdownSanitizesRawControls is the negative case for the checkbox allowance: content
// may write its own HTML, so the input element and its three attributes are reachable without the
// task-list syntax. Only type=checkbox, an empty checked and an empty disabled may survive; every
// other attribute is stripped, and form/button/select/textarea stay outside the allowlist.
func TestRenderMarkdownSanitizesRawControls(t *testing.T) {
	cases := []struct {
		name      string
		src       string
		forbidden []string
		required  []string
	}{
		{
			name:      "unlisted attribute on a checkbox",
			src:       `<input type="checkbox" disabled checked formaction="/submit">`,
			forbidden: []string{"formaction", "/submit"},
			required:  []string{`type="checkbox"`, `disabled=""`, `checked=""`},
		},
		{
			name:      "event attribute on a checkbox",
			src:       `<input type="checkbox" onfocus="alert(1)">`,
			forbidden: []string{"onfocus", "alert("},
			required:  []string{`type="checkbox"`},
		},
		{
			// A non-checkbox type fails the value matcher, and name is not an allowed attribute, so no
			// attribute survives and bluemonday drops the whole input.
			name:      "non-checkbox input loses the element",
			src:       `<input type="text" name="password">`,
			forbidden: []string{"<input", "type=", "name="},
		},
		{
			name:      "checked must have an empty value",
			src:       `<input type="checkbox" checked="checked">`,
			forbidden: []string{"checked="},
			required:  []string{`<input type="checkbox">`},
		},
		{
			name:      "disabled must have an empty value",
			src:       `<input type="checkbox" disabled="disabled">`,
			forbidden: []string{"disabled="},
			required:  []string{`<input type="checkbox">`},
		},
		{
			name:      "checkbox attributes on another element",
			src:       `<p type="checkbox" checked disabled>x</p>`,
			forbidden: []string{"type=", "checked", "disabled"},
			required:  []string{"<p>x</p>"},
		},
		{
			name:      "raw form",
			src:       `<form action="https://example.com/submit"><input type="checkbox" disabled></form>`,
			forbidden: []string{"<form", "action="},
			required:  []string{`type="checkbox"`},
		},
		{
			name:      "raw button",
			src:       `<button type="submit">send</button>`,
			forbidden: []string{"<button", `type="submit"`},
			required:  []string{"send"},
		},
		{
			name:      "raw select",
			src:       `<select name="pick"><option>one</option></select>`,
			forbidden: []string{"<select", "<option", `name=`},
			required:  []string{"one"},
		},
		{
			name:      "raw textarea",
			src:       `<textarea name="note">text</textarea>`,
			forbidden: []string{"<textarea", `name=`},
			required:  []string{"text"},
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
					t.Errorf("output missing %q:\n%s", want, out)
				}
			}
			assertSafeFragment(t, out)
		})
	}
}

// TestRenderMarkdownHardWraps pins the soft-break rule on both entry points: one newline inside a
// paragraph becomes <br>, a blank line still starts a new paragraph, and code is untouched.
//
// A fenced block keeps the newline as a literal character. An inline code span follows the
// Markdown code-span rule instead: its line ending is normalized to a space, which is why the case
// asserts a space and not a newline. Neither may gain a <br>.
func TestRenderMarkdownHardWraps(t *testing.T) {
	cases := []struct {
		name      string
		src       string
		render    func(string) (string, error)
		required  []string
		forbidden []string
	}{
		{
			name:     "card paragraph newline becomes a break",
			src:      "a\nb",
			render:   RenderMarkdown,
			required: []string{"<p>a<br>\nb</p>"},
		},
		{
			name:     "mail paragraph newline becomes a break",
			src:      "a\nb",
			render:   RenderMailMarkdown,
			required: []string{"<p>a<br>\nb</p>"},
		},
		{
			name:      "blank line still separates paragraphs",
			src:       "a\n\nb",
			render:    RenderMarkdown,
			required:  []string{"<p>a</p>", "<p>b</p>"},
			forbidden: []string{"<br"},
		},
		{
			name:      "fenced block keeps its newline",
			src:       "```\na\nb\n```",
			render:    RenderMarkdown,
			required:  []string{"<pre><code>a\nb\n</code></pre>"},
			forbidden: []string{"<br"},
		},
		{
			name:      "inline code span normalizes its newline without a break",
			src:       "use `a\nb` now",
			render:    RenderMarkdown,
			required:  []string{"<code>a b</code>"},
			forbidden: []string{"<br"},
		},
		{
			name:      "table row newline does not become a break",
			src:       "| a | b |\n| --- | --- |\n| 1 | 2 |\n",
			render:    RenderMarkdown,
			required:  []string{"<table>", "<td>1</td>", "</table>"},
			forbidden: []string{"<br"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := tc.render(tc.src)
			if err != nil {
				t.Fatalf("render error = %v", err)
			}
			for _, want := range tc.required {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q:\n%s", want, out)
				}
			}
			for _, bad := range tc.forbidden {
				if strings.Contains(out, bad) {
					t.Errorf("output still contains forbidden %q:\n%s", bad, out)
				}
			}
		})
	}
}

// TestRenderMarkdownHardWrapsKeepsShortcodeAndMath pins the interaction between hard wraps and the
// two placeholder mechanisms: a shortcode expands to an inline element and a formula is replaced by
// a token before Markdown parsing.
//
// The breaks around them are expected, because the newlines that separate them from the surrounding
// text are soft line breaks. The element and the formula themselves must stay intact, with no <br>
// inserted inside, including a formula that spans a newline -- the token hides that newline from the
// block parser.
func TestRenderMarkdownHardWrapsKeepsShortcodeAndMath(t *testing.T) {
	const shortcode = `<span class="engram-tts" data-tts-text="a">a</span>`

	cases := []struct {
		name      string
		src       string
		required  []string
		forbidden []string
	}{
		{
			// The required substring holds the whole span between the two breaks, so a <br> inserted
			// inside the element would make the match fail.
			name:     "shortcode between two soft breaks",
			src:      "前\n{{tts:a}}\n后",
			required: []string{"前<br>\n" + shortcode + "<br>\n后"},
		},
		{
			name:      "formula in a wrapped paragraph",
			src:       "行\n\\(x\\)\n尾",
			required:  []string{"行<br>\n\\(x\\)<br>\n尾"},
			forbidden: []string{`\(x<br>`},
		},
		{
			name:      "formula spanning a newline keeps the newline",
			src:       "start \\(a\nb\\) end",
			required:  []string{"<p>start \\(a\nb\\) end</p>"},
			forbidden: []string{"<br"},
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
			for _, bad := range tc.forbidden {
				if strings.Contains(out, bad) {
					t.Errorf("output still contains forbidden %q:\n%s", bad, out)
				}
			}
			assertSafeFragment(t, out)
		})
	}
}
