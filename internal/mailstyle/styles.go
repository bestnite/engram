package mailstyle

// This package holds the inline styles of a mail body and the one Markdown-to-HTML conversion that
// writes them in.
//
// Two callers share it. The built-in mail bodies are hand-written HTML and read their style values
// from Body, so the copy in the code and the styles the mail ships with cannot drift apart. The
// one-time migration that converts stored Markdown templates to HTML calls MarkdownToHTML, so a
// converted custom template gets exactly the same markup a built-in body has.
//
// Nothing here sanitises. Since administrators write arbitrary HTML and CSS, the styles are a
// presentation default, not a boundary: an element that already carries a style attribute keeps it.

import "html"

// Body is the single inline style of each tag a mail body is made of, keyed by tag name. Paragraphs
// deliberately set no color and no font: the color must be inherited so a note inside a blockquote
// stays gray, and the font must come from the fixed shell so there is only one source for it. Mail
// clients drop <style> often enough that the shipped look cannot depend on a stylesheet.
var Body = map[string]string{
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

// Button is the style of a call-to-action anchor. It replaces Body["a"] on the anchor of a paragraph
// whose only content is that link. The border matches the background on purpose: a client that
// drops background-color but keeps border still shows a clickable box instead of dark text.
const Button = "display:inline-block;padding:13px 22px;background-color:#18181b;border:1px solid #18181b;border-radius:8px;color:#fafafa;font-size:15px;font-weight:600;line-height:1.25;text-decoration:none;"

// StyleAttr returns the style of a tag as it must be written inside a double-quoted attribute in
// hand-written HTML. The serialiser used by MarkdownToHTML escapes the same characters, so a
// built-in body and a converted Markdown template produce byte-identical attributes.
func StyleAttr(tag string) string {
	return html.EscapeString(Body[tag])
}
