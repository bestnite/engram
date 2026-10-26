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
//   - one line, no nesting, and the body ends at the first `}}` on the line;
//   - an unknown name, a malformed parameter, or a body that itself contains `{{` is emitted as
//     its source text, so a shortcode never makes a render fail;
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

// shortcodeNode holds the already-rendered fragment for one shortcode.
type shortcodeNode struct {
	ast.BaseInline
	output []byte
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
func (s *shortcodeParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	line, _ := block.PeekLine()
	if !bytes.HasPrefix(line, []byte("{{")) {
		return nil
	}
	end := bytes.Index(line, []byte("}}"))
	if end < 0 {
		// No closing brace on this line: not a shortcode, so the text is parsed normally.
		return nil
	}

	inner := line[2:end]
	mode := shortcodeModeCard
	if v, ok := pc.Get(shortcodeModeKey).(shortcodeMode); ok {
		mode = v
	}

	output := expandShortcode(inner, mode)
	block.Advance(end + 2)
	return &shortcodeNode{output: output}
}

// expandShortcode turns the bytes between `{{` and `}}` into the output fragment for mode.
//
// Every rejection path returns the source text, escaped: a shortcode is content, and content must
// never be able to make the render fail or disappear.
func expandShortcode(inner []byte, mode shortcodeMode) []byte {
	content := string(inner)
	// A shortcode does not nest: a body that opens another `{{` is emitted as source text instead
	// of being read as a name, so no shortcode can ever expand another shortcode's payload.
	if strings.Contains(content, "{{") {
		return shortcodeLiteral(content)
	}

	name, rest, hasBody := strings.Cut(content, ":")
	if !hasBody {
		return shortcodeLiteral(content)
	}

	if name == "ruby" {
		return expandRuby(rest, mode)
	}

	def, ok := shortcodeTable[name]
	if !ok {
		return shortcodeLiteral(content)
	}
	return expandSpan(name, def, rest, mode)
}

// expandRuby renders `{{ruby:base|reading}}`.
//
// The reading is positional, not a key=value parameter: ruby has exactly one extra field and no
// parameters, so an `=` in either half is malformed rather than an unknown parameter.
func expandRuby(rest string, mode shortcodeMode) []byte {
	base, reading, found := strings.Cut(rest, "|")
	if !found || base == "" || reading == "" {
		return shortcodeLiteral("ruby:" + rest)
	}
	if strings.ContainsAny(base, "=|") || strings.ContainsAny(reading, "=|") {
		return shortcodeLiteral("ruby:" + rest)
	}

	if mode == shortcodeModeMail {
		return []byte(escapeShortcodeText(base + "(" + reading + ")"))
	}
	return []byte("<ruby>" + escapeShortcodeText(base) + "<rt>" + escapeShortcodeText(reading) + "</rt></ruby>")
}

// expandSpan renders a span shortcode from shortcodeTable.
func expandSpan(name string, def shortcodeDef, rest string, mode shortcodeMode) []byte {
	body, params := splitShortcodeBody(def, rest)
	if body == "" {
		return shortcodeLiteral(name + ":" + rest)
	}

	values, ok := parseShortcodeParams(def, params)
	if !ok {
		return shortcodeLiteral(name + ":" + rest)
	}

	if def.bodyRe != nil && !def.bodyRe.MatchString(body) {
		return shortcodeLiteral(name + ":" + rest)
	}

	// A mail body has no controls to attach, so the shortcode degrades to its visible text: the
	// body for a span shortcode, and the annotated form for ruby (handled above).
	if mode == shortcodeModeMail {
		return []byte(escapeShortcodeText(body))
	}

	var b strings.Builder
	b.WriteString(`<span class="`)
	b.WriteString(def.class)
	b.WriteByte('"')
	if def.bodyAttr != "" {
		b.WriteString(` `)
		b.WriteString(def.bodyAttr)
		b.WriteString(`="`)
		b.WriteString(escapeShortcodeText(body))
		b.WriteByte('"')
	}
	if def.textAttr != "" {
		b.WriteString(` `)
		b.WriteString(def.textAttr)
		b.WriteString(`="`)
		b.WriteString(escapeShortcodeText(body))
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
	b.WriteString(escapeShortcodeText(body))
	b.WriteString(`</span>`)
	return []byte(b.String())
}

// splitShortcodeBody splits a name's argument text into the visible body and the parameter list.
//
// The rule is: the parameter list starts at the first `|` that is followed by a parameter field of
// this shortcode -- either a `name=` value field, or a bare flag word the table declares. A body is
// free text, so a bare `|` inside it (a table separator, a quoted snippet) belongs to the body.
// The consequence is that a body cannot itself contain the sequence `|word=`, or a `|` followed by
// this shortcode's flag word, because those are exactly the separator shapes. The rule is stated
// here because the grammar gives no other way to tell the two apart.
func splitShortcodeBody(def shortcodeDef, rest string) (body, params string) {
	for i := 0; i < len(rest); i++ {
		if rest[i] != '|' {
			continue
		}
		if isShortcodeParamStart(rest[i+1:]) || isShortcodeFlagWord(def, rest[i+1:]) {
			return rest[:i], rest[i+1:]
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

// shortcodeLiteral renders the source form of a rejected shortcode as escaped text.
func shortcodeLiteral(content string) []byte {
	return []byte(escapeShortcodeText("{{" + content + "}}"))
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
// The fragment is already escaped by expandShortcode, and every attribute in it is fixed by the
// shortcode table, so it is written raw only because the surrounding goldmark renderer runs with
// html.WithUnsafe; writing it as text would escape the markup the client needs.
func (r *shortcodeRenderer) renderShortcode(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		n := node.(*shortcodeNode)
		_, _ = w.Write(n.output)
	}
	return ast.WalkSkipChildren, nil
}
