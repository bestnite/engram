// Package render implements the rendering pipeline for card content:
// Markdown + TeX source -> goldmark to HTML -> bluemonday allowlist sanitizing -> HTML that can be
// embedded into a page safely.
//
// Why a separate package: rendering is used by the web pages and will also be reused by a future
// deck package export; living inside internal/web would make the export path depend on the HTTP
// layer in reverse (AGENTS.md §2.4).
//
// Why bluemonday is mandatory: card content allows a subset of HTML (the last item),
// so goldmark lets raw HTML through with WithUnsafe and safety rests entirely on this package's
// allowlist sanitizing.
package render

import (
	"bytes"
	"fmt"
	"regexp"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

// markdown is the shared goldmark instance.
//
// The Table and Strikethrough extensions are enabled because the card allowlist permits
// table/tbody/del (the table structure comes from the Table extension, del from the Strikethrough
// ~~strike~~ syntax). WithUnsafe is deliberate: raw HTML has to be trimmed by bluemonday against
// the allowlist instead of being escaped into text at the goldmark stage -- otherwise the HTML
// subset would be impossible.
var markdown = goldmark.New(
	goldmark.WithExtensions(
		extension.Table,
		extension.Strikethrough,
	),
	goldmark.WithRendererOptions(html.WithUnsafe()),
)

// policy is the shared bluemonday sanitizing policy for card content, built once at package init.
var policy = buildPolicy(false)

// mailPolicy is the same allowlist plus headings: a mail body has a title of its own, while card
// content does not, so the card policy deliberately strips h1-h6. Nothing else is relaxed -- in
// particular style attributes stay forbidden, because the mail renderer adds its inline styles only
// after sanitizing.
var mailPolicy = buildPolicy(true)

// imgSrcPattern describes the two kinds of value img.src may take: an absolute https URL, or a
// site-relative path.
//
// It is stricter than a.href (no http), because a self-hosted service should not hotlink images over
// plaintext HTTP; the relative-path branch uses "contains no colon" to exclude pseudo schemes such as
// javascript:/data: -- RE2 has no negative lookahead, and url.Parse validates the scheme once more at
// the validURL stage as a second line of defense.
var imgSrcPattern = regexp.MustCompile(
	`(?i)^https://[^:]*$|^[^:/?#][^:]*$|^\.{1,2}/[^:]*$|^/[^:/][^:]*$`,
)

// RenderMarkdown renders the card's Markdown + TeX source into a sanitized HTML fragment.
//
// The src passed in is untrusted; the return value contains only allowlisted tags and attributes.
// MathJax delimiters \( \) and \[ \], along with the backslashes and braces inside them, are kept
// verbatim for the browser-side MathJax 3 to render.
func RenderMarkdown(src string) (string, error) {
	return renderWith(src, policy)
}

// RenderMailMarkdown renders a mail template: the same pipeline as card content, with headings
// allowed (a mail body has a title; card content does not).
//
// A custom mail template is admin-authored Markdown, so it is untrusted exactly like card content and
// goes through the same allowlist. The only difference is the element set.
func RenderMailMarkdown(src string) (string, error) {
	return renderWith(src, mailPolicy)
}

// renderWith runs the shared Markdown + MathJax + sanitize pipeline under one allowlist.
func renderWith(src string, pol *bluemonday.Policy) (string, error) {
	// Protect formulas first, then render Markdown: goldmark treats \( as a CommonMark backslash
	// escape and would swallow the delimiter into (, destroying MathJax completely; see math.go.
	protected, spans := protectMath(src)

	var buf bytes.Buffer
	if err := markdown.Convert([]byte(protected), &buf); err != nil {
		return "", fmt.Errorf("render markdown: %w", err)
	}

	clean := pol.SanitizeBytes(buf.Bytes())

	// Restore the formulas after sanitizing: a formula is plain text, so restoring escapes only the
	// three characters that would break HTML, leaving backslashes and braces untouched, which is what
	// MathJax needs to read the original TeX.
	restored := restoreMath(string(clean), spans)

	// The restore has to go through the same allowlist once more.
	//
	// The placeholders were inserted before sanitizing, so goldmark may have put one in an attribute
	// value position (link target, image alt, class/href of raw HTML). The first sanitizing pass
	// validated the purely alphanumeric placeholder itself, not the restored formula text; a quote in
	// the formula closes the attribute and injects attributes the allowlist has never seen -- event
	// handler attributes (onmouseover/onerror), or a javascript: src/href (the restored attribute value
	// was never URL-validated). So the restored HTML is parsed and sanitized once more by the same
	// policy, re-validating attribute names, event attributes and URL schemes on the real restored
	// content. The < > & in the formula text node were already escaped by restoreMath, and the second
	// pass leaves the backslashes and braces MathJax needs alone.
	return string(pol.SanitizeBytes([]byte(restored))), nil
}

// buildPolicy builds the content allowlist: img/a/code/pre/table/span[class] are allowed, scripts
// never. withHeadings adds h1-h3, which only the mail body needs.
func buildPolicy(withHeadings bool) *bluemonday.Policy {
	p := bluemonday.NewPolicy()

	// The set of elements card content may contain. Headings/h1-h6 are deliberately excluded from the
	// card set: a heading is meaningless in card content, and leaving it out of the allowlist makes
	// bluemonday strip the tag while keeping the text, so nothing is lost.
	p.AllowElements(
		"p", "br", "hr",
		"strong", "em", "del", "blockquote",
		"code", "pre",
		"ul", "ol", "li",
		"table", "thead", "tbody", "tr", "th", "td",
		"a", "img", "span",
	)
	if withHeadings {
		p.AllowElements("h1", "h2", "h3")
	}

	// span's class is used by site styling and highlighting; narrow the value with bluemonday's own
	// token regex.
	p.AllowAttrs("class").Matching(bluemonday.SpaceSeparatedTokens).OnElements("span")

	// The value of a.href is guarded jointly by the global scheme policy below and the relative-path
	// switch: only http/https/mailto and site-relative paths pass.
	p.AllowAttrs("href").OnElements("a")

	// img.src allows only https and site-relative paths (no http); alt improves accessibility.
	p.AllowAttrs("src").Matching(imgSrcPattern).OnElements("img")
	p.AllowAttrs("alt").OnElements("img")

	// ol.start: when an ordered list starts at something other than 1 goldmark emits a start attribute,
	// and not allowing it would produce wrong numbering; the value is restricted to positive integers,
	// so there is no script risk.
	p.AllowAttrs("start").Matching(regexp.MustCompile(`^[0-9]+$`)).OnElements("ol")

	// Turn on URL parsing validation and explicitly allow relative paths (otherwise site addresses such
	// as /media/x would be stripped).
	p.RequireParseableURLs(true)
	p.AllowRelativeURLs(true)
	p.AllowURLSchemes("http", "https", "mailto")

	return p
}
