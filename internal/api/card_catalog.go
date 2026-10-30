package api

// This file is the transitional card-type catalog.
//
// A note type is now data (internal/notetype), and a card's answer input is rendered by an answer control
// (internal/answer). The SPA still reads the old self-description shape from GET /api/v1/card-types to draw its
// editor forms and the review page's answer controls, and REST/MCP clients still send a `kind` field, so this file
// keeps that shape one more milestone.
//
// The table is deliberately a table: it maps one engram stock kind to the label, the answer control vocabulary the
// SPA knows, and the field table the editor draws. It lives in internal/api rather than in a package of its own
// because it has no behaviour, only data. It is removed when the note-type endpoints and the frame runtime replace
// these payloads.

// Field control values: they correspond one-to-one with the controls of the front-end edit form.
const (
	controlText     = "text"     // single-line text
	controlTextarea = "textarea" // multi-line text
	controlLines    = "lines"    // multi-line text, one array element per line
	controlNumber   = "number"   // number
	controlBool     = "bool"     // boolean
	controlIndex    = "index"    // single select: takes an options index
	controlIndexes  = "indexes"  // multi select: takes a set of options indices
)

// Answer control values: they decide whether the review page gives input controls or the four-level self-rating.
const (
	answerNone   = "none"   // self-graded card type, no answer control
	answerText   = "text"   // text answer
	answerNumber = "number" // numeric answer
	answerSingle = "single" // single select
	answerMulti  = "multi"  // multi select
	answerBool   = "bool"   // true/false card
	answerBlanks = "blanks" // per-blank input (cloze card): one text box per target cloze
	answerItems  = "items"  // per-item input (list card): one text box per item
	answerEssay  = "essay"  // free-form writing followed by self-rating (short answer)
)

// cardFieldSpec describes the shape of one field in the edit form.
type cardFieldSpec struct {
	Key      string `json:"key"`
	Control  string `json:"control"`
	Required bool   `json:"required"`
	// Default is the server-side default value for boolean fields that have one; omitted when there is none.
	Default any `json:"default,omitempty"`
}

// cardKindDescription is one card kind's self-description, in the shape the SPA and MCP clients read.
type cardKindDescription struct {
	Kind          string          `json:"kind"`
	LabelKey      string          `json:"label_key"`
	Graded        bool            `json:"graded"`
	AnswerControl string          `json:"answer_control"`
	FrontField    string          `json:"front_field"`
	BackField     string          `json:"back_field"`
	PromptField   string          `json:"prompt_field"`
	OptionsField  string          `json:"options_field"`
	Fields        []cardFieldSpec `json:"fields"`
	Example       map[string]any  `json:"example"`
}

// commonOptionalFields are the optional fields every stock card kind shares.
func commonOptionalFields() []cardFieldSpec {
	return []cardFieldSpec{
		{Key: "source_url", Control: controlText},
		{Key: "extra", Control: controlTextarea},
	}
}

// fieldsWithCommon returns "the kind's own fields + the common optional fields" in a fresh slice.
func fieldsWithCommon(own ...cardFieldSpec) []cardFieldSpec {
	out := make([]cardFieldSpec, 0, len(own)+2)
	out = append(out, own...)
	out = append(out, commonOptionalFields()...)
	return out
}

// cardKindCatalog returns the self-description of every engram stock card kind, ordered by kind.
//
// Graded says whether a machine answer is accepted: true for the kinds whose answer control grades, false for the
// self-assessed kinds (basic, cloze-free and short answer). The short answer kind carries a control but no machine
// grading, so the flag is stated here instead of being inferred from the control name at every call site.
func cardKindCatalog() []cardKindDescription {
	kinds := []cardKindDescription{
		{
			Kind: "basic", LabelKey: "cardtype.basic", AnswerControl: answerNone,
			FrontField: "front", BackField: "back",
			Fields: fieldsWithCommon(
				cardFieldSpec{Key: "front", Control: controlTextarea, Required: true},
				cardFieldSpec{Key: "back", Control: controlTextarea, Required: true},
			),
			Example: map[string]any{
				"front":      "What is the capital of France?",
				"back":       "Paris",
				"extra":      map[string]any{"note": "capital since the late 10th century"},
				"source_url": "https://example.com/geography/france",
			},
		},
		{
			Kind: "basic_both", LabelKey: "cardtype.basic_both", AnswerControl: answerNone,
			FrontField: "front", BackField: "back",
			Fields: fieldsWithCommon(
				cardFieldSpec{Key: "front", Control: controlTextarea, Required: true},
				cardFieldSpec{Key: "back", Control: controlTextarea, Required: true},
			),
			Example: map[string]any{
				"front": "der Hund",
				"back":  "the dog",
			},
		},
		{
			Kind: "cloze", LabelKey: "cardtype.cloze", Graded: true, AnswerControl: answerBlanks,
			FrontField: "text", BackField: "text",
			Fields: fieldsWithCommon(
				cardFieldSpec{Key: "text", Control: controlTextarea, Required: true},
			),
			Example: map[string]any{
				"text": "{{c1::Paris}} is the capital of {{c2::France}}.",
			},
		},
		{
			Kind: "typed", LabelKey: "cardtype.typed", Graded: true, AnswerControl: answerText,
			FrontField: "prompt", BackField: "answer", PromptField: "prompt",
			Fields: fieldsWithCommon(
				cardFieldSpec{Key: "prompt", Control: controlTextarea, Required: true},
				cardFieldSpec{Key: "answer", Control: controlText, Required: true},
				cardFieldSpec{Key: "accept", Control: controlLines},
				cardFieldSpec{Key: "ignore_case", Control: controlBool, Default: true},
				cardFieldSpec{Key: "ignore_whitespace", Control: controlBool, Default: true},
			),
			Example: map[string]any{
				"prompt":            "Which element has the chemical symbol Au?",
				"answer":            "gold",
				"accept":            []any{"aurum"},
				"ignore_case":       true,
				"ignore_whitespace": true,
			},
		},
		{
			Kind: "numeric", LabelKey: "cardtype.numeric", Graded: true, AnswerControl: answerNumber,
			FrontField: "prompt", BackField: "value", PromptField: "prompt",
			Fields: fieldsWithCommon(
				cardFieldSpec{Key: "prompt", Control: controlTextarea, Required: true},
				cardFieldSpec{Key: "value", Control: controlNumber, Required: true},
				cardFieldSpec{Key: "unit", Control: controlText},
				cardFieldSpec{Key: "tolerance_absolute", Control: controlNumber},
				cardFieldSpec{Key: "tolerance_relative", Control: controlNumber},
			),
			Example: map[string]any{
				"prompt":             "What is standard gravity \\(g_0\\)?",
				"value":              9.80665,
				"unit":               "m/s²",
				"tolerance_relative": 0.01,
			},
		},
		{
			Kind: "choice_single", LabelKey: "cardtype.choice_single", Graded: true, AnswerControl: answerSingle,
			FrontField: "question", BackField: "options", PromptField: "question", OptionsField: "options",
			Fields: fieldsWithCommon(
				cardFieldSpec{Key: "question", Control: controlTextarea, Required: true},
				cardFieldSpec{Key: "options", Control: controlLines, Required: true},
				cardFieldSpec{Key: "answer", Control: controlIndex, Required: true},
			),
			Example: map[string]any{
				"question": "Which planet is closest to the Sun?",
				"options":  []any{"Venus", "Mercury", "Mars"},
				"answer":   1,
			},
		},
		{
			Kind: "choice_multi", LabelKey: "cardtype.choice_multi", Graded: true, AnswerControl: answerMulti,
			FrontField: "question", BackField: "options", PromptField: "question", OptionsField: "options",
			Fields: fieldsWithCommon(
				cardFieldSpec{Key: "question", Control: controlTextarea, Required: true},
				cardFieldSpec{Key: "options", Control: controlLines, Required: true},
				cardFieldSpec{Key: "answers", Control: controlIndexes, Required: true},
			),
			Example: map[string]any{
				"question": "Which of these numbers are prime?",
				"options":  []any{"2", "4", "5", "9"},
				"answers":  []any{0, 2},
			},
		},
		{
			Kind: "true_false", LabelKey: "cardtype.true_false", Graded: true, AnswerControl: answerBool,
			FrontField: "statement", BackField: "answer", PromptField: "statement",
			Fields: fieldsWithCommon(
				cardFieldSpec{Key: "statement", Control: controlTextarea, Required: true},
				cardFieldSpec{Key: "answer", Control: controlBool, Required: true},
			),
			Example: map[string]any{
				"statement": "Water boils at 100 °C at sea level.",
				"answer":    true,
			},
		},
		{
			Kind: "list", LabelKey: "cardtype.list", Graded: true, AnswerControl: answerItems,
			FrontField: "prompt", BackField: "items", PromptField: "prompt",
			Fields: fieldsWithCommon(
				cardFieldSpec{Key: "prompt", Control: controlTextarea, Required: true},
				cardFieldSpec{Key: "items", Control: controlLines, Required: true},
				cardFieldSpec{Key: "ordered", Control: controlBool, Default: false},
			),
			Example: map[string]any{
				"prompt":  "Name the additive primary colours of light.",
				"items":   []any{"red", "green", "blue|blue-violet"},
				"ordered": false,
			},
		},
		{
			Kind: "short_answer", LabelKey: "cardtype.short_answer", AnswerControl: answerEssay,
			FrontField: "prompt", BackField: "reference", PromptField: "prompt",
			Fields: fieldsWithCommon(
				cardFieldSpec{Key: "prompt", Control: controlTextarea, Required: true},
				cardFieldSpec{Key: "reference", Control: controlTextarea},
			),
			Example: map[string]any{
				"prompt":    "Why does the Moon show phases?",
				"reference": "The Sun always lights half of the Moon; as the Moon orbits Earth we see a changing share of that lit half.",
			},
		},
	}
	return kinds
}

// cardKindByKind returns one entry of the catalog.
func cardKindByKind(kind string) (cardKindDescription, bool) {
	for _, entry := range cardKindCatalog() {
		if entry.Kind == kind {
			return entry, true
		}
	}
	return cardKindDescription{}, false
}

// cardFieldSpecs returns the field table of a catalog entry keyed by field name.
func cardFieldSpecs(entry cardKindDescription) map[string]cardFieldSpec {
	out := make(map[string]cardFieldSpec, len(entry.Fields))
	for _, spec := range entry.Fields {
		out[spec.Key] = spec
	}
	return out
}
