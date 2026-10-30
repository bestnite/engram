package mail

import (
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"git.nite07.com/nite/engram/internal/notetype"
)

// This file derives the text-only segment of a mail.
//
// The conversion is notetype.PlainText, the repository's one HTML-to-text rule (the same one a field
// list or a search index uses). PlainText strips every tag, which would drop the destination of a
// link -- and a password-reset mail whose text part has no link is useless. A link is therefore
// expanded to "label (url)" in the tree before PlainText runs, the same shape the old mail renderer
// produced.

// mailPlainText expands links and then reduces the fragment to text with notetype.PlainText. A
// fragment the HTML parser rejects is passed to PlainText unchanged: the fallback must never fail.
func mailPlainText(fragment string) string {
	expanded, err := expandLinks(fragment)
	if err != nil {
		return notetype.PlainText(fragment)
	}
	return notetype.PlainText(expanded)
}

// expandLinks replaces every anchor with the text a text-only reader needs: the label, followed by
// the target when the two differ.
func expandLinks(fragment string) (string, error) {
	context := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(fragment), context)
	if err != nil {
		return "", err
	}
	for _, n := range nodes {
		rewriteLinks(n)
	}
	var b strings.Builder
	for _, n := range nodes {
		if err := html.Render(&b, n); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

// rewriteLinks replaces the anchors in the subtree in place.
func rewriteLinks(n *html.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if c.Type == html.ElementNode && c.Data == "a" {
			label := strings.TrimSpace(nodeText(c))
			href := nodeAttr(c, "href")
			n.InsertBefore(&html.Node{Type: html.TextNode, Data: linkText(label, href)}, c)
			n.RemoveChild(c)
		} else {
			rewriteLinks(c)
		}
		c = next
	}
}

// linkText is the text form of one link: its label, plus the target in parentheses when the two
// differ. A link with no text keeps its target so the destination is not lost.
func linkText(label, href string) string {
	switch {
	case href == "" || href == label:
		return label
	case label == "":
		return href
	default:
		return label + " (" + href + ")"
	}
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

// nodeText concatenates the text nodes of a subtree. The parser has already decoded entities, so the
// result is the characters a reader sees.
func nodeText(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(nodeText(c))
	}
	return b.String()
}
