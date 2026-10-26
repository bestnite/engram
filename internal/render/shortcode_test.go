package render

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// shortcodeAudioSHA is a 64-character lowercase hex string, i.e. the only audio source shape the
// allowlist accepts. It stands in for a sha256 and is not derived from any real file.
const shortcodeAudioSHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// TestRenderMarkdownShortcodeExpansion pins the output of every name in the shortcode table.
//
// Each case asserts the full element, not just a fragment: the class is the client's hook, the
// attribute ordering is the table's order, and the visible body has to stay in the text position so
// a client without JavaScript still shows something.
func TestRenderMarkdownShortcodeExpansion(t *testing.T) {
	audioSrc := "/media/" + shortcodeAudioSHA
	cases := []struct {
		name     string
		src      string
		required []string
	}{
		{
			name:     "tts with language and rate",
			src:      `{{tts:こんにちは|lang=ja-JP|rate=0.9}}`,
			required: []string{`<span class="engram-tts" data-tts-text="こんにちは" data-tts-lang="ja-JP" data-tts-rate="0.9">こんにちは</span>`},
		},
		{
			name:     "tts body only",
			src:      `{{tts:hello}}`,
			required: []string{`<span class="engram-tts" data-tts-text="hello">hello</span>`},
		},
		{
			name:     "tts with language and the hide flag",
			src:      `{{tts:こんにちは|lang=ja-JP|hide}}`,
			required: []string{`<span class="engram-tts" data-tts-text="こんにちは" data-tts-lang="ja-JP" data-tts-hide="1">こんにちは</span>`},
		},
		{
			name:     "tts hide flag is emitted after the parameters",
			src:      `{{tts:x|hide|rate=1}}`,
			required: []string{`<span class="engram-tts" data-tts-text="x" data-tts-rate="1" data-tts-hide="1">x</span>`},
		},
		{
			name:     "tts parameters are emitted in table order regardless of source order",
			src:      `{{tts:x|rate=1|lang=en}}`,
			required: []string{`<span class="engram-tts" data-tts-text="x" data-tts-lang="en" data-tts-rate="1">x</span>`},
		},
		{
			name:     "tts body keeps a pipe that does not start a parameter",
			src:      `{{tts:a|b}}`,
			required: []string{`<span class="engram-tts" data-tts-text="a|b">a|b</span>`},
		},
		{
			name:     "another shortcode's flag word is body text, not a parameter",
			src:      `{{tts:x|loop}}`,
			required: []string{`<span class="engram-tts" data-tts-text="x|loop">x|loop</span>`},
		},
		{
			name: "audio with every parameter",
			src:  `{{audio:` + audioSrc + `|start=12.5|end=15|rate=0.75|loop}}`,
			required: []string{
				`<span class="engram-audio" data-audio-src="` + audioSrc + `" data-audio-start="12.5" data-audio-end="15" data-audio-rate="0.75" data-audio-loop="1">` + audioSrc + `</span>`,
			},
		},
		{
			name:     "audio source only",
			src:      `{{audio:` + audioSrc + `}}`,
			required: []string{`<span class="engram-audio" data-audio-src="` + audioSrc + `">` + audioSrc + `</span>`},
		},
		{
			name:     "ruby annotation",
			src:      `{{ruby:漢字|かんじ}}`,
			required: []string{`<ruby>漢字<rt>かんじ</rt></ruby>`},
		},
		{
			name:     "shortcode inside surrounding markdown",
			src:      "前 **{{tts:a}}** 后",
			required: []string{"<strong>", `<span class="engram-tts" data-tts-text="a">a</span>`, "后"},
		},
		{
			name:     "two shortcodes on one line",
			src:      `{{tts:a}} {{tts:b}}`,
			required: []string{`data-tts-text="a"`, `data-tts-text="b"`},
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

// TestRenderMarkdownRubyWrapsClozeHighlight pins the ruby base rules: the base is inline Markdown,
// so a cloze highlight span inside it stays markup and its surrounding text stays text. The span is
// the cloze card type's own output, which it emits before Markdown runs. Every other raw tag in the
// base is shown as source text, and only the reading is escaped.
func TestRenderMarkdownRubyWrapsClozeHighlight(t *testing.T) {
	cases := []struct {
		name      string
		src       string
		required  []string
		forbidden []string
	}{
		{
			name:      "cloze span base",
			src:       `{{ruby:<span class="cloze">你好</span>|nǐ hǎo}}`,
			required:  []string{`<ruby><span class="cloze">你好</span><rt>nǐ hǎo</rt></ruby>`},
			forbidden: []string{"{{ruby", "&lt;span"},
		},
		{
			name:      "front placeholder base",
			src:       `{{ruby:<span class="cloze">[…]</span>|nǐ hǎo}}`,
			required:  []string{`<ruby><span class="cloze">[…]</span><rt>nǐ hǎo</rt></ruby>`},
			forbidden: []string{"{{ruby", "&lt;span"},
		},
		{
			name:      "reading is escaped, not markup",
			src:       `{{ruby:<span class="cloze">你好</span>|a<b&c}}`,
			required:  []string{"<rt>a&lt;b&amp;c</rt>"},
			forbidden: []string{"<rt>a<b", "<b&c"},
		},
		{
			name:      "a pipe inside the highlight does not split the base",
			src:       `{{ruby:<span class="cloze">a|b</span>|r}}`,
			required:  []string{`<ruby><span class="cloze">a|b</span><rt>r</rt></ruby>`},
			forbidden: []string{"{{ruby"},
		},
		{
			name:      "an equals sign inside the highlight does not split the base",
			src:       `{{ruby:<span class="cloze">a=b</span>|r}}`,
			required:  []string{`<ruby><span class="cloze">a=b</span><rt>r</rt></ruby>`},
			forbidden: []string{"{{ruby"},
		},
		{
			name:      "a reading with a pipe stays literal",
			src:       `{{ruby:<span class="cloze">x</span>|a|b}}`,
			required:  []string{"{{ruby:", "|a|b}}"},
			forbidden: []string{"<ruby>", "<rt>"},
		},
		{
			name:      "a reading with an equals sign stays literal",
			src:       `{{ruby:<span class="cloze">x</span>|a=b}}`,
			required:  []string{"{{ruby:", "|a=b}}"},
			forbidden: []string{"<ruby>", "<rt>"},
		},
		{
			name:      "a non-cloze span base stays literal text",
			src:       `{{ruby:<span>你好</span>|nǐ hǎo}}`,
			required:  []string{`<ruby>&lt;span&gt;你好&lt;/span&gt;<rt>nǐ hǎo</rt></ruby>`},
			forbidden: []string{`<span>你好</span>`, "{{ruby"},
		},
		{
			name:      "text before the cloze span is part of the base",
			src:       `{{ruby:先<span class="cloze">x</span>|r}}`,
			required:  []string{`<ruby>先<span class="cloze">x</span><rt>r</rt></ruby>`},
			forbidden: []string{"{{ruby"},
		},
		{
			name:      "text after the cloze span is part of the base",
			src:       `{{ruby:<span class="cloze">x</span>后|r}}`,
			required:  []string{`<ruby><span class="cloze">x</span>后<rt>r</rt></ruby>`},
			forbidden: []string{"{{ruby"},
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
					t.Errorf("output contains forbidden %q:\n%s", bad, out)
				}
			}
			assertSafeFragment(t, out)
		})
	}
}

// TestRenderMarkdownNestedShortcodes pins the supported nesting set and the text projection.
//
// A body is inline Markdown, so emphasis, links and code spans render inside it. The full element
// is asserted rather than a fragment because the point is the projection: data-tts-text carries the
// visible text without the ruby reading, while the visible body carries the rendered structure.
func TestRenderMarkdownNestedShortcodes(t *testing.T) {
	cases := []struct {
		name      string
		src       string
		required  []string
		forbidden []string
	}{
		{
			name:     "tts around ruby",
			src:      `{{tts:{{ruby:漢字|かんじ}}}}`,
			required: []string{`<span class="engram-tts" data-tts-text="漢字"><ruby>漢字<rt>かんじ</rt></ruby></span>`},
		},
		{
			name:     "tts around ruby with parameters",
			src:      `{{tts:{{ruby:漢字|かんじ}}|lang=zh|hide}}`,
			required: []string{`<span class="engram-tts" data-tts-text="漢字" data-tts-lang="zh" data-tts-hide="1"><ruby>漢字<rt>かんじ</rt></ruby></span>`},
		},
		{
			name:     "markdown in a tts body",
			src:      `{{tts:**hi**}}`,
			required: []string{`<span class="engram-tts" data-tts-text="hi"><strong>hi</strong></span>`},
		},
		{
			name:     "link label is the spoken text",
			src:      `{{tts:[docs](https://example.com)}}`,
			required: []string{`data-tts-text="docs"`, `<a href="https://example.com">docs</a>`},
		},
		{
			name:     "autolink is spoken by its label",
			src:      `{{tts:<https://example.com>}}`,
			required: []string{`data-tts-text="https://example.com"`},
		},
		{
			name:     "code span text is spoken and stays literal",
			src:      "{{tts:a `{{ruby:x|y}}` b}}",
			required: []string{`data-tts-text="a {{ruby:x|y}} b"`, "<code>{{ruby:x|y}}</code>"},
			forbidden: []string{
				"<ruby>",
			},
		},
		{
			name:      "malformed inner ruby stays text inside a working tts",
			src:       `{{tts:{{ruby:漢字}}}}`,
			required:  []string{`<span class="engram-tts" data-tts-text="{{ruby:漢字}}">{{ruby:漢字}}</span>`},
			forbidden: []string{"<ruby>"},
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
					t.Errorf("output contains forbidden %q:\n%s", bad, out)
				}
			}
			assertSafeFragment(t, out)
		})
	}
}

// TestRenderMarkdownNestedShortcodeRejections pins the nesting set from the other side: a body that
// expands a shortcode of a kind not allowed in that position makes the outer shortcode literal
// source, and no element or attribute of either shortcode may survive. An inner shortcode is a
// violation only when it expanded; TestRenderMarkdownNestedShortcodes covers the malformed case.
func TestRenderMarkdownNestedShortcodeRejections(t *testing.T) {
	audioSrc := "/media/" + shortcodeAudioSHA
	cases := []struct {
		name string
		src  string
	}{
		{name: "tts inside tts", src: `{{tts:{{tts:x}}}}`},
		{name: "audio inside tts", src: `{{tts:{{audio:` + audioSrc + `}}}}`},
		{name: "tts inside ruby", src: `{{ruby:{{tts:x}}|r}}`},
		{name: "audio inside ruby", src: `{{ruby:{{audio:` + audioSrc + `}}|r}}`},
		{name: "ruby inside ruby", src: `{{ruby:{{ruby:a|b}}|r}}`},
		{name: "audio body holds a shortcode", src: `{{audio:{{tts:x}}}}`},
		{name: "audio body holds an audio shortcode", src: `{{audio:{{audio:` + audioSrc + `}}}}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := RenderMarkdown(tc.src)
			if err != nil {
				t.Fatalf("RenderMarkdown() error = %v", err)
			}
			if !strings.Contains(out, tc.src) {
				t.Errorf("output lost the literal source %q:\n%s", tc.src, out)
			}
			for _, bad := range []string{"engram-", "<ruby>", "<rt>", "data-"} {
				if strings.Contains(out, bad) {
					t.Errorf("rejected nesting still produced %q:\n%s", bad, out)
				}
			}
			assertSafeFragment(t, out)
		})
	}
}

// TestRenderMarkdownShortcodeEmitsSourceForRejection pins the "a shortcode never fails a render"
// rule: every malformed form must come back as its own source text, with no element produced and
// no error returned.
func TestRenderMarkdownShortcodeEmitsSourceForRejection(t *testing.T) {
	audioSrc := "/media/" + shortcodeAudioSHA
	cases := []struct {
		name string
		src  string
		want string
	}{
		{name: "unknown name", src: `{{speak:hello}}`, want: `{{speak:hello}}`},
		{name: "missing body separator", src: `{{tts}}`, want: `{{tts}}`},
		{name: "unknown parameter", src: `{{tts:x|pitch=2}}`, want: `{{tts:x|pitch=2}}`},
		{name: "non-numeric rate", src: `{{tts:x|rate=fast}}`, want: `{{tts:x|rate=fast}}`},
		{name: "empty parameter value", src: `{{tts:x|rate=}}`, want: `{{tts:x|rate=}}`},
		{name: "empty parameter field", src: `{{tts:x|lang=en||rate=1}}`, want: `{{tts:x|lang=en||rate=1}}`},
		{name: "repeated parameter", src: `{{tts:x|rate=1|rate=2}}`, want: `{{tts:x|rate=1|rate=2}}`},
		{name: "bare word after the audio body", src: `{{audio:` + audioSrc + `|rewind}}`, want: `{{audio:` + audioSrc + `|rewind}}`},
		{name: "flag written as a parameter", src: `{{audio:` + audioSrc + `|loop=1}}`, want: `{{audio:` + audioSrc + `|loop=1}}`},
		{name: "tts hide written as a parameter", src: `{{tts:x|hide=1}}`, want: `{{tts:x|hide=1}}`},
		{name: "tts hide flag with an empty value", src: `{{tts:x|hide=}}`, want: `{{tts:x|hide=}}`},
		{name: "repeated tts hide flag", src: `{{tts:x|hide|hide}}`, want: `{{tts:x|hide|hide}}`},
		{name: "hide flag on audio", src: `{{audio:` + audioSrc + `|hide}}`, want: `{{audio:` + audioSrc + `|hide}}`},
		{name: "hide flag on ruby", src: `{{ruby:漢字|かんじ|hide}}`, want: `{{ruby:漢字|かんじ|hide}}`},
		{name: "audio source outside /media", src: `{{audio:https://example.com/a.mp3}}`, want: `{{audio:https://example.com/a.mp3}}`},
		{name: "audio source hash not lowercase", src: `{{audio:/media/0123456789ABCDEF0123456789abcdef0123456789abcdef0123456789abcdef}}`, want: `{{audio:/media/0123456789ABCDEF0123456789abcdef0123456789abcdef0123456789abcdef}}`},
		{name: "audio source is not a path", src: `{{audio:a.mp3}}`, want: `{{audio:a.mp3}}`},
		{name: "empty body", src: `{{tts:}}`, want: `{{tts:}}`},
		{name: "body opens another shortcode", src: `{{tts:a{{b}}`, want: `{{tts:a{{b}}`},
		{name: "list body is not one paragraph", src: `{{tts:- x}}`, want: `{{tts:- x}}`},
		{name: "heading body is not one paragraph", src: `{{tts:# x}}`, want: `{{tts:# x}}`},
		{name: "ruby without reading", src: `{{ruby:漢字}}`, want: `{{ruby:漢字}}`},
		{name: "ruby reading given as a parameter", src: `{{ruby:漢字|かんじ=x}}`, want: `{{ruby:漢字|かんじ=x}}`},
		{name: "ruby with a third field", src: `{{ruby:a|b|c}}`, want: `{{ruby:a|b|c}}`},
		{name: "unterminated shortcode", src: `{{tts:x`, want: `{{tts:x`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := RenderMarkdown(tc.src)
			if err != nil {
				t.Fatalf("RenderMarkdown() error = %v", err)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("output lost the source text %q:\n%s", tc.want, out)
			}
			for _, bad := range []string{"engram-", "<ruby>", "data-tts-", "data-audio-"} {
				if strings.Contains(out, bad) {
					t.Errorf("rejected shortcode still produced %q:\n%s", bad, out)
				}
			}
			assertSafeFragment(t, out)
		})
	}
}

// TestRenderMarkdownShortcodeStaysLiteralInCode is the reason the feature is an inline parser and
// not a string replacement: goldmark does not run inline parsers inside code, so a documented
// shortcode can be shown to the reader as text.
func TestRenderMarkdownShortcodeStaysLiteralInCode(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{name: "code span", src: "`{{tts:x}}`", want: "<code>{{tts:x}}</code>"},
		{name: "code span with a nested shortcode", src: "`{{tts:{{ruby:a|b}}}}`", want: "<code>{{tts:{{ruby:a|b}}}}</code>"},
		{name: "fenced block", src: "```\n{{tts:x}}\n```\n", want: "{{tts:x}}"},
		{name: "fenced block with a nested shortcode", src: "```\n{{tts:{{ruby:a|b}}}}\n```\n", want: "{{tts:{{ruby:a|b}}}}"},
		{name: "indented code block", src: "    {{tts:x}}\n", want: "{{tts:x}}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := RenderMarkdown(tc.src)
			if err != nil {
				t.Fatalf("RenderMarkdown() error = %v", err)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("output missing literal %q:\n%s", tc.want, out)
			}
			for _, bad := range []string{"engram-", "<ruby>", "<rt>"} {
				if strings.Contains(out, bad) {
					t.Errorf("a shortcode expanded inside code (%q):\n%s", bad, out)
				}
			}
		})
	}
}

// TestRenderMarkdownShortcodeCannotEscapeItsAttribute pins the escaping rule: the body is untrusted
// text, so a quote or an angle bracket in it must stay inside the attribute (or the text node) and
// must not create an attribute the allowlist has never seen.
//
// The assertions parse the output and inspect the element's attribute set, because a string search
// cannot tell "the quote is text" from "the quote closed the attribute": both contain the same
// bytes.
func TestRenderMarkdownShortcodeCannotEscapeItsAttribute(t *testing.T) {
	cases := []struct {
		name      string
		src       string
		class     string
		wantText  string
		wantAttrs map[string]string
	}{
		{
			name:     "quote in the body",
			src:      `{{tts:a" onmouseover="alert(1)}}`,
			class:    "engram-tts",
			wantText: `a" onmouseover="alert(1)`,
			wantAttrs: map[string]string{
				"data-tts-text": `a" onmouseover="alert(1)`,
			},
		},
		{
			name:     "angle brackets in the body",
			src:      `{{tts:<script>alert(1)</script>}}`,
			class:    "engram-tts",
			wantText: `<script>alert(1)</script>`,
			wantAttrs: map[string]string{
				"data-tts-text": `<script>alert(1)</script>`,
			},
		},
		{
			name:     "ampersand in the body",
			src:      `{{tts:a&b}}`,
			class:    "engram-tts",
			wantText: `a&b`,
			wantAttrs: map[string]string{
				"data-tts-text": `a&b`,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := RenderMarkdown(tc.src)
			if err != nil {
				t.Fatalf("RenderMarkdown() error = %v", err)
			}
			if strings.Contains(out, "<script") {
				t.Errorf("body introduced a script element:\n%s", out)
			}
			assertSafeFragment(t, out)

			got, text := elementWithClass(t, out, "span", tc.class)
			if got == nil {
				t.Fatalf("output has no span.%s:\n%s", tc.class, out)
			}
			if text != tc.wantText {
				t.Errorf("visible text = %q, want %q", text, tc.wantText)
			}
			for key, want := range tc.wantAttrs {
				if v, ok := got[key]; !ok || v != want {
					t.Errorf("attribute %s = %q (present %v), want %q", key, v, ok, want)
				}
			}
			// The span may carry class and data-tts-* only; a body that produced anything else would
			// appear here.
			for key := range got {
				if key != "class" && !strings.HasPrefix(key, "data-tts-") {
					t.Errorf("unexpected attribute %q on the expanded span", key)
				}
			}
		})
	}
}

// TestRenderMarkdownShortcodeAllowlistIsClosed asserts the allowlist grew by the shortcode
// attributes alone: the same named attributes are accepted on the shortcode span, and a raw span
// cannot use them to smuggle a value the table would never emit, or to carry data-* in general.
func TestRenderMarkdownShortcodeAllowlistIsClosed(t *testing.T) {
	audioSrc := "/media/" + shortcodeAudioSHA
	cases := []struct {
		name      string
		src       string
		forbidden []string
		required  []string
	}{
		{
			name:     "raw span keeps a legitimately shaped data attribute",
			src:      `<span class="engram-tts" data-tts-text="x">x</span>`,
			required: []string{`data-tts-text="x"`},
		},
		{
			name:      "audio source must be the media path",
			src:       `<span data-audio-src="https://example.com/a.mp3">x</span>`,
			forbidden: []string{"https://example.com", "data-audio-src"},
			required:  []string{"<span>x</span>"},
		},
		{
			name:      "audio source hash must be lowercase hex",
			src:       `<span data-audio-src="/media/a.mp3">x</span>`,
			forbidden: []string{"data-audio-src"},
			required:  []string{"<span>x</span>"},
		},
		{
			name:      "a general data attribute stays forbidden",
			src:       `<span data-anything="1">x</span>`,
			forbidden: []string{"data-anything"},
			required:  []string{"<span>x</span>"},
		},
		{
			name:      "a shortcode attribute on another element stays forbidden",
			src:       `<p data-tts-text="x">x</p>`,
			forbidden: []string{"data-tts-text"},
			required:  []string{"<p>x</p>"},
		},
		{
			name:      "numeric parameters reject non-numeric values",
			src:       `<span data-audio-start="soon">x</span>`,
			forbidden: []string{"data-audio-start"},
			required:  []string{"<span>x</span>"},
		},
		{
			name:     "loop is a fixed value",
			src:      `<span data-audio-loop="1">x</span>`,
			required: []string{`data-audio-loop="1"`},
		},
		{
			name:      "loop value is not free text",
			src:       `<span data-audio-loop="true">x</span>`,
			forbidden: []string{"data-audio-loop"},
			required:  []string{"<span>x</span>"},
		},
		{
			name:     "tts hide is a fixed value",
			src:      `<span data-tts-hide="1">x</span>`,
			required: []string{`data-tts-hide="1"`},
		},
		{
			name:      "tts hide value is not free text",
			src:       `<span data-tts-hide="true">x</span>`,
			forbidden: []string{"data-tts-hide"},
			required:  []string{"<span>x</span>"},
		},
		{
			name:      "tts hide on another element stays forbidden",
			src:       `<p data-tts-hide="1">x</p>`,
			forbidden: []string{"data-tts-hide"},
			required:  []string{"<p>x</p>"},
		},
		{
			name:     "ruby elements carry no attributes",
			src:      `<ruby>漢字<rt>かんじ</rt><rp>(</rp></ruby>`,
			required: []string{"<ruby>漢字<rt>かんじ</rt><rp>(</rp></ruby>"},
		},
		{
			name:      "ruby attribute is not allowed",
			src:       `<ruby class="x">漢字</ruby>`,
			forbidden: []string{`class="x"`},
			required:  []string{"<ruby>漢字</ruby>"},
		},
		{
			name:     "expanded audio source survives the allowlist",
			src:      `{{audio:` + audioSrc + `|loop}}`,
			required: []string{`data-audio-src="` + audioSrc + `"`, `data-audio-loop="1"`},
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

// TestRenderMailMarkdownShortcodesArePlainText pins the mail rule: a mail body shares the pipeline
// but has no controls, so each shortcode becomes the text a reader needs, never an element.
func TestRenderMailMarkdownShortcodesArePlainText(t *testing.T) {
	audioSrc := "/media/" + shortcodeAudioSHA
	cases := []struct {
		name      string
		src       string
		required  []string
		forbidden []string
	}{
		{
			name:      "tts",
			src:       `{{tts:こんにちは|lang=ja-JP|rate=0.9}}`,
			required:  []string{"こんにちは"},
			forbidden: []string{"engram-", "data-tts-", "<span"},
		},
		{
			name:      "tts with the hide flag",
			src:       `{{tts:こんにちは|lang=ja-JP|hide}}`,
			required:  []string{"こんにちは"},
			forbidden: []string{"engram-", "data-tts-", "<span"},
		},
		{
			name:      "audio",
			src:       `{{audio:` + audioSrc + `|start=12.5|loop}}`,
			required:  []string{audioSrc},
			forbidden: []string{"engram-", "data-audio-", "<span"},
		},
		{
			name:      "ruby",
			src:       `{{ruby:漢字|かんじ}}`,
			required:  []string{"漢字(かんじ)"},
			forbidden: []string{"<ruby>", "<rt>", "<rp>"},
		},
		{
			name:      "ruby over a cloze highlight",
			src:       `{{ruby:<span class="cloze">你好</span>|nǐ hǎo}}`,
			required:  []string{"你好(nǐ hǎo)"},
			forbidden: []string{"<ruby>", "<rt>", "<span", "cloze"},
		},
		{
			name:      "ruby over a cloze highlight drops raw html",
			src:       `{{ruby:<span class="cloze"><b>你</b>好</span>|nǐ hǎo}}`,
			required:  []string{"你好(nǐ hǎo)"},
			forbidden: []string{"<ruby>", "<rt>", "<span", "<b>"},
		},
		{
			name:      "mail body text is escaped",
			src:       `{{tts:a<b&c}}`,
			required:  []string{"a&lt;b&amp;c"},
			forbidden: []string{"<b>", "a<b"},
		},
		{
			name:      "plain tts body",
			src:       `{{tts:hello}}`,
			required:  []string{"hello"},
			forbidden: []string{"<span", "data-"},
		},
		{
			name:      "markdown in a tts body flattens to its text",
			src:       `{{tts:**hi**}}`,
			required:  []string{"hi"},
			forbidden: []string{"<strong", "data-"},
		},
		{
			name:      "tts around ruby flattens the annotation",
			src:       `{{tts:{{ruby:漢字|かんじ}}}}`,
			required:  []string{"漢字(かんじ)"},
			forbidden: []string{"<span", "data-", "<ruby", "<rt>"},
		},
		{
			name:      "tts around ruby with parameters flattens the annotation",
			src:       `{{tts:{{ruby:漢字|かんじ}}|lang=zh|hide}}`,
			required:  []string{"漢字(かんじ)"},
			forbidden: []string{"<span", "data-", "<ruby", "<rt>"},
		},
		{
			name:      "tts around a cloze highlight flattens to its text",
			src:       `{{tts:<span class="cloze">你好</span>}}`,
			required:  []string{"你好"},
			forbidden: []string{"<span", "data-", "cloze"},
		},
		{
			name:      "rejected shortcode stays source text",
			src:       `{{speak:hello}}`,
			required:  []string{"{{speak:hello}}"},
			forbidden: []string{"engram-"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := RenderMailMarkdown(tc.src)
			if err != nil {
				t.Fatalf("RenderMailMarkdown() error = %v", err)
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
			assertSafeFragmentAllowing(t, out, mailHeadingElements)
		})
	}
}

// elementWithClass returns the attributes of the first tag element whose class list contains class,
// and that element's text content.
func elementWithClass(t *testing.T, out, tag, class string) (map[string]string, string) {
	t.Helper()

	nodes, err := html.ParseFragment(strings.NewReader(out), &html.Node{Type: html.ElementNode, DataAtom: atom.Div, Data: "div"})
	if err != nil {
		t.Fatalf("html.ParseFragment(%q) error = %v", out, err)
	}

	var found *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == tag {
			for _, a := range n.Attr {
				if a.Key != "class" {
					continue
				}
				for _, token := range strings.Fields(a.Val) {
					if token == class {
						found = n
						return
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
	if found == nil {
		return nil, ""
	}

	attrs := make(map[string]string, len(found.Attr))
	for _, a := range found.Attr {
		attrs[a.Key] = a.Val
	}
	var text strings.Builder
	var collect func(*html.Node)
	collect = func(n *html.Node) {
		if n.Type == html.TextNode {
			text.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			collect(c)
		}
	}
	collect(found)
	return attrs, text.String()
}
