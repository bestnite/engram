// Copyright: Ankitects Pty Ltd and contributors
// License: GNU AGPL, version 3 or later; http://www.gnu.org/licenses/agpl.html
//
// Ported from rslib/src/template.rs at tag 26.09.3 of ankitects/anki. The lexer,
// the parser, the conditional rules and the field-empty test keep Anki's
// behaviour. One part is deliberately left out: Anki can stop at the first
// filter it does not know and let its Python renderer finish the job. Nothing
// in this repository calls back into Python, so every template is rendered
// completely and an unknown filter is skipped instead.

package notetype

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Side selects which template of a card is rendered.
type Side int

const (
	// SideFront is the question: the front template.
	SideFront Side = iota
	// SideBack is the answer: the back template.
	SideBack
)

func (s Side) String() string {
	if s == SideBack {
		return "back"
	}
	return "front"
}

// RenderData is everything a card contributes to rendering besides its note
// type and template.
//
// Fields maps field ids to field values. Tags, TypeName, DeckName, SubdeckName,
// CardName, CardFlag and CardID supply the special fields. ClozeOrdinal is the
// 1-based cloze number of a cloze card and zero for every other card; a normal
// note type uses its template position as the number, as Anki does.
//
// A note field whose name matches a special field wins, because Anki fills the
// special fields only when the note does not define a field of that name.
type RenderData struct {
	Fields       map[string]string
	Tags         []string
	TypeName     string
	DeckName     string // full deck path, for example "Parent::Child"
	SubdeckName  string // leaf deck name, for example "Child"
	CardName     string // template name
	CardFlag     string // for example "flag0"
	CardID       string
	ClozeOrdinal int
}

// ErrorCode is the stable identifier of a rendering failure. A caller compares
// codes and produces the user-facing message from its translation catalog;
// Detail is English and only meant for logs and diagnostics.
type ErrorCode string

const (
	// CodeUnknownField reports a replacement whose name is neither a note
	// field, a special field nor a cloze number.
	CodeUnknownField ErrorCode = "unknown_field"
	// CodeUnknownConditional reports a conditional on a name that is neither a
	// note field, a special field nor a cloze number.
	CodeUnknownConditional ErrorCode = "unknown_conditional"
	// CodeUnclosedConditional reports a conditional that is opened and never
	// closed.
	CodeUnclosedConditional ErrorCode = "unclosed_conditional"
	// CodeConditionalNotOpen reports a closing tag that does not match the
	// conditional that is open at that point.
	CodeConditionalNotOpen ErrorCode = "conditional_not_open"
	// CodeEmptyFront reports a front template that cannot render because no
	// field it references holds content.
	CodeEmptyFront ErrorCode = "empty_front"
	// CodeMissingCloze reports a cloze card whose cloze number appears in no
	// field.
	CodeMissingCloze ErrorCode = "missing_cloze"
)

// RenderError reports why a template could not be rendered. Field names the
// offending field or conditional when there is one.
type RenderError struct {
	Code   ErrorCode
	Field  string
	Detail string
}

func (e *RenderError) Error() string {
	return "notetype: " + string(e.Code) + ": " + e.Detail
}

func renderError(code ErrorCode, field, format string, args ...any) *RenderError {
	return &RenderError{Code: code, Field: field, Detail: fmt.Sprintf(format, args...)}
}

// CardTemplate resolves the template a stored card uses.
//
// cardTemplate is the value of cards.template: a template id for a normal note
// type and "cloze:<n>" for a cloze note type. The returned clozeNumber is the
// 1-based cloze number of a cloze card and zero for every other card. A
// template the config does not declare, or a cloze value that does not name a
// positive number, is an error.
func CardTemplate(cfg Config, cardTemplate string) (Template, int, error) {
	if cfg.Kind == KindCloze {
		if len(cfg.Templates) == 0 {
			return Template{}, 0, fmt.Errorf("notetype: cloze note type declares no template")
		}
		digits, ok := strings.CutPrefix(cardTemplate, clozeTemplatePrefix)
		if !ok {
			return Template{}, 0, fmt.Errorf("notetype: card template %q is not a cloze card", cardTemplate)
		}
		number, err := strconv.Atoi(digits)
		if err != nil || number < 1 {
			return Template{}, 0, fmt.Errorf("notetype: card template %q has no positive cloze number", cardTemplate)
		}
		return cfg.Templates[0], number, nil
	}
	for _, template := range cfg.Templates {
		if template.ID == cardTemplate {
			return template, 0, nil
		}
	}
	return Template{}, 0, fmt.Errorf("notetype: note type declares no template %q", cardTemplate)
}

// Clone returns a copy of the config whose slices can be modified without
// affecting the receiver. The renderer and the store only read a config, so a
// clone is needed only where a caller edits a decoded copy.
func (c Config) Clone() Config {
	clone := c
	clone.Fields = append([]Field(nil), c.Fields...)
	clone.Templates = make([]Template, len(c.Templates))
	for i, template := range c.Templates {
		clone.Templates[i] = template
		if template.Answer != nil {
			answer := *template.Answer
			answer.Bindings = make(map[string]string, len(template.Answer.Bindings))
			for role, fieldID := range template.Answer.Bindings {
				answer.Bindings[role] = fieldID
			}
			clone.Templates[i].Answer = &answer
		}
	}
	return clone
}

// Render expands one side of a card template.
//
// The front side is the question. The back side is the answer, and it renders
// the front first with the same data so that {{FrontSide}} expands to the
// question text; {{FrontSide}} on the front side expands to the empty string,
// which is what Anki does because some templates rely on it.
//
// Render returns the template output and nothing else. It does not decide
// whether a card may be shown: a front whose content is empty renders as the
// empty string, and FrontProblem reports that condition, because Anki builds
// the "front is blank" message in its collection layer rather than in the
// template engine. Callers that need Anki's exact question text therefore
// append that message themselves.
//
// Render is safe to call concurrently: it only reads its arguments.
func Render(cfg Config, tmpl Template, side Side, data RenderData) (string, error) {
	values, ordinal := renderFieldValues(cfg, tmpl, data)
	ctx := &renderContext{
		fields:      values,
		nonEmpty:    nonEmptyFieldNames(values),
		cardOrdinal: ordinal,
		onFront:     side == SideFront,
	}
	format := tmpl.QFormat
	if side == SideBack {
		front, err := renderFormat(tmpl, tmpl.QFormat, SideFront, ctx)
		if err != nil {
			return "", err
		}
		ctx.frontSide = front
		ctx.onFront = false
		format = tmpl.AFormat
	}
	return renderFormat(tmpl, format, side, ctx)
}

// RenderAnswer renders the answer side with frontSide standing in for {{FrontSide}}.
//
// The caller passes the question text it already holds, so the answer text can contain a form of the question that
// differs from a fresh render — the review path inserts question text whose audio tags are already extracted markers.
// A template may use {{FrontSide}} on the front; RenderAnswer renders the answer only, so that case has no meaning here.
func RenderAnswer(cfg Config, tmpl Template, data RenderData, frontSide string) (string, error) {
	values, ordinal := renderFieldValues(cfg, tmpl, data)
	ctx := &renderContext{
		fields:      values,
		nonEmpty:    nonEmptyFieldNames(values),
		cardOrdinal: ordinal,
		frontSide:   frontSide,
		onFront:     false,
	}
	return renderFormat(tmpl, tmpl.AFormat, SideBack, ctx)
}

// FrontProblem reports why a front side cannot be shown, or nil when it can.
// Both conditions Anki draws a message for are reported here so that the
// caller can localise the matching message: a cloze card whose cloze number
// appears in no field is missing_cloze, and a normal card whose front references
// no non-empty field is empty_front. Which of the two applies is decided by the
// note type, in the same order Anki checks them.
func FrontProblem(cfg Config, tmpl Template, data RenderData) *RenderError {
	if cfg.Kind == KindCloze {
		// A cloze card without a cloze number is a caller error; there is no
		// card to report on.
		if data.ClozeOrdinal <= 0 {
			return nil
		}
		for _, value := range data.Fields {
			if clozeNumbersIn(value)[data.ClozeOrdinal] {
				return nil
			}
		}
		return renderError(CodeMissingCloze, "",
			"template %q: no field contains a cloze deletion for ordinal %d",
			tmpl.Name, data.ClozeOrdinal)
	}
	if FrontIsEmpty(cfg, tmpl, data) {
		return renderError(CodeEmptyFront, "",
			"template %q: the front references no non-empty field", tmpl.Name)
	}
	return nil
}

// FrontIsEmpty reports whether the front template renders without any non-empty
// field, which is Anki's test for a blank front. Text alone does not count: a
// template whose only content is literal HTML is still blank. A template that
// does not parse is not reported as empty, because Render reports the parse
// error instead.
func FrontIsEmpty(cfg Config, tmpl Template, data RenderData) bool {
	nodes, err := parseTemplate(tmpl.QFormat, tmpl.Name, SideFront)
	if err != nil {
		return false
	}
	values, _ := renderFieldValues(cfg, tmpl, data)
	return templateIsEmpty(nodes, nonEmptyFieldNames(values), true)
}

// templateIsEmpty reports whether every node of a template is a reference to a
// non-empty field. Negated conditionals are treated as a reference to their
// content when checkNegated is false, which the legacy required-field cache
// needs; rendering always uses checkNegated true.
func templateIsEmpty(nodes []templateNode, nonEmpty map[string]bool, checkNegated bool) bool {
	for _, node := range nodes {
		switch node.kind {
		case tokenText, tokenComment:
			continue
		case tokenReplacement:
			if nonEmpty[node.key] {
				return false
			}
		case tokenOpenConditional:
			if node.negated {
				if !checkNegated || !nonEmpty[node.key] {
					if !templateIsEmpty(node.children, nonEmpty, checkNegated) {
						return false
					}
				}
				continue
			}
			if !nonEmpty[node.key] {
				continue
			}
			if !templateIsEmpty(node.children, nonEmpty, checkNegated) {
				return false
			}
		}
	}
	return true
}

// renderFieldValues builds the name-to-value map a template is rendered
// against. Note fields come first so they win over the special fields, and the
// cloze number of the current card is added last so that {{#c1}} selects the
// card's own cloze deletion.
func renderFieldValues(cfg Config, tmpl Template, data RenderData) (map[string]string, int) {
	values := make(map[string]string, len(cfg.Fields)+9)
	for _, field := range cfg.Fields {
		values[field.Name] = data.Fields[field.ID]
	}
	setIfAbsent := func(name, value string) {
		if _, exists := values[name]; !exists {
			values[name] = value
		}
	}
	setIfAbsent("Tags", strings.Join(data.Tags, " "))
	setIfAbsent("Type", data.TypeName)
	setIfAbsent("Deck", data.DeckName)
	setIfAbsent("Subdeck", data.SubdeckName)
	setIfAbsent("Card", data.CardName)
	setIfAbsent("CardFlag", data.CardFlag)
	setIfAbsent("CardID", data.CardID)

	ordinal := cardOrdinal(cfg, tmpl, data)
	setIfAbsent("c"+strconv.Itoa(ordinal), "1")
	return values, ordinal
}

// cardOrdinal returns the 1-based card number used for the cloze term of the
// template. A cloze card supplies its own number; every other card uses its
// template position, which is what Anki stores as the card's template index.
func cardOrdinal(cfg Config, tmpl Template, data RenderData) int {
	if data.ClozeOrdinal > 0 {
		return data.ClozeOrdinal
	}
	for i, candidate := range cfg.Templates {
		if candidate.ID == tmpl.ID {
			return i + 1
		}
	}
	for i, candidate := range cfg.Templates {
		if candidate.Name == tmpl.Name {
			return i + 1
		}
	}
	return 1
}

func nonEmptyFieldNames(values map[string]string) map[string]bool {
	names := make(map[string]bool, len(values))
	for name, value := range values {
		if !fieldIsEmpty(value) {
			names[name] = true
		}
	}
	return names
}

// fieldIsEmptyPattern matches text that holds only whitespace and empty <br> or
// <div> tags. A field with that content counts as empty for conditionals and
// for the blank-front check.
var fieldIsEmptyPattern = regexp.MustCompile(`(?is)^(?:[[:space:]]|</?(?:br|div) ?/?>)*$`)

func fieldIsEmpty(text string) bool {
	return fieldIsEmptyPattern.MatchString(text)
}

// renderContext is the state one side of a card is rendered with.
type renderContext struct {
	fields      map[string]string // field name to value, including specials
	nonEmpty    map[string]bool   // field names with non-empty content
	cardOrdinal int               // 1-based card number
	frontSide   string            // rendered front, used by {{FrontSide}}
	onFront     bool              // true while the question side is rendered
}

// isClozeConditional reports whether a conditional names a cloze number, such
// as c1 or c12. Those conditions test the card, not a note field.
func isClozeConditional(name string) bool {
	digits, ok := strings.CutPrefix(name, "c")
	if !ok || digits == "" {
		return false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (ctx *renderContext) evaluateConditional(name string, negated bool, tmplName string, side Side) (bool, error) {
	if ctx.nonEmpty[name] {
		return !negated, nil
	}
	if _, known := ctx.fields[name]; known || isClozeConditional(name) {
		return negated, nil
	}
	prefix := "#"
	if negated {
		prefix = "^"
	}
	return false, renderError(CodeUnknownConditional, name,
		"template %q %s uses %q on a name that is not a field or a cloze number",
		tmplName, side, prefix+name)
}

// Lexing
//----------------------------------------

const (
	commentStart = "<!--"
	commentEnd   = "-->"
	// altDelimiterDirective switches the template to <% %> delimiters.
	altDelimiterDirective = "{{=<% %>=}}"
)

type tokenKind int

const (
	tokenText tokenKind = iota
	tokenComment
	tokenReplacement
	tokenOpenConditional
	tokenOpenNegated
	tokenCloseConditional
)

type templateToken struct {
	kind tokenKind
	body string // literal text for text and comment tokens, the tag body otherwise
}

type templateDelimiters struct {
	open  string
	close string
}

func lexTemplate(template string) []templateToken {
	delimiters := templateDelimiters{open: "{{", close: "}}"}
	trimmed := strings.TrimLeftFunc(template, unicode.IsSpace)
	if strings.HasPrefix(trimmed, altDelimiterDirective) {
		// Anki removes every leading copy of the directive, not only the first.
		for strings.HasPrefix(trimmed, altDelimiterDirective) {
			trimmed = trimmed[len(altDelimiterDirective):]
		}
		template = trimmed
		delimiters = templateDelimiters{open: "<%", close: "%>"}
	}

	var tokens []templateToken
	rest := template
	for rest != "" {
		var token templateToken
		rest, token = nextToken(rest, delimiters)
		tokens = append(tokens, token)
	}
	return tokens
}

// nextToken returns the next text, comment or handlebar token. A handlebar or
// comment only starts a token when its opening delimiter is followed by a
// closing one; otherwise the delimiter stays literal text, as in Anki.
func nextToken(input string, delimiters templateDelimiters) (string, templateToken) {
	for i := 0; i < len(input); {
		remaining := input[i:]
		if rest, token, ok := handlebarToken(remaining, delimiters); ok {
			if i == 0 {
				return rest, token
			}
			return remaining, templateToken{kind: tokenText, body: input[:i]}
		}
		if rest, token, ok := commentToken(remaining); ok {
			if i == 0 {
				return rest, token
			}
			return remaining, templateToken{kind: tokenText, body: input[:i]}
		}
		_, size := utf8.DecodeRuneInString(input[i:])
		i += size
	}
	return "", templateToken{kind: tokenText, body: input}
}

func handlebarToken(input string, delimiters templateDelimiters) (string, templateToken, bool) {
	if !strings.HasPrefix(input, delimiters.open) {
		return input, templateToken{}, false
	}
	body := input[len(delimiters.open):]
	end := strings.Index(body, delimiters.close)
	if end < 0 {
		return input, templateToken{}, false
	}
	return body[end+len(delimiters.close):], classifyHandle(body[:end]), true
}

func commentToken(input string) (string, templateToken, bool) {
	if !strings.HasPrefix(input, commentStart) {
		return input, templateToken{}, false
	}
	body := input[len(commentStart):]
	end := strings.Index(body, commentEnd)
	if end < 0 {
		return input, templateToken{}, false
	}
	return body[end+len(commentEnd):], templateToken{kind: tokenComment, body: body[:end]}, true
}

// classifyHandle decides from the first character whether a handlebar opens a
// conditional, closes one, or is a replacement.
func classifyHandle(handle string) templateToken {
	// Anki strips leading braces before trimming the surrounding whitespace, so
	// a tag such as "{{ {Front} }}" stays a replacement of "{Front}".
	start := strings.TrimSpace(strings.TrimLeft(handle, "{"))
	if len(start) < 2 {
		return templateToken{kind: tokenReplacement, body: start}
	}
	switch start[0] {
	case '#':
		return templateToken{kind: tokenOpenConditional, body: strings.TrimLeftFunc(start[1:], unicode.IsSpace)}
	case '/':
		return templateToken{kind: tokenCloseConditional, body: strings.TrimLeftFunc(start[1:], unicode.IsSpace)}
	case '^':
		return templateToken{kind: tokenOpenNegated, body: strings.TrimLeftFunc(start[1:], unicode.IsSpace)}
	default:
		return templateToken{kind: tokenReplacement, body: start}
	}
}

// Parsing
//----------------------------------------

// templateNode is one node of a parsed template. The fields used depend on
// kind, which stays a token kind so the lexer and parser agree on node types.
type templateNode struct {
	kind     tokenKind
	body     string // text or comment content
	key      string // replacement key or conditional name
	filters  []string
	negated  bool
	children []templateNode
}

func parseTemplate(template, tmplName string, side Side) ([]templateNode, error) {
	parser := templateParser{tokens: lexTemplate(template), tmplName: tmplName, side: side}
	nodes, err := parser.parse("", false, false)
	if err != nil {
		return nil, err
	}
	return nodes, nil
}

type templateParser struct {
	tokens   []templateToken
	pos      int
	tmplName string
	side     Side
}

// parse reads nodes until the conditional named openKey closes, or until the
// input ends when openKey is empty.
func (p *templateParser) parse(openKey string, openNegated, insideConditional bool) ([]templateNode, error) {
	var nodes []templateNode
	for p.pos < len(p.tokens) {
		token := p.tokens[p.pos]
		p.pos++

		switch token.kind {
		case tokenText:
			nodes = append(nodes, templateNode{kind: tokenText, body: token.body})
		case tokenComment:
			nodes = append(nodes, templateNode{kind: tokenComment, body: token.body})
		case tokenReplacement:
			key, filters := splitReplacement(token.body)
			nodes = append(nodes, templateNode{kind: tokenReplacement, key: key, filters: filters})
		case tokenOpenConditional, tokenOpenNegated:
			children, err := p.parse(token.body, token.kind == tokenOpenNegated, true)
			if err != nil {
				return nil, err
			}
			nodes = append(nodes, templateNode{
				kind:     tokenOpenConditional,
				key:      token.body,
				negated:  token.kind == tokenOpenNegated,
				children: children,
			})
		case tokenCloseConditional:
			if insideConditional && openKey == token.body {
				return nodes, nil
			}
			if !insideConditional {
				return nil, renderError(CodeConditionalNotOpen, token.body,
					"template %q %s: the closing tag %q is not inside a conditional",
					p.tmplName, p.side, "{{/"+token.body+"}}")
			}
			open := "{{#" + openKey + "}}"
			if openNegated {
				open = "{{^" + openKey + "}}"
			}
			return nil, renderError(CodeConditionalNotOpen, token.body,
				"template %q %s: %q closes the conditional opened by %q",
				p.tmplName, p.side, "{{/"+token.body+"}}", open)
		}
	}

	if insideConditional {
		return nil, renderError(CodeUnclosedConditional, openKey,
			"template %q %s: the conditional %q is never closed",
			p.tmplName, p.side, openKey)
	}
	return nodes, nil
}

// splitReplacement separates the field name from its filters. Filters are
// written between the field and the opening braces, so splitting from the right
// keeps a field name that contains a colon intact, and the filters come out in
// the order they have to be applied.
func splitReplacement(body string) (string, []string) {
	parts := strings.Split(body, ":")
	key := parts[len(parts)-1]
	if len(parts) == 1 {
		return key, nil
	}
	filters := make([]string, 0, len(parts)-1)
	for i := len(parts) - 2; i >= 0; i-- {
		filters = append(filters, parts[i])
	}
	return key, filters
}

// Rendering
//----------------------------------------

func renderFormat(tmpl Template, format string, side Side, ctx *renderContext) (string, error) {
	nodes, err := parseTemplate(format, tmpl.Name, side)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := renderNodes(&b, nodes, tmpl.Name, side, ctx); err != nil {
		return "", err
	}
	return b.String(), nil
}

func renderNodes(b *strings.Builder, nodes []templateNode, tmplName string, side Side, ctx *renderContext) error {
	for _, node := range nodes {
		switch node.kind {
		case tokenText:
			b.WriteString(node.body)
		case tokenComment:
			// A comment is template text, so its delimiters come back verbatim
			// and anything inside stays unexpanded.
			b.WriteString(commentStart)
			b.WriteString(node.body)
			b.WriteString(commentEnd)
		case tokenReplacement:
			if err := renderReplacement(b, node, tmplName, side, ctx); err != nil {
				return err
			}
		default: // conditional
			active, err := ctx.evaluateConditional(node.key, node.negated, tmplName, side)
			if err != nil {
				return err
			}
			if active {
				if err := renderNodes(b, node.children, tmplName, side, ctx); err != nil {
					return err
				}
				continue
			}
			// The discarded branch is still rendered so that an unknown field
			// inside it is reported instead of silently skipped.
			var discard strings.Builder
			if err := renderNodes(&discard, node.children, tmplName, side, ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

func renderReplacement(b *strings.Builder, node templateNode, tmplName string, side Side, ctx *renderContext) error {
	// {{FrontSide}} carries the question text into the answer template. Any
	// filter written on it is ignored, matching Anki.
	if node.key == "FrontSide" {
		b.WriteString(ctx.frontSide)
		return nil
	}

	// An empty field name with a filter passes the empty string through the
	// filters, so a filter that generates its own output still contributes.
	if node.key == "" && len(node.filters) > 0 {
		b.WriteString(applyFilters("", node.filters, "", ctx))
		return nil
	}

	text, exists := ctx.fields[node.key]
	if !exists {
		return renderError(CodeUnknownField, node.key,
			"template %q %s references the unknown field %q", tmplName, side, node.key)
	}
	b.WriteString(applyFilters(text, node.filters, node.key, ctx))
	return nil
}
