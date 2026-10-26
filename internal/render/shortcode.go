package render

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// A shortcode is an inline `{{name:body|key=value|...}}` marker that expands into an element
// carrying data attributes. It exists so card content can describe a behaviour (speech, audio
// playback, ruby annotation) without the server emitting that behaviour: the expanded element
// carries only named data attributes, and a later client feature attaches the controls. The
// visible body always stays in the output, so a reader without JavaScript, without a speech
// engine, or on a page whose media it may not read sees text instead of nothing.
//
// The syntax is intentionally minimal:
//
//   - one line, and the body ends at the `}}` that balances its opening `{{`, so a body may
//     itself contain a shortcode;
//   - a body is inline Markdown, parsed by the same shared parser, so emphasis, code spans,
//     links and a cloze highlight read normally inside it;
//   - an unknown name, a malformed body, or a nesting combination outside the allowed set is
//     emitted as its source text, so a shortcode never makes a render fail;
//   - only the names in shortcodeTable exist; there is no per-card or per-deployment extension
//     point, because an allowlist that can grow from content is not an allowlist.

// shortcodeTextPattern accepts an arbitrary visible body as an attribute value.
//
// The body is written into the attribute already HTML-escaped, so `<`, `>` and `"` reaching the
// sanitizer are inert characters, not markup. Only control characters are excluded here: they
// have no meaning in text, and bluemonday would otherwise carry them into the output.
var shortcodeTextPattern = regexp.MustCompile(`^[^\x00-\x08\x0b\x0c\x0e-\x1f\x7f]*$`)

// shortcodeNumberPattern is the value shape of every numeric parameter: a non-negative decimal
// such as 15 or 12.5. The bound on digits keeps a pathological value from becoming a huge
// attribute, and the sign is excluded because none of the parameters has a meaningful negative
// value.
var shortcodeNumberPattern = regexp.MustCompile(`^[0-9]{1,9}(?:\.[0-9]{1,6})?$`)

// shortcodeLangPattern is the BCP-47 shape a speech language tag may take: a primary language
// subtag of two or three letters, followed by dot-less alphanumeric subtags.
var shortcodeLangPattern = regexp.MustCompile(`^[A-Za-z]{2,3}(?:-[A-Za-z0-9]{2,8})*$`)

// shortcodeAudioSrcPattern pins the audio source to this service's own content-addressed media
// path. It is deliberately not a general URL: a card must not be able to make the browser fetch
// an external file, and the 64 lowercase hex characters are exactly the sha256 the media package
// stores under /media/.
var shortcodeAudioSrcPattern = regexp.MustCompile(`^/media/[0-9a-f]{64}$`)

// shortcodeFlagValue is the fixed attribute value a bare presence flag expands to. A fixed value
// keeps the allowlist a closed set: the client tests for the attribute, not for a value it would
// otherwise have to interpret.
const shortcodeFlagValue = "1"

// shortcodeParam is one `key=value` field a shortcode name accepts.
type shortcodeParam struct {
	key  string
	attr string
	re   *regexp.Regexp
}

// shortcodeFlag is one bare word flag a shortcode name accepts; it expands to attr="1".
type shortcodeFlag struct {
	key  string
	attr string
}

// shortcodeDef describes one name in the fixed shortcode table.
type shortcodeDef struct {
	// class is the span's CSS class. It is also the hook the client uses to find the element.
	class string
	// bodyAttr receives the body as an attribute value. Empty means the body is text only.
	bodyAttr string
	// bodyRe validates the body when bodyAttr is set; a body that does not match makes the
	// shortcode malformed.
	bodyRe *regexp.Regexp
	// textAttr receives the body as text, for a shortcode whose body is the visible text the
	// client needs to read. Empty means no text attribute is emitted.
	textAttr string
	// params are the accepted key=value fields, in output order.
	params []shortcodeParam
	// flags are the accepted bare words, in output order.
	flags []shortcodeFlag
}

// shortcodeTable is the complete set of shortcodes. Adding a name means adding an entry here and
// an allowlist entry in buildPolicy; no other code changes.
var shortcodeTable = map[string]shortcodeDef{
	"tts": {
		class:    "engram-tts",
		textAttr: "data-tts-text",
		params: []shortcodeParam{
			{key: "lang", attr: "data-tts-lang", re: shortcodeLangPattern},
			{key: "rate", attr: "data-tts-rate", re: shortcodeNumberPattern},
		},
		// `hide` marks the body as one the client may hide in favour of its own speech control.
		// The body is still rendered, so a reader without JavaScript keeps the text.
		flags: []shortcodeFlag{
			{key: "hide", attr: "data-tts-hide"},
		},
	},
	"audio": {
		class:    "engram-audio",
		bodyAttr: "data-audio-src",
		bodyRe:   shortcodeAudioSrcPattern,
		params: []shortcodeParam{
			{key: "start", attr: "data-audio-start", re: shortcodeNumberPattern},
			{key: "end", attr: "data-audio-end", re: shortcodeNumberPattern},
			{key: "rate", attr: "data-audio-rate", re: shortcodeNumberPattern},
		},
		flags: []shortcodeFlag{
			{key: "loop", attr: "data-audio-loop"},
		},
	},
}

// shortcodeModeKey is the parser context key that selects the output form for one render. The
// goldmark instance is shared, so the mode travels with the parse instead of being baked into a
// second configured instance.
var shortcodeModeKey = parser.NewContextKey()

// shortcodeDepthKey is the parser context key that carries how many shortcode bodies enclose the
// text currently being parsed. It bounds the recursion: a body is parsed by the same inline
// parser that produced its parent, so without the bound a self-referential body would never end.
var shortcodeDepthKey = parser.NewContextKey()

// shortcodeMaxDepth is the number of shortcode levels a render may expand. The top-level
// shortcode is depth 0 and its body is depth 1; at depth 2 the parser leaves the source as text.
// Two levels cover every combination content needs, including a spoken ruby annotation around a
// cloze highlight, whose marker is already an HTML span by the time Markdown runs.
const shortcodeMaxDepth = 2

// shortcodeMode selects how a shortcode expands. Card content gets the element; a mail body gets
// plain text, because a mail renders in clients this service does not control and has no
// client-side controls to attach.
type shortcodeMode int

const (
	shortcodeModeCard shortcodeMode = iota
	shortcodeModeMail
)

// shortcodeExtension wires the inline parser and its node renderer into the shared goldmark
// instance.
type shortcodeExtension struct{}

// Extend implements goldmark.Extender.
func (shortcodeExtension) Extend(m goldmark.Markdown) {
	// The trigger character is `{` and no other parser claims it, so the priority only fixes the
	// position among parsers triggered by the same byte; 450 is between the raw HTML parser (400)
	// and emphasis (500). Code spans and fenced blocks are excluded by goldmark itself: it never
	// runs inline parsers inside code.
	m.Parser().AddOptions(parser.WithInlineParsers(util.Prioritized(newShortcodeParser(), 450)))
	m.Renderer().AddOptions(renderer.WithNodeRenderers(util.Prioritized(newShortcodeRenderer(), 500)))
}

// kindShortcode is the AST node kind of an expanded or rejected shortcode.
var kindShortcode = ast.NewNodeKind("EngramShortcode")

// shortcodeNode holds one expanded or rejected shortcode.
//
// The node is a leaf: its body is not attached as AST children, because it was parsed in a
// separate parse with its own source buffer and is kept here as the output fragment and its two
// text projections.
type shortcodeNode struct {
	ast.BaseInline

	// name is the shortcode name; empty for a rejected source literal.
	name string
	// rejected marks a node that shows its own source text. A parent uses it to tell a literal
	// inner shortcode from one that expanded: only an expanded one is a nesting violation.
	rejected bool
	// output is the fragment the renderer writes raw, already in the parse's output mode.
	output []byte
	// flat is the fully flattened plain text: the mail form of this node, and its contribution
	// to a parent's flat text.
	flat string
	// speech is the visible text without an annotation reading: the value of data-tts-text, and
	// this node's contribution to a parent's speech text.
	speech string
}

// Kind implements ast.Node.
func (n *shortcodeNode) Kind() ast.NodeKind { return kindShortcode }

// Dump implements ast.Node.
func (n *shortcodeNode) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, nil, nil)
}

// shortcodeParser is the goldmark inline parser for the `{{...}}` form.
type shortcodeParser struct{}

func newShortcodeParser() parser.InlineParser { return &shortcodeParser{} }

// Trigger implements parser.InlineParser.
func (s *shortcodeParser) Trigger() []byte { return []byte{'{'} }

// Parse implements parser.InlineParser.
//
// A shortcode has to start and end on the current line. Anything that does not fit the form is
// left to the surrounding text parsers, which keeps the source visible instead of swallowing it.
// Once the depth bound is reached the parser declines, so the inner source stays text.
func (s *shortcodeParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	line, _ := block.PeekLine()
	if !bytes.HasPrefix(line, []byte("{{")) {
		return nil
	}

	depth := shortcodeDepth(pc)
	if depth >= shortcodeMaxDepth {
		return nil
	}

	end, ok := shortcodeEnd(line)
	if !ok {
		// No balanced closing brace on this line: not a shortcode, so the text is parsed normally.
		return nil
	}

	mode := shortcodeModeCard
	if v, ok := pc.Get(shortcodeModeKey).(shortcodeMode); ok {
		mode = v
	}

	node := expandShortcode(string(line[2:end]), mode, depth)
	block.Advance(end + 2)
	return node
}

// shortcodeDepth returns the nesting depth stored in the parser context, defaulting to zero.
func shortcodeDepth(pc parser.Context) int {
	if v, ok := pc.Get(shortcodeDepthKey).(int); ok {
		return v
	}
	return 0
}

// shortcodeEnd returns the index of the `}}` that closes the shortcode whose opening `{{` starts
// line.
//
// Braces are counted instead of taking the first `}}`, because a body may itself contain a
// shortcode and the first `}}` then belongs to the innermost body. Bodies are single-line, so the
// scan never crosses a line break.
//
// Braces inside a code span or behind a backslash escape are content, not structure: CommonMark
// renders both as literal text, so counting them would end the shortcode inside source that the
// author wrote as visible characters.
func shortcodeEnd(line []byte) (int, bool) {
	s := string(line)
	depth := 1
	for i := 2; i+1 < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && util.IsPunct(s[i+1]) {
			i++
			continue
		}
		if s[i] == '`' {
			if end, ok := codeSpanEnd(s, i); ok {
				i = end - 1
				continue
			}
		}
		switch {
		case s[i] == '{' && s[i+1] == '{':
			depth++
			i++
		case s[i] == '}' && s[i+1] == '}':
			depth--
			if depth == 0 {
				return i, true
			}
			i++
		}
	}
	return 0, false
}

// codeSpanEnd returns the offset just past the code span that starts at open.
//
// A code span opens with a backtick run and closes with the next run of exactly the same length;
// a different length belongs to the content. An unclosed run is literal text, so it reports false
// and the caller keeps scanning, which is what CommonMark does with unmatched backticks.
func codeSpanEnd(s string, open int) (int, bool) {
	n := 0
	for open+n < len(s) && s[open+n] == '`' {
		n++
	}
	if n == 0 {
		return 0, false
	}
	for i := open + n; i < len(s); {
		if s[i] != '`' {
			i++
			continue
		}
		m := 0
		for i+m < len(s) && s[i+m] == '`' {
			m++
		}
		if m == n {
			return i + m, true
		}
		i += m
	}
	return 0, false
}

// expandShortcode turns the bytes between `{{` and `}}` into a node for mode, at nesting depth.
//
// Every rejection path returns a literal node, escaped: a shortcode is content, and content must
// never be able to make the render fail or disappear.
func expandShortcode(content string, mode shortcodeMode, depth int) *shortcodeNode {
	name, rest, hasBody := strings.Cut(content, ":")
	if !hasBody {
		return newShortcodeLiteral(content)
	}

	if name == "ruby" {
		return expandRuby(rest, mode, depth)
	}

	def, ok := shortcodeTable[name]
	if !ok {
		return newShortcodeLiteral(content)
	}
	return expandSpan(name, def, rest, mode, depth)
}

// newShortcodeLiteral returns a node that shows a rejected shortcode as its own source text.
//
// The node is marked rejected so a parent can distinguish "this inner shortcode is plain text"
// from "this inner shortcode expanded": an unknown or malformed inner shortcode is text and does
// not invalidate the outer one, while an expanded inner shortcode of a disallowed kind does.
func newShortcodeLiteral(content string) *shortcodeNode {
	source := "{{" + content + "}}"
	return &shortcodeNode{
		rejected: true,
		output:   []byte(escapeShortcodeText(source)),
		flat:     source,
		speech:   source,
	}
}

// clozeSpanOpen is the exact opening tag the cloze card type emits around a highlighted deletion.
// A body accepts raw markup only inside this region; see rewriteShortcodeRawHTML.
const clozeSpanOpen = `<span class="cloze">`

// rawTagName returns the element name of a raw HTML tag and whether it is a closing tag.
//
// It reads only the name: attributes and their quoting are not interpreted, because the cloze
// tracking needs nothing more and a general HTML parser would be a much larger promise than this
// static check makes.
func rawTagName(tag string) (name string, closing, ok bool) {
	if len(tag) < 2 || tag[0] != '<' {
		return "", false, false
	}
	i := 1
	if tag[i] == '/' {
		closing = true
		i++
	}
	start := i
	for i < len(tag) {
		c := tag[i]
		if c == '>' || c == '/' || c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			break
		}
		i++
	}
	if i == start {
		return "", false, false
	}
	return tag[start:i], closing, true
}

// clozeSpanDepth applies one raw tag to a cloze-highlight nesting depth.
//
// A cloze highlight may itself contain spans: the cloze card type wraps the highlighted content
// and that content is ordinary Markdown, which may produce its own span or a nested cloze. The
// exact opening tag the card type emits enters the region, every span open tag inside it raises
// the depth, and a span close tag lowers it, so the region ends only when the depth returns to
// zero instead of at the first closing tag. A tag of another name leaves the depth alone.
func clozeSpanDepth(tag string, depth int) int {
	name, closing, ok := rawTagName(tag)
	if !ok || !strings.EqualFold(name, "span") {
		return depth
	}
	if !closing {
		if depth > 0 || tag == clozeSpanOpen {
			return depth + 1
		}
		return depth
	}
	if depth > 0 {
		return depth - 1
	}
	return depth
}

// clozeSpanEnd returns the offset just past the cloze highlight that starts at open.
//
// It walks the raw tags of the region with clozeSpanDepth, so a nested span inside the highlight
// does not end it early. A region that never closes is not a region this renderer can split or
// keep raw, so it reports false and the caller treats the source as malformed.
func clozeSpanEnd(src string, open int) (int, bool) {
	if !strings.HasPrefix(src[open:], clozeSpanOpen) {
		return 0, false
	}
	depth := 0
	for i := open; i < len(src); i++ {
		if src[i] != '<' {
			continue
		}
		gt := strings.IndexByte(src[i:], '>')
		if gt < 0 {
			return 0, false
		}
		depth = clozeSpanDepth(src[i:i+gt+1], depth)
		i += gt
		if depth == 0 {
			return i + 1, true
		}
	}
	return 0, false
}

// parseInlineBody parses one shortcode body as inline Markdown with the shared parser.
//
// A body is a single paragraph: a list, heading, fenced block, reference definition or empty
// document is not a body, and the shortcode then stays literal source. Bodies are single-line,
// so no construct that needs a blank line or an indented block can be a body.
//
// The one exception is a body that starts with a block-level HTML tag: goldmark classifies that
// line as an HTML block, while the body is inline content here, so it is reparsed behind a text
// prefix and the prefix is dropped. Every tag in it still goes through rewriteShortcodeRawHTML, so
// the tag text ends up visible instead of being emitted as markup.
//
// The parse re-enters this package's inline parser for nested shortcodes at depth+1, which is
// intended; the depth bound is what keeps a self-referential body finite. A fresh parser context
// isolates the body's delimiter state from the enclosing document.
func parseInlineBody(src string, depth int) (*ast.Paragraph, []byte, bool) {
	source := []byte(src)
	doc := markdown.Parser().Parse(text.NewReader(source), parser.WithContext(newBodyContext(depth)))
	if para, ok := singleBodyParagraph(doc); ok {
		return para, source, true
	}

	// A single-line body that begins with a block-level tag (`<script>`, `<div>`, a heading-style
	// element only if it were alone on the line) is classified as an HTML block even though it is
	// inline content of the shortcode body. Reparse it behind a one-character text prefix, which
	// cannot start a block, and drop that prefix from the AST again.
	if doc.ChildCount() == 1 {
		if _, isHTMLBlock := doc.FirstChild().(*ast.HTMLBlock); isHTMLBlock {
			return parsePrefixedInlineBody(src, depth)
		}
	}
	return nil, nil, false
}

// bodyPrefix is the plain text prepended to a body that goldmark would otherwise read as an HTML
// block. It is ordinary text, so it cannot open any block; the AST drops it again after parsing.
const bodyPrefix = "x"

// newBodyContext builds the parser context for one body parse: card output, one level deeper and
// a fresh delimiter state.
func newBodyContext(depth int) parser.Context {
	pc := parser.NewContext()
	pc.Set(shortcodeModeKey, shortcodeModeCard)
	pc.Set(shortcodeDepthKey, depth+1)
	return pc
}

// singleBodyParagraph returns the document's only child when that child is a paragraph.
func singleBodyParagraph(doc ast.Node) (*ast.Paragraph, bool) {
	if doc.ChildCount() != 1 {
		return nil, false
	}
	para, ok := doc.FirstChild().(*ast.Paragraph)
	return para, ok
}

// parsePrefixedInlineBody parses a body behind bodyPrefix and removes the prefix from the resulting
// paragraph. Every remaining segment points into the same buffer at its original offset, so the
// rendered fragment and the text projections contain exactly the body.
func parsePrefixedInlineBody(src string, depth int) (*ast.Paragraph, []byte, bool) {
	source := []byte(bodyPrefix + src)
	doc := markdown.Parser().Parse(text.NewReader(source), parser.WithContext(newBodyContext(depth)))
	para, ok := singleBodyParagraph(doc)
	if !ok {
		return nil, nil, false
	}

	first, ok := para.FirstChild().(*ast.Text)
	if !ok || first.Segment.Start != 0 || first.Segment.Stop < len(bodyPrefix) {
		return nil, nil, false
	}
	first.Segment.Start = len(bodyPrefix)
	if first.Segment.Start >= first.Segment.Stop {
		para.RemoveChild(para, first)
	}
	return para, source, true
}

// bodyNestedShortcodes returns the expanded shortcode nodes inside a parsed body, in document
// order. Rejected literals are text, so they are not nesting.
func bodyNestedShortcodes(n ast.Node) []*shortcodeNode {
	var out []*shortcodeNode
	var walk func(ast.Node)
	walk = func(node ast.Node) {
		for c := node.FirstChild(); c != nil; c = c.NextSibling() {
			if s, ok := c.(*shortcodeNode); ok {
				if !s.rejected {
					out = append(out, s)
				}
				continue
			}
			walk(c)
		}
	}
	walk(n)
	return out
}

// shortcodeNestingAllowed reports whether a body of parent may expand a child shortcode.
//
// The allowed set is tiny and one-directional: speech may announce a ruby annotation, and nothing
// else nests. Ruby already spends its one extra field on the reading, and audio's body is an
// opaque media path, so a shortcode inside either has no meaning the renderer could honour. A
// cloze is not a shortcode at this stage: the cloze card type replaces its marker with an HTML
// span before Markdown runs, so a shortcode inside a cloze is an ordinary top-level shortcode
// and is not affected by this rule.
func shortcodeNestingAllowed(parent, child string) bool {
	return parent == "tts" && child == "ruby"
}

// prepareBody applies the body raw-HTML rule and renders the body fragment with its two text
// projections.
func prepareBody(para *ast.Paragraph, source []byte) (fragment, speech, flat string, ok bool) {
	rewriteShortcodeRawHTML(para, source, 0)

	var buf bytes.Buffer
	if err := markdown.Renderer().Render(&buf, source, para); err != nil {
		return "", "", "", false
	}
	rendered := buf.String()
	// The fragment is the paragraph's content without its wrapper. A body whose render is not
	// exactly one paragraph is not a body, so the shortcode stays literal rather than embedding a
	// wrapper element inside an inline span.
	if !strings.HasPrefix(rendered, "<p>") || !strings.HasSuffix(rendered, "</p>\n") {
		return "", "", "", false
	}
	fragment = rendered[len("<p>") : len(rendered)-len("</p>\n")]
	speech = inlineVisibleText(para, source, shortcodeTextSpeech)
	flat = inlineVisibleText(para, source, shortcodeTextFlat)
	return fragment, speech, flat, true
}

// rewriteShortcodeRawHTML applies the body raw-HTML rule in document order and returns the cloze
// span depth at the end of the traversal.
//
// A cloze highlight is the one raw markup a body may keep. The cloze card type emits it before
// Markdown runs, and its content may itself be Markdown or sanitized HTML, so the tags must stay
// raw for the inner Markdown to render and for the final allowlist pass to trim them the same way
// it trims a flat cloze. A highlight may contain its own spans, so the region is tracked by depth;
// see clozeSpanDepth. Any tag outside the region is author text, so it is replaced by a raw string
// node and renders as escaped source: `{{tts:<script>x</script>}}` shows the tags as text instead
// of letting them through. The check compares tag names and the exact opening tag the card type
// emits; it is not a general HTML parser.
func rewriteShortcodeRawHTML(n ast.Node, source []byte, clozeDepth int) int {
	for c := n.FirstChild(); c != nil; {
		next := c.NextSibling()
		if raw, ok := c.(*ast.RawHTML); ok {
			tag := string(raw.Segments.Value(source))
			depth := clozeSpanDepth(tag, clozeDepth)
			switch {
			case depth != clozeDepth:
				clozeDepth = depth
			case clozeDepth == 0:
				n.ReplaceChild(n, c, literalTagNode(tag))
			}
		} else {
			clozeDepth = rewriteShortcodeRawHTML(c, source, clozeDepth)
		}
		c = next
	}
	return clozeDepth
}

// literalTagNode turns raw tag source into text that renders exactly as written.
//
// The raw flag matters twice: without it the renderer would resolve character references and
// remove backslash escapes, so `<div title="&copy;">` would be displayed as `<div title="©">`
// instead of keeping the source characters the author typed.
func literalTagNode(tag string) *ast.String {
	n := ast.NewString([]byte(tag))
	n.SetRaw(true)
	return n
}

// shortcodeTextProjection selects which text a shortcode node contributes to its parent.
type shortcodeTextProjection int

const (
	// shortcodeTextSpeech is visible text without an annotation reading.
	shortcodeTextSpeech shortcodeTextProjection = iota
	// shortcodeTextFlat is the mail form: ruby keeps its reading in parentheses.
	shortcodeTextFlat
)

// inlineTextState carries what a projection must skip across consecutive inline nodes.
type inlineTextState struct {
	// hidden is the lower-case name of the raw container whose content is not visible text. The
	// allowlist removes script and style together with their content, so speaking that text would
	// read something the reader never sees.
	hidden string
}

// inlineVisibleText returns the visible text of a parsed body under the given projection.
func inlineVisibleText(n ast.Node, source []byte, projection shortcodeTextProjection) string {
	var b strings.Builder
	state := &inlineTextState{}
	appendInlineVisibleText(&b, n, source, projection, state)
	return b.String()
}

// appendInlineVisibleText walks an inline AST in document order and writes its visible text.
//
// Text is decoded the way the HTML writer decodes it before escaping: backslash escapes are
// removed and character references are resolved, because the source segment and the visible text
// are not the same bytes. Code span content keeps its source literally, as the renderer does.
// Tags contribute nothing except a space for a line break (their inner text still counts), and a
// nested shortcode contributes its own projection, so a spoken ruby around a cloze yields the base
// without the reading. Unknown inline kinds are transparent: walking their children covers GFM
// strikethrough and any later inline container without this function having to know it.
func appendInlineVisibleText(b *strings.Builder, n ast.Node, source []byte, projection shortcodeTextProjection, state *inlineTextState) {
	if state.hidden != "" {
		// Only a matching raw close tag can end the hidden region; the allowlist removes
		// everything between the tags, including Markdown that would otherwise render.
		if raw, ok := n.(*ast.RawHTML); ok {
			appendRawHTMLVisibleText(b, string(raw.Segments.Value(source)), state)
		}
		return
	}

	switch v := n.(type) {
	case *shortcodeNode:
		if projection == shortcodeTextSpeech {
			b.WriteString(v.speech)
		} else {
			b.WriteString(v.flat)
		}
	case *ast.Text:
		if v.IsRaw() {
			b.Write(v.Segment.Value(source))
		} else {
			b.Write(decodeInlineText(v.Segment.Value(source)))
		}
	case *ast.String:
		b.Write(v.Value)
	case *ast.CodeSpan:
		for c := v.FirstChild(); c != nil; c = c.NextSibling() {
			if t, ok := c.(*ast.Text); ok {
				b.Write(t.Segment.Value(source))
			}
		}
	case *ast.AutoLink:
		b.Write(decodeInlineText(v.Label(source)))
	case *ast.RawHTML:
		appendRawHTMLVisibleText(b, string(v.Segments.Value(source)), state)
	default:
		for c := v.FirstChild(); c != nil; c = c.NextSibling() {
			appendInlineVisibleText(b, c, source, projection, state)
		}
	}
}

// appendRawHTMLVisibleText adds what one raw tag contributes to visible text.
//
// A line break is a visible word boundary, so it contributes one space: the renderer emits a
// <br> element, which carries no text node of its own. A script or style open tag starts a region
// whose content the allowlist removes, so it is marked hidden until the matching close tag; both
// names are compared case-insensitively because HTML tag names are.
func appendRawHTMLVisibleText(b *strings.Builder, tag string, state *inlineTextState) {
	name, closing, ok := rawTagName(tag)
	if !ok {
		return
	}
	lower := strings.ToLower(name)
	if state.hidden != "" {
		if closing && lower == state.hidden {
			state.hidden = ""
		}
		return
	}
	if closing {
		return
	}
	switch lower {
	case "script", "style":
		state.hidden = lower
	case "br":
		b.WriteByte(' ')
	}
}

// decodeInlineText applies the transformations the HTML writer applies to text before escaping:
// backslash escapes are removed and character references are resolved. Numeric references go
// before named ones because the writer decides between the two by the character after `&`, and an
// entity name such as `&amp;` must not turn the rest of the source into a second reference.
func decodeInlineText(v []byte) []byte {
	v = util.UnescapePunctuations(v)
	v = util.ResolveNumericReferences(v)
	return util.ResolveEntityNames(v)
}

// expandSpan renders a span shortcode from shortcodeTable.
func expandSpan(name string, def shortcodeDef, rest string, mode shortcodeMode, depth int) *shortcodeNode {
	body, params := splitShortcodeBody(def, rest)
	if body == "" {
		return newShortcodeLiteral(name + ":" + rest)
	}

	values, ok := parseShortcodeParams(def, params)
	if !ok {
		return newShortcodeLiteral(name + ":" + rest)
	}

	if def.bodyRe != nil {
		// A body validated by a pattern is an opaque value, not Markdown. The audio path is the
		// only such body, and a body that does not match its shape stays source text.
		if !def.bodyRe.MatchString(body) {
			return newShortcodeLiteral(name + ":" + rest)
		}
		n := &shortcodeNode{name: name, speech: body, flat: body}
		if mode == shortcodeModeMail {
			n.output = []byte(escapeShortcodeText(body))
			return n
		}
		n.output = spanMarkup(def, values, escapeShortcodeText(body), escapeShortcodeText(body))
		return n
	}

	para, bodySource, ok := parseInlineBody(body, depth)
	if !ok {
		return newShortcodeLiteral(name + ":" + rest)
	}
	for _, nested := range bodyNestedShortcodes(para) {
		if !shortcodeNestingAllowed(name, nested.name) {
			return newShortcodeLiteral(name + ":" + rest)
		}
	}

	fragment, speech, flat, ok := prepareBody(para, bodySource)
	if !ok {
		return newShortcodeLiteral(name + ":" + rest)
	}

	n := &shortcodeNode{name: name, speech: speech, flat: flat}
	// A mail body has no controls to attach, so the shortcode degrades to its visible text.
	if mode == shortcodeModeMail {
		n.output = []byte(escapeShortcodeText(flat))
		return n
	}
	n.output = spanMarkup(def, values, fragment, escapeShortcodeText(speech))
	return n
}

// spanMarkup builds a span shortcode element.
//
// visible is the already-rendered visible fragment and text is the escaped value shared by the
// body and text attributes. Attribute order is fixed by the table, so the output contract stays
// stable regardless of the order the parameter list was written in.
func spanMarkup(def shortcodeDef, values map[string]string, visible, text string) []byte {
	var b strings.Builder
	b.WriteString(`<span class="`)
	b.WriteString(def.class)
	b.WriteByte('"')
	if def.bodyAttr != "" {
		b.WriteString(` `)
		b.WriteString(def.bodyAttr)
		b.WriteString(`="`)
		b.WriteString(text)
		b.WriteByte('"')
	}
	if def.textAttr != "" {
		b.WriteString(` `)
		b.WriteString(def.textAttr)
		b.WriteString(`="`)
		b.WriteString(text)
		b.WriteByte('"')
	}
	for _, p := range def.params {
		v, ok := values[p.key]
		if !ok {
			continue
		}
		b.WriteString(` `)
		b.WriteString(p.attr)
		b.WriteString(`="`)
		b.WriteString(escapeShortcodeText(v))
		b.WriteByte('"')
	}
	for _, f := range def.flags {
		if _, ok := values[f.key]; !ok {
			continue
		}
		b.WriteString(` `)
		b.WriteString(f.attr)
		b.WriteString(`="`)
		b.WriteString(shortcodeFlagValue)
		b.WriteByte('"')
	}
	b.WriteString(`>`)
	b.WriteString(visible)
	b.WriteString(`</span>`)
	return []byte(b.String())
}

// expandRuby renders `{{ruby:base|reading}}`.
//
// The reading is positional, not a key=value parameter: ruby has exactly one extra field and no
// parameters, so an `=` in the reading is malformed rather than an unknown parameter. The base is
// inline Markdown: it may carry emphasis, a code span, or a cloze highlight (the cloze card type
// resolves its own marker before Markdown runs, so the body reaching this function already
// contains `<span class="cloze">…</span>`).
//
// No shortcode may nest inside the base: ruby already uses its extra field for the reading, so a
// base that expands another shortcode makes the whole ruby literal source. In mail mode the
// annotation becomes `base(reading)` with no markup.
func expandRuby(rest string, mode shortcodeMode, depth int) *shortcodeNode {
	base, reading, ok := splitRubyBody(rest)
	if !ok || base == "" {
		return newShortcodeLiteral("ruby:" + rest)
	}
	if reading == "" || strings.ContainsAny(reading, "=|") || strings.Contains(reading, "{{") {
		return newShortcodeLiteral("ruby:" + rest)
	}

	para, source, ok := parseInlineBody(base, depth)
	if !ok {
		return newShortcodeLiteral("ruby:" + rest)
	}
	if len(bodyNestedShortcodes(para)) > 0 {
		return newShortcodeLiteral("ruby:" + rest)
	}

	fragment, speech, flat, ok := prepareBody(para, source)
	if !ok {
		return newShortcodeLiteral("ruby:" + rest)
	}

	node := &shortcodeNode{name: "ruby", speech: speech, flat: flat + "(" + reading + ")"}
	if mode == shortcodeModeMail {
		node.output = []byte(escapeShortcodeText(node.flat))
		return node
	}
	node.output = []byte("<ruby>" + fragment + "<rt>" + escapeShortcodeText(reading) + "</rt></ruby>")
	return node
}

// splitRubyBody splits a ruby body into its base and reading at the first `|` outside a nested
// shortcode, a code span, a backslash escape and a cloze highlight.
//
// Skipping a complete `<span class="cloze">…</span>` region is what keeps a `|` inside the
// highlighted content from splitting the base; the region is followed by depth so a span nested in
// the highlight does not end it early. A cloze region opened but never closed is not a body this
// function can split, so the ruby stays literal. A `|` inside a nested shortcode belongs to that
// shortcode's own parameter list, so brace depth decides which separator is ruby's, and a `|`
// inside a code span or behind a backslash is literal text that Markdown renders as `|`.
func splitRubyBody(rest string) (base, reading string, ok bool) {
	depth := 0
	for i := 0; i < len(rest); i++ {
		if rest[i] == '\\' && i+1 < len(rest) && util.IsPunct(rest[i+1]) {
			i++
			continue
		}
		if rest[i] == '`' {
			if end, found := codeSpanEnd(rest, i); found {
				i = end - 1
				continue
			}
		}
		switch {
		case depth == 0 && strings.HasPrefix(rest[i:], clozeSpanOpen):
			end, found := clozeSpanEnd(rest, i)
			if !found {
				return "", "", false
			}
			i = end - 1
		case strings.HasPrefix(rest[i:], "{{"):
			depth++
			i++
		case depth > 0 && strings.HasPrefix(rest[i:], "}}"):
			depth--
			i++
		case depth == 0 && rest[i] == '|':
			return rest[:i], rest[i+1:], true
		}
	}
	return "", "", false
}

// splitShortcodeBody splits a name's argument text into the visible body and the parameter list.
//
// The rule is: the parameter list starts at the first `|` that sits outside a nested shortcode and
// is followed by a parameter field of this shortcode -- either a `name=` value field, or a bare
// flag word the table declares. A body is free text, so a bare `|` inside it (a table separator, a
// quoted snippet) belongs to the body. The consequence is that a body cannot itself contain the
// sequence `|word=`, or a `|` followed by this shortcode's flag word, because those are exactly
// the separator shapes. A `|` inside a nested `{{…}}` belongs to that shortcode, so brace depth
// decides which one is the separator; a `|` inside a code span or behind a backslash is literal
// text and can never be a separator.
func splitShortcodeBody(def shortcodeDef, rest string) (body, params string) {
	depth := 0
	for i := 0; i < len(rest); i++ {
		if rest[i] == '\\' && i+1 < len(rest) && util.IsPunct(rest[i+1]) {
			i++
			continue
		}
		if rest[i] == '`' {
			if end, ok := codeSpanEnd(rest, i); ok {
				i = end - 1
				continue
			}
		}
		switch {
		case strings.HasPrefix(rest[i:], "{{"):
			depth++
			i++
		case depth > 0 && strings.HasPrefix(rest[i:], "}}"):
			depth--
			i++
		case depth == 0 && rest[i] == '|':
			if isShortcodeParamStart(rest[i+1:]) || isShortcodeFlagWord(def, rest[i+1:]) {
				return rest[:i], rest[i+1:]
			}
		}
	}
	return rest, ""
}

// shortcodeParamStartPattern is the shape that makes a `|` the parameter separator.
var shortcodeParamStartPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*=`)

// isShortcodeParamStart reports whether s begins with a `name=` field.
func isShortcodeParamStart(s string) bool {
	return shortcodeParamStartPattern.MatchString(s)
}

// isShortcodeFlagWord reports whether s begins with one of def's bare flag words as a whole field.
func isShortcodeFlagWord(def shortcodeDef, s string) bool {
	field, _, _ := strings.Cut(s, "|")
	return isShortcodeFlag(def, field)
}

// parseShortcodeParams validates every field of the parameter list and returns the accepted
// values by key.
//
// A field is accepted only when it is a bare word in def.flags, or a key=value whose key is in
// def.params and whose value matches that parameter's pattern. Anything else -- an unknown key, a
// missing value, an empty field, a repeated key, a bare word where a key=value is required -- makes
// the whole shortcode malformed, because silently dropping one field would expand content the
// author did not write.
func parseShortcodeParams(def shortcodeDef, params string) (map[string]string, bool) {
	values := make(map[string]string, len(def.params))
	if params == "" {
		return values, true
	}

	for _, field := range strings.Split(params, "|") {
		if field == "" {
			return nil, false
		}

		key, value, hasValue := strings.Cut(field, "=")
		if !hasValue {
			if !isShortcodeFlag(def, key) {
				return nil, false
			}
			if _, dup := values[key]; dup {
				return nil, false
			}
			values[key] = shortcodeFlagValue
			continue
		}

		spec, ok := findShortcodeParam(def, key)
		if !ok || !spec.re.MatchString(value) {
			return nil, false
		}
		if _, dup := values[key]; dup {
			return nil, false
		}
		values[key] = value
	}
	return values, true
}

// findShortcodeParam returns the parameter spec for key.
func findShortcodeParam(def shortcodeDef, key string) (shortcodeParam, bool) {
	for _, p := range def.params {
		if p.key == key {
			return p, true
		}
	}
	return shortcodeParam{}, false
}

// isShortcodeFlag reports whether key is a bare word flag of def.
func isShortcodeFlag(def shortcodeDef, key string) bool {
	for _, f := range def.flags {
		if f.key == key {
			return true
		}
	}
	return false
}

// shortcodeEscaper escapes the four characters that would break HTML structure when a body or a
// parameter value is written into a text node or a double-quoted attribute value.
//
// It is applied before the sanitizer, so the allowlist never has to absorb an unescaped quote or
// angle bracket; bluemonday is the second line of defence, not the first.
var shortcodeEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
)

// escapeShortcodeText escapes body text and attribute values for HTML.
func escapeShortcodeText(s string) string {
	return shortcodeEscaper.Replace(s)
}

// shortcodeRenderer writes a shortcodeNode's pre-rendered fragment.
type shortcodeRenderer struct{}

func newShortcodeRenderer() renderer.NodeRenderer { return &shortcodeRenderer{} }

// RegisterFuncs implements renderer.NodeRenderer.
func (r *shortcodeRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindShortcode, r.renderShortcode)
}

// renderShortcode writes the fragment raw.
//
// The fragment is built by expandShortcode: its markup is fixed by the shortcode table and every
// text and attribute value in it is escaped, so it is written raw only because the surrounding
// goldmark renderer runs with html.WithUnsafe; writing it as text would escape the markup the
// client needs.
func (r *shortcodeRenderer) renderShortcode(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		n := node.(*shortcodeNode)
		_, _ = w.Write(n.output)
	}
	return ast.WalkSkipChildren, nil
}
