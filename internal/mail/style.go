package mail

import (
	"fmt"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// This file gives a rendered mail body its inline styles and derives the plain-text part from the
// same tree.
//
// Why inline styles and not a stylesheet: Gmail drops <style> both on the web and in the apps, so a
// class-based sheet would lose all styling in the largest client. An inline style attribute on each
// element is the only representation every target client agrees on. The sanitiser strips style
// attributes, so this pass runs after it, never before -- adding styles earlier would have them
// removed with the rest of the untrusted attributes.

// bodyStyle is the single inline style applied to each allowed tag of a rendered mail body. Keys are
// tag names exactly as goldmark emits them; the sanitiser runs first, so every tag here is already
// known-safe and appears with at most the attributes the allowlist kept.
//
// Two values are missing on purpose. Paragraphs set no color and no font: the color must be inherited
// so copy inside a blockquote stays gray, and the font must come from the shell so there is only one
// source for it. An inline stylesheet has no ":last-child" or descendant selectors, so an inherited
// value is the only way to keep those variants working.
var bodyStyle = map[string]string{
	"h1":         "margin:0 0 10px;font-size:24px;line-height:1.3;font-weight:600;letter-spacing:-0.02em;color:#18181b;",
	"h2":         "margin:0 0 12px;font-size:21px;line-height:1.32;font-weight:600;letter-spacing:-0.015em;color:#18181b;",
	"h3":         "margin:22px 0 8px;font-size:16px;line-height:1.45;font-weight:600;letter-spacing:-0.005em;color:#18181b;",
	"p":          "margin:0 0 16px;font-size:15px;line-height:1.6;",
	"a":          "color:#2563eb;text-decoration:underline;",
	"hr":         "margin:22px 0;border:0;border-top:1px solid #e4e4e7;",
	"blockquote": "margin:0 0 16px;padding:0 0 0 14px;border-left:2px solid #d4d4d8;color:#52525b;",
	"ul":         "margin:0 0 18px;padding:0;list-style:none;border-top:1px solid #e4e4e7;",
	"ol":         "margin:0 0 18px;padding-left:0;list-style-type:decimal;list-style-position:inside;",
	"li":         "margin:0;padding:8px 0;border-bottom:1px solid #e4e4e7;font-size:15px;line-height:1.6;",
	"strong":     "font-weight:600;",
	"em":         "font-style:italic;",
	"del":        "text-decoration:line-through;color:#71717a;",
	"code":       "padding:1px 5px;border-radius:4px;background-color:#f4f4f5;font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,'Liberation Mono',monospace;font-size:13px;color:#3f3f46;",
	"pre":        "margin:0 0 16px;padding:12px 14px;background-color:#f4f4f5;border:1px solid #e4e4e7;border-radius:8px;font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,'Liberation Mono',monospace;font-size:13px;line-height:1.55;color:#3f3f46;white-space:pre-wrap;word-break:break-word;",
	"table":      "width:100%;margin:0 0 18px;border-collapse:collapse;",
	"th":         "padding:8px 10px;border-bottom:1px solid #e4e4e7;background-color:#fafafa;text-align:left;vertical-align:bottom;font-size:12px;line-height:1.4;font-weight:600;letter-spacing:0.04em;text-transform:uppercase;color:#71717a;",
	"td":         "padding:8px 10px;border-bottom:1px solid #e4e4e7;text-align:left;vertical-align:top;font-size:14px;line-height:1.5;color:#3f3f46;",
}

// buttonStyle replaces bodyStyle["a"] on the anchor of a CTA paragraph. Every value is inline-safe,
// and the border matches the background on purpose: a client that drops background-color but keeps
// border still shows a clickable box instead of dark text.
const buttonStyle = "display:inline-block;padding:13px 22px;background-color:#18181b;border:1px solid #18181b;border-radius:8px;color:#fafafa;font-size:15px;font-weight:600;line-height:1.25;text-decoration:none;"

// plainBlockTags are the elements that read as a block in the plain-text part: a blank line goes
// before and after each of them so a reader of the text-only mail sees the same paragraph structure
// as a reader of the HTML part.
var plainBlockTags = map[string]bool{
	"h1": true, "h2": true, "h3": true,
	"p": true, "blockquote": true, "pre": true,
	"ul": true, "ol": true, "table": true, "tr": true, "hr": true,
}

// styleFragment adds the fixed inline styles to a rendered mail body and derives the plain-text
// part from the same tree.
//
// The input has its variables substituted already, so every element here is one the
// allowlist kept. The fragment is parsed with a div context node: the body is a fragment, not a
// document, and parsing it as a document would invent html/head/body elements around it.
func styleFragment(fragment string) (htmlOut, plain string, err error) {
	context := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(fragment), context)
	if err != nil {
		return "", "", fmt.Errorf("parse mail body fragment: %w", err)
	}

	// The plain text is derived before styling: styles carry no text, so the order does not matter for
	// the output, but deriving from the tree as parsed makes "the two parts say the same thing"
	// independent of the styling pass.
	plain = derivePlain(nodes)

	buttons := make(map[*html.Node]bool)
	for _, n := range nodes {
		findButtonAnchors(n, buttons)
	}
	for _, n := range nodes {
		applyBodyStyle(n, buttons)
	}

	var b strings.Builder
	for _, n := range nodes {
		if err := html.Render(&b, n); err != nil {
			return "", "", fmt.Errorf("serialize mail body fragment: %w", err)
		}
	}
	return b.String(), plain, nil
}

// findButtonAnchors records the anchors that stand alone in a paragraph. A leading call to action
// is a paragraph whose only content is one link; the rule is structural so that a link inside a
// sentence keeps the ordinary link styling.
//
// "Only content" means: after ignoring whitespace-only text nodes the paragraph has exactly one
// child, that child is an anchor, and the anchor has a non-empty href. An empty href is not a real
// destination, so it keeps the ordinary link style rather than looking like a working button.
func findButtonAnchors(n *html.Node, out map[*html.Node]bool) {
	if n.Type == html.ElementNode && n.Data == "p" {
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
func soleAnchorChild(p *html.Node) *html.Node {
	var only *html.Node
	for c := p.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode && strings.TrimSpace(c.Data) == "" {
			continue
		}
		if only != nil {
			return nil
		}
		only = c
	}
	if only == nil || only.Type != html.ElementNode || only.Data != "a" {
		return nil
	}
	if strings.TrimSpace(nodeAttr(only, "href")) == "" {
		return nil
	}
	return only
}

// applyBodyStyle writes the style attribute of every element in the subtree. An element that already
// carries a style is left alone: the sanitiser strips styles, so in practice that cannot happen, and
// keeping the guard means a future allowlist change cannot silently overwrite an author's value.
func applyBodyStyle(n *html.Node, buttons map[*html.Node]bool) {
	if n.Type == html.ElementNode {
		switch {
		case n.Data == "a" && buttons[n]:
			setStyle(n, buttonStyle)
		case bodyStyle[n.Data] != "":
			setStyle(n, bodyStyle[n.Data])
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		applyBodyStyle(c, buttons)
	}
}

// setStyle appends a style attribute unless the node already has one.
func setStyle(n *html.Node, style string) {
	for _, a := range n.Attr {
		if a.Key == "style" {
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: "style", Val: style})
}

// nodeAttr returns an attribute value, or "" when the attribute is absent.
func nodeAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// derivePlain walks the fragment and writes the plain-text part. Text nodes are taken decoded (the
// parser has already resolved entities), so the text-only reader sees the same characters as the
// HTML reader.
func derivePlain(nodes []*html.Node) string {
	var b strings.Builder
	for _, n := range nodes {
		writePlain(n, &b)
	}
	s := strings.TrimSpace(b.String())
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return s
}

// writePlain writes one node and its descendants. Block elements are separated by a blank line, list
// items get a "- " prefix and their own line, and a link renders as "label (url)" so the text-only
// part stays actionable.
func writePlain(n *html.Node, b *strings.Builder) {
	switch n.Type {
	case html.TextNode:
		// Formatting whitespace between block elements (a newline after </li>, between table rows) is
		// not content: the block separators above already produce the layout, so keeping these would
		// double every newline. Whitespace inside a paragraph is left alone, because there it separates
		// words.
		if strings.TrimSpace(n.Data) == "" && formattingWhitespaceParent(n) {
			return
		}
		b.WriteString(n.Data)
		return
	case html.ElementNode:
	default:
		return
	}

	switch n.Data {
	case "br":
		b.WriteString("\n")
		return
	case "hr":
		plainBreak(b, 2)
		b.WriteString("----------")
		plainBreak(b, 2)
		return
	case "img":
		b.WriteString("[" + nodeAttr(n, "alt") + "]")
		return
	case "a":
		writePlainAnchor(n, b)
		return
	}

	block := plainBlockTags[n.Data]
	if block {
		plainBreak(b, 2)
	}
	switch n.Data {
	case "li":
		// A list item starts on its own line, after the blank line the enclosing list opened with.
		plainBreak(b, 1)
		b.WriteString("- ")
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		writePlain(c, b)
	}
	if n.Data == "li" {
		plainBreak(b, 1)
	}
	if block {
		plainBreak(b, 2)
	}
}

// formattingWhitespaceParent reports whether a node sits in a container whose children are blocks, so
// a whitespace-only text node there is source formatting rather than a separator between words. The
// root of the parsed fragment (a nil parent) counts too: the renderer emits blocks at the top level.
func formattingWhitespaceParent(n *html.Node) bool {
	if n.Parent == nil {
		return true
	}
	switch n.Parent.Data {
	case "ul", "ol", "table", "thead", "tbody", "tr", "blockquote":
		return true
	}
	return false
}

// writePlainAnchor writes a link as its label, followed by the target in parentheses when the two
// differ. A link whose text is empty keeps its target so the destination is not lost from the
// text-only part.
func writePlainAnchor(n *html.Node, b *strings.Builder) {
	label := strings.TrimSpace(plainChildren(n))
	href := nodeAttr(n, "href")
	switch {
	case href == "" || href == label:
		b.WriteString(label)
	case label == "":
		b.WriteString(href)
	default:
		b.WriteString(label + " (" + href + ")")
	}
}

// plainChildren renders the descendants of one node into a string, used for a link's label.
func plainChildren(n *html.Node) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		writePlain(c, &b)
	}
	return b.String()
}

// plainBreak guarantees that the buffer ends with at least want newlines, and does nothing at the
// very start so the output does not begin with blank lines.
func plainBreak(b *strings.Builder, want int) {
	s := b.String()
	if s == "" {
		return
	}
	have := len(s) - len(strings.TrimRight(s, "\n"))
	if have >= want {
		return
	}
	b.WriteString(strings.Repeat("\n", want-have))
}
