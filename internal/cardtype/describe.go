package cardtype

// This file defines the card types' self-description contract: a card type declares all the metadata the editor and review pages need
// (field table, front/back fields, answer controls, prompt/option fields), so the front end no longer has to hard-code a card type table of its own
// (AGENTS.md §2.3 item 7: adding a card type = add a file and register it, core code unchanged).
//
// The field names and values in the contract are frozen: the front end of the next lane implements them verbatim, so renaming or changing a value
// is a breaking change. Graded is not declared here: it is a runtime fact, derived by the registry doing an interface assertion on Grader while
// enumerating, and a card type self-reporting it would drift from the implementation.

// Field control values: they correspond one-to-one with the controls of the front-end edit form.
const (
	ControlText     = "text"     // single-line text
	ControlTextarea = "textarea" // multi-line text (Markdown / LaTeX)
	ControlLines    = "lines"    // multi-line text, one array element per line
	ControlNumber   = "number"   // number
	ControlBool     = "bool"     // boolean
	ControlIndex    = "index"    // single select: takes an options index
	ControlIndexes  = "indexes"  // multi select: takes a set of options indices
)

// Answer control values: they decide whether the review page gives input controls or the four-level self-rating.
const (
	AnswerNone   = "none"   // self-graded card type, no answer control
	AnswerText   = "text"   // text answer
	AnswerNumber = "number" // numeric answer
	AnswerSingle = "single" // single select
	AnswerMulti  = "multi"  // multi select
	AnswerBool   = "bool"   // true/false card
	AnswerBlanks = "blanks" // per-blank input (cloze card): one text box per target cloze
	AnswerItems  = "items"  // per-item input (list card): one text box per item
	AnswerEssay  = "essay"  // free-form writing followed by self-rating (short answer): the written answer is saved with the self-rating and is not graded
)

// FieldSpec describes the shape of one of a card type's fields in the edit form; the value set of Control corresponds one-to-one with the front-end controls.
type FieldSpec struct {
	Key      string `json:"key"`
	Control  string `json:"control"`
	Required bool   `json:"required"`
	// Default is the server-side default value for boolean fields that have a database default (for example true); omitted when there is none.
	Default any `json:"default,omitempty"`
}

// Description is one card type's self-description: the editor and review pages render from it and need no branching by card type.
type Description struct {
	Kind     string `json:"kind"`
	LabelKey string `json:"label_key"`
	// Graded says whether the card type implements Grader; the registry derives it by interface assertion while enumerating, and a card type must not declare it itself.
	Graded        bool        `json:"graded"`
	AnswerControl string      `json:"answer_control"`
	FrontField    string      `json:"front_field"`
	BackField     string      `json:"back_field"`
	PromptField   string      `json:"prompt_field"`
	OptionsField  string      `json:"options_field"`
	Fields        []FieldSpec `json:"fields"`
	// Example is a set of note field values that passes this card type's Validate. The field table only gives field names and controls,
	// which cannot express the conventions: cloze syntax, option indices starting at 0, formulas written with \( \) and \[ \] rather than $.
	// Examples turn those rules into data, so an agent writing them gets it right, and there is no need for a separate translatable paragraph per rule.
	// Whether the examples really pass validation is asserted per card type by describe_test, so they cannot drift from the implementation.
	Example map[string]any `json:"example"`
}

// commonOptionalFields are the optional fields shared by every card type, consistent with cardtype.validateCommonOptional.
var commonOptionalFields = []FieldSpec{
	{Key: "source_url", Control: ControlText},
	{Key: "extra", Control: ControlTextarea},
}

// fieldsWithCommon returns a new slice of "the card type's own fields + the common optional fields".
// A fresh slice is built each time, so a caller mutating one card type's field table cannot affect another.
func fieldsWithCommon(own ...FieldSpec) []FieldSpec {
	out := make([]FieldSpec, 0, len(own)+len(commonOptionalFields))
	out = append(out, own...)
	out = append(out, commonOptionalFields...)
	return out
}

// Descriptions returns the self-descriptions of every card type in the Default registry, ordered by kind.
func Descriptions() []Description { return Default.Descriptions() }

// Descriptions returns the self-descriptions of every card type in the registry, ordered by kind.
//
// Kind follows the registration key (not the Kind a card type reports itself), and Graded is derived from the Grader interface assertion:
// both are runtime facts, and deferring to the registry keeps the result stable and free of drift from the implementation.
func (r *Registry) Descriptions() []Description {
	kinds := r.Kinds()
	out := make([]Description, 0, len(kinds))
	for _, kind := range kinds {
		t := r.types[kind]
		d := t.Describe()
		d.Kind = kind
		_, graded := t.(Grader)
		d.Graded = graded
		out = append(out, d)
	}
	return out
}
