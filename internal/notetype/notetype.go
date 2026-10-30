// Package notetype holds the note type data model shared by the deck editor, the
// template renderer and the importer. A note type is data, not code: its fields,
// templates and options all live in one Config value that is stored as JSON.
package notetype

import (
	"fmt"
	"strings"
)

// Kind is the note type's card-generation mode. A normal note type generates one
// card per template; a cloze note type generates one card per cloze number found
// in its fields.
type Kind string

const (
	KindNormal Kind = "normal"
	KindCloze  Kind = "cloze"
)

// OriginalStockKind records which of Anki's stock note types a note type was
// created from. It is null for a note type that was not created from one.
type OriginalStockKind string

const (
	StockBasic                 OriginalStockKind = "basic"
	StockBasicAndReversed      OriginalStockKind = "basic_and_reversed"
	StockBasicOptionalReversed OriginalStockKind = "basic_optional_reversed"
	StockBasicTyping           OriginalStockKind = "basic_typing"
	StockCloze                 OriginalStockKind = "cloze"
	StockImageOcclusion        OriginalStockKind = "image_occlusion"
)

// AnkiOrigin identifies the Anki note type a config was imported from.
type AnkiOrigin struct {
	NoteTypeID int64 `json:"notetype_id"`
	MTime      int64 `json:"mtime"`
}

// Config is the contents of a note type's config_json column. The note type's
// name is a column of its own, so it is not part of this value.
type Config struct {
	Kind              Kind               `json:"kind"`
	Fields            []Field            `json:"fields"`
	Templates         []Template         `json:"templates"`
	CSS               string             `json:"css"`
	SortFieldID       string             `json:"sort_field_id"`
	LaTeXPre          string             `json:"latex_pre"`
	LaTeXPost         string             `json:"latex_post"`
	LaTeXSVG          bool               `json:"latex_svg"`
	OriginalStockKind *OriginalStockKind `json:"original_stock_kind"`
	AnkiOrigin        *AnkiOrigin        `json:"anki_origin"`
}

// Field is one note field. ID is opaque and stable: renaming or reordering a
// field never changes it, and notes store their values by this id. Tag mirrors
// Anki's nullable field tag value: nil when unset, otherwise the stored integer.
type Field struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Sticky            bool   `json:"sticky"`
	RTL               bool   `json:"rtl"`
	Font              string `json:"font"`
	FontSize          int    `json:"font_size"`
	Description       string `json:"description"`
	PlainText         bool   `json:"plain_text"`
	Collapsed         bool   `json:"collapsed"`
	ExcludeFromSearch bool   `json:"exclude_from_search"`
	Tag               *int   `json:"tag"`
	PreventDeletion   bool   `json:"prevent_deletion"`
}

// Template is one card template. ID is opaque and stable in the same way as
// Field.ID. The browser fields drive the card browser's own rendering.
type Template struct {
	ID              string      `json:"id"`
	Name            string      `json:"name"`
	QFormat         string      `json:"qfmt"`
	AFormat         string      `json:"afmt"`
	BQFormat        string      `json:"bqfmt"`
	BAFormat        string      `json:"bafmt"`
	BrowserFont     string      `json:"browser_font"`
	BrowserFontSize int         `json:"browser_font_size"`
	Answer          *AnswerSpec `json:"answer"`
}

// AnswerSpec binds an answer control to the fields it reads. Control names the
// control (for example "text" or "numeric"), and Bindings maps a role name such
// as "answer" to the id of the field that supplies it.
type AnswerSpec struct {
	Control  string            `json:"control"`
	Bindings map[string]string `json:"bindings"`
}

// Rule is the stable identifier of a validation rule. Callers compare Rule
// values instead of matching error text.
type Rule string

const (
	RuleEmptyNoteTypeName     Rule = "empty_note_type_name"
	RuleInvalidKind           Rule = "invalid_kind"
	RuleEmptyFieldID          Rule = "empty_field_id"
	RuleDuplicateFieldID      Rule = "duplicate_field_id"
	RuleEmptyFieldName        Rule = "empty_field_name"
	RuleDuplicateFieldName    Rule = "duplicate_field_name"
	RuleEmptyTemplateID       Rule = "empty_template_id"
	RuleDuplicateTemplateID   Rule = "duplicate_template_id"
	RuleEmptyTemplateName     Rule = "empty_template_name"
	RuleDuplicateTemplateName Rule = "duplicate_template_name"
	RuleClozeTemplateCount    Rule = "cloze_template_count"
	RuleUnknownField          Rule = "unknown_field"
	// RuleAnswerTypePlaceholder reports a template that carries an answer spec
	// but does not have exactly one type placeholder on its front.
	RuleAnswerTypePlaceholder Rule = "answer_type_placeholder"
)

// ValidationError reports the first rule a note type violates. Detail is English
// and names the offending field, template or reference.
type ValidationError struct {
	Rule   Rule
	Detail string
}

func (e *ValidationError) Error() string {
	return "notetype: " + string(e.Rule) + ": " + e.Detail
}

func validationError(rule Rule, format string, args ...any) *ValidationError {
	return &ValidationError{Rule: rule, Detail: fmt.Sprintf(format, args...)}
}

// ValidateName checks a note type name. The name is a table column rather than
// part of Config, so callers validate it here. A name that is empty after
// trimming surrounding whitespace is rejected.
func ValidateName(name string) error {
	if strings.TrimSpace(name) == "" {
		return validationError(RuleEmptyNoteTypeName, "note type name is empty")
	}
	return nil
}

// Validate checks the rules that hold for every note type: unique non-empty ids
// and names, a known kind, at most one template for a cloze note type, and a
// front template that references only fields the config declares. The note type
// name is validated separately by ValidateName.
func (c Config) Validate() error {
	switch c.Kind {
	case KindNormal, KindCloze:
	default:
		return validationError(RuleInvalidKind, "kind %q is not %q or %q", string(c.Kind), KindNormal, KindCloze)
	}

	fieldIDs := make(map[string]struct{}, len(c.Fields))
	fieldNames := make(map[string]struct{}, len(c.Fields))
	for i, field := range c.Fields {
		if field.ID == "" {
			return validationError(RuleEmptyFieldID, "field %d has an empty id", i+1)
		}
		if _, exists := fieldIDs[field.ID]; exists {
			return validationError(RuleDuplicateFieldID, "field id %q is used by more than one field", field.ID)
		}
		fieldIDs[field.ID] = struct{}{}

		if strings.TrimSpace(field.Name) == "" {
			return validationError(RuleEmptyFieldName, "field %d has an empty name", i+1)
		}
		if _, exists := fieldNames[field.Name]; exists {
			return validationError(RuleDuplicateFieldName, "duplicate field name %q", field.Name)
		}
		fieldNames[field.Name] = struct{}{}
	}

	templateIDs := make(map[string]struct{}, len(c.Templates))
	templateNames := make(map[string]struct{}, len(c.Templates))
	for i, template := range c.Templates {
		if template.ID == "" {
			return validationError(RuleEmptyTemplateID, "template %d has an empty id", i+1)
		}
		if _, exists := templateIDs[template.ID]; exists {
			return validationError(RuleDuplicateTemplateID, "template id %q is used by more than one template", template.ID)
		}
		templateIDs[template.ID] = struct{}{}

		if strings.TrimSpace(template.Name) == "" {
			return validationError(RuleEmptyTemplateName, "template %d has an empty name", i+1)
		}
		if _, exists := templateNames[template.Name]; exists {
			return validationError(RuleDuplicateTemplateName, "duplicate template name %q", template.Name)
		}
		templateNames[template.Name] = struct{}{}
	}

	if c.Kind == KindCloze && len(c.Templates) > 1 {
		return validationError(RuleClozeTemplateCount, "cloze note type has %d templates; at most one is allowed", len(c.Templates))
	}

	for _, template := range c.Templates {
		for _, reference := range ReferencedFields(template.QFormat) {
			if _, exists := fieldNames[reference]; !exists {
				return validationError(RuleUnknownField, "template %q front references unknown field %q", template.Name, reference)
			}
		}
	}

	// A template that grades an answer needs exactly one place to put the input
	// control. The count is taken from the front template text, not from the
	// rendered output, because a template that never renders the placeholder
	// must be rejected before any card is generated.
	for _, template := range c.Templates {
		if template.Answer == nil {
			continue
		}
		if count := typePlaceholderCount(template.QFormat); count != 1 {
			return validationError(RuleAnswerTypePlaceholder,
				"template %q front has %d type placeholders; exactly one is required",
				template.Name, count)
		}
	}

	return nil
}

// typePlaceholderCount counts the type-in placeholders in a front template.
// A placeholder is a replacement whose filter chain contains the type filter,
// so all three written forms count: {{type:Field}}, {{cloze:type:Field}} and
// {{type-nc:Field}}. The filter chains are read with the same splitting the
// renderer uses, which keeps the count in step with what actually renders.
func typePlaceholderCount(template string) int {
	count := 0
	for _, token := range lexTemplate(template) {
		if token.kind != tokenReplacement {
			continue
		}
		_, filters := splitReplacement(token.body)
		for _, filter := range filters {
			if filter == "type" || filter == "type-nc" || filter == "type-cloze" {
				count++
				break
			}
		}
	}
	return count
}

// specialFields are template names Anki fills from the card or its context. They
// are not note fields, so they never take part in field validation.
var specialFields = map[string]struct{}{
	"FrontSide": {},
	"Tags":      {},
	"Type":      {},
	"Deck":      {},
	"Subdeck":   {},
	"Card":      {},
	"CardFlag":  {},
	"CardID":    {},
}

// ReferencedFields returns the field names a front template refers to, in order
// of first appearance and without duplicates. It recognises replacements,
// conditionals, filter chains and special fields, and ignores HTML comments and
// inline cloze text.
//
// This scanner is deliberately small and is not the template parser: it only has
// to answer whether a template names a field that does not exist. The template
// parser replaces its internals while keeping this signature, so callers must not
// depend on more syntax than the rules below. It does not follow a delimiter
// change: a template that starts with {{=<% %>=}} yields no references after the
// declaration.
func ReferencedFields(template string) []string {
	template = withoutHTMLComments(template)

	var references []string
	seen := make(map[string]struct{})
	for offset := 0; ; {
		start := strings.Index(template[offset:], "{{")
		if start < 0 {
			break
		}
		start += offset
		end := strings.Index(template[start+2:], "}}")
		if end < 0 {
			break
		}
		end += start + 2

		if name := fieldNameInTag(template[start+2 : end]); name != "" {
			if _, exists := seen[name]; !exists {
				seen[name] = struct{}{}
				references = append(references, name)
			}
		}
		offset = end + 2
	}
	return references
}

// fieldNameInTag extracts the field name from one template tag body, or returns
// an empty string when the tag does not name a field.
func fieldNameInTag(tag string) string {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return ""
	}
	switch tag[0] {
	case '=', '/':
		return "" // delimiter declaration or closing conditional
	case '#', '^':
		tag = strings.TrimSpace(tag[1:])
	}

	// Inline cloze markup ({{c1::text}}) carries content, not a field name.
	if strings.Contains(tag, "::") {
		return ""
	}
	// A filter chain separates filters from the field with colons.
	if colon := strings.LastIndexByte(tag, ':'); colon >= 0 {
		tag = strings.TrimSpace(tag[colon+1:])
	}
	if tag == "" {
		return ""
	}
	if isClozeNumber(tag) {
		return ""
	}
	if _, special := specialFields[tag]; special {
		return ""
	}
	return tag
}

// isClozeNumber reports whether s is a cloze number such as c1 or c1,2. Those
// names are card selectors, not fields.
func isClozeNumber(s string) bool {
	if len(s) < 2 || s[0] != 'c' {
		return false
	}
	hasDigit := false
	for i := 1; i < len(s); i++ {
		switch {
		case s[i] >= '0' && s[i] <= '9':
			hasDigit = true
		case s[i] == ',':
		default:
			return false
		}
	}
	return hasDigit
}

// withoutHTMLComments removes HTML comments, whose contents Anki renders
// literally and therefore does not treat as template syntax.
func withoutHTMLComments(s string) string {
	for {
		start := strings.Index(s, "<!--")
		if start < 0 {
			return s
		}
		end := strings.Index(s[start+4:], "-->")
		if end < 0 {
			return s[:start]
		}
		s = s[:start] + s[start+4+end+3:]
	}
}
