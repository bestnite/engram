package mailstyle

// The Markdown-to-HTML conversion used by the one-time mail-template migration.
//
// This is the old mail pipeline reduced to the two steps the migration needs: render the stored
// Markdown, then write the inline styles in. It is deliberately frozen here rather than kept in the
// live mail package: administrators now write mail as HTML, so no live path converts Markdown any
// more, and the package that renders mail must not carry a Markdown dependency for a migration.
//
// The conversion matches the renderer that produced the stored bodies: GitHub Flavored Markdown, a
// single newline inside a paragraph as <br>, raw HTML passed through (the old pipeline did not
// sanitise either), the visible text of a shortcode, and TeX formulas left alone.

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
	"golang.org/x/net/html/atom"

	xhtml "golang.org/x/net/html"

	"git.nite07.com/nite/engram/internal/texmath"
)

var markdown = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(html.WithUnsafe(), html.WithHardWraps()),
)

// MarkdownToHTML converts one stored mail body to the inline-styled HTML the mail pipeline now
// expects. The conversion is the exact one the pre-HTML renderer applied to the same source.
//
// Template placeholders are replaced with inert tokens before goldmark runs and restored afterwards:
// goldmark percent-encodes braces in a link destination, which would turn href="{{url}}" into
// href="%7B%7Burl%7D%7D" and the live renderer would then find no placeholder to substitute. The old
// renderer tokenised first for the same reason.
func MarkdownToHTML(src string) (string, error) {
	tokenised, names := tokenisePlaceholders(src)
	protected, spans := protectMath(shortcodeText(tokenised))
	var buf bytes.Buffer
	if err := markdown.Convert([]byte(protected), &buf); err != nil {
		return "", fmt.Errorf("render mail markdown: %w", err)
	}
	styled, err := StyleFragment(restoreMath(buf.String(), spans))
	if err != nil {
		return "", err
	}
	return restorePlaceholders(styled, names), nil
}

// placeholderRe matches one {{name}} placeholder in a stored template.
var placeholderRe = regexp.MustCompile(`\{\{([a-z0-9_]+)\}\}`)

// placeholderPrefix is the alphanumeric prefix of a placeholder token. Goldmark leaves it alone and
// the HTML serialiser writes it unchanged in text and in attribute values.
const placeholderPrefix = "MAILPLACEHOLDERTOKEN"

// tokenisePlaceholders replaces placeholders with tokens and returns the names in token order.
func tokenisePlaceholders(src string) (string, []string) {
	var names []string
	out := placeholderRe.ReplaceAllStringFunc(src, func(match string) string {
		name := placeholderRe.FindStringSubmatch(match)[1]
		names = append(names, name)
		return placeholderPrefix + strconv.Itoa(len(names)-1)
	})
	return out, names
}

// restorePlaceholders writes the original placeholders back into the rendered HTML.
func restorePlaceholders(rendered string, names []string) string {
	for i, name := range names {
		rendered = strings.ReplaceAll(rendered, placeholderPrefix+strconv.Itoa(i), "{{"+name+"}}")
	}
	return rendered
}

// shortcodeText replaces every shortcode with the visible text it stands for: the body of a tts or
// audio shortcode, and "base(reading)" for a ruby annotation. An unknown name, a body without the
// name separator or an unclosed marker stays literal source text.
func shortcodeText(src string) string {
	var out strings.Builder
	for i := 0; i < len(src); {
		start := strings.Index(src[i:], "{{")
		if start < 0 {
			out.WriteString(src[i:])
			break
		}
		start += i
		out.WriteString(src[i:start])
		body, end, ok := balancedShortcode(src, start)
		if !ok {
			out.WriteString(src[start:])
			break
		}
		if text, known := shortcodeVisibleText(body); known {
			out.WriteString(text)
		} else {
			out.WriteString(src[start:end])
		}
		i = end
	}
	return out.String()
}

// balancedShortcode returns the text between the braces that open at start and the index just past
// the closing pair. Nested shortcodes are matched, so a body may contain another marker.
func balancedShortcode(src string, start int) (string, int, bool) {
	depth := 0
	for i := start; i < len(src); {
		switch {
		case strings.HasPrefix(src[i:], "{{"):
			depth++
			i += 2
		case strings.HasPrefix(src[i:], "}}"):
			depth--
			i += 2
			if depth == 0 {
				return src[start+2 : i-2], i, true
			}
		default:
			i++
		}
	}
	return "", 0, false
}

// shortcodeVisibleText maps one shortcode body (the text between the braces) to its visible form.
func shortcodeVisibleText(body string) (string, bool) {
	name, rest, ok := strings.Cut(body, ":")
	if !ok {
		return "", false
	}
	switch strings.TrimSpace(name) {
	case "tts", "audio":
		visible := rest
		if pipe := strings.IndexByte(visible, '|'); pipe >= 0 {
			visible = visible[:pipe]
		}
		return shortcodeText(visible), true
	case "ruby":
		base, reading, ok := strings.Cut(rest, "|")
		if !ok {
			return "", false
		}
		return shortcodeText(base) + "(" + strings.TrimSpace(reading) + ")", true
	default:
		return "", false
	}
}

// mathPrefix is the prefix of a formula placeholder; it is purely alphanumeric so goldmark leaves
// it alone.
const mathPrefix = "MJXFORMULATOKEN"

// mathSpan records one raw formula replaced by a placeholder.
type mathSpan struct {
	token string
	raw   string
}

// mathEscaper escapes only the three characters that would break HTML structure in a text node.
var mathEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
)

// protectMath replaces every whole formula in src with a placeholder.
func protectMath(src string) (string, []mathSpan) {
	ranges := texmath.Ranges(src)
	spans := make([]mathSpan, 0, len(ranges))
	var b strings.Builder
	last := 0
	for _, r := range ranges {
		token := fmt.Sprintf("%s%06d", mathPrefix, len(spans))
		spans = append(spans, mathSpan{token: token, raw: src[r[0]:r[1]]})
		b.WriteString(src[last:r[0]])
		b.WriteString(token)
		last = r[1]
	}
	b.WriteString(src[last:])
	return b.String(), spans
}

// restoreMath swaps the placeholders in the rendered HTML back for the raw formula text.
func restoreMath(out string, spans []mathSpan) string {
	for _, s := range spans {
		out = strings.ReplaceAll(out, s.token, mathEscaper.Replace(s.raw))
	}
	return out
}

// StyleFragment writes the inline styles of Body into a fragment and serialises the result.
//
// An element that already carries a style is left alone, so an author's own CSS survives the
// conversion. A paragraph whose only content is one non-empty link gets Button instead of the
// ordinary link style: that is the call-to-action rule of the built-in mail.
func StyleFragment(fragment string) (string, error) {
	context := &xhtml.Node{Type: xhtml.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := xhtml.ParseFragment(strings.NewReader(fragment), context)
	if err != nil {
		return "", fmt.Errorf("parse mail body fragment: %w", err)
	}

	buttons := make(map[*xhtml.Node]bool)
	for _, n := range nodes {
		findButtonAnchors(n, buttons)
	}
	for _, n := range nodes {
		applyBodyStyle(n, buttons)
	}

	var b strings.Builder
	for _, n := range nodes {
		if err := xhtml.Render(&b, n); err != nil {
			return "", fmt.Errorf("serialize mail body fragment: %w", err)
		}
	}
	return b.String(), nil
}

// findButtonAnchors records the anchors that stand alone in a paragraph.
func findButtonAnchors(n *xhtml.Node, out map[*xhtml.Node]bool) {
	if n.Type == xhtml.ElementNode && n.Data == "p" {
		if a := soleAnchorChild(n); a != nil {
			out[a] = true
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		findButtonAnchors(c, out)
	}
}

// soleAnchorChild returns the paragraph's only non-whitespace child when that child is a non-empty
// anchor, and nil otherwise.
func soleAnchorChild(p *xhtml.Node) *xhtml.Node {
	var only *xhtml.Node
	for c := p.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == xhtml.TextNode && strings.TrimSpace(c.Data) == "" {
			continue
		}
		if only != nil {
			return nil
		}
		only = c
	}
	if only == nil || only.Type != xhtml.ElementNode || only.Data != "a" {
		return nil
	}
	if strings.TrimSpace(nodeAttr(only, "href")) == "" {
		return nil
	}
	return only
}

// applyBodyStyle writes the style attribute of every element in the subtree.
func applyBodyStyle(n *xhtml.Node, buttons map[*xhtml.Node]bool) {
	if n.Type == xhtml.ElementNode {
		switch {
		case n.Data == "a" && buttons[n]:
			setStyle(n, Button)
		case Body[n.Data] != "":
			setStyle(n, Body[n.Data])
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		applyBodyStyle(c, buttons)
	}
}

// setStyle appends a style attribute unless the node already has one.
func setStyle(n *xhtml.Node, style string) {
	for _, a := range n.Attr {
		if a.Key == "style" {
			return
		}
	}
	n.Attr = append(n.Attr, xhtml.Attribute{Key: "style", Val: style})
}

// nodeAttr returns an attribute value, or "" when the attribute is absent.
func nodeAttr(n *xhtml.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
