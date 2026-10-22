// Package cardtype is the card type registry and core pipeline.
//
// Design goal: adding a card type = adding an implementation file + registering it, with the core code unchanged
// (AGENTS.md §2.3 item 7). To that end the core depends on just one narrow interface, CardType; capabilities such as
// answer-based grading and future LLM scoring are asserted through optional narrow interfaces (Grader / PromptContexter / ReferenceRefer),
// instead of being stuffed into a big interface up front.
//
// Field names are frozen and match schema/note-import.schema.json verbatim (basic/basic_both: front/back;
// cloze: text; list: prompt/items[]/ordered; common optional extra/source_url).
package cardtype

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// Note is the content view the card type sees: Kind decides the structure of Fields.
// It is kept separate from store.Note because the registry only cares about content, not persistence
// (content separated from progress).
type Note struct {
	Kind   string
	Fields map[string]any
}

// Card carries both the presentation identity and the fields needed for rendering.
//
// Template must fall within the cards.template convention:
// forward / reverse / cloze:<index>. Ordinal increases monotonically from 0 within the same note.
// Fields are the fields of the owning note, filled in by the caller before rendering (it may be empty when generating card identities, and is not persisted).
type Card struct {
	Template string
	Ordinal  int
	Fields   map[string]any
}

// Side states whether the render request is for the front or the back.
type Side int

const (
	// SideFront is the question side.
	SideFront Side = iota
	// SideBack is the answer side.
	SideBack
)

// String returns the English identifier of the side name, for logs and error messages.
func (s Side) String() string {
	if s == SideBack {
		return "back"
	}
	return "front"
}

// RenderResult is the render input for one side: Markdown + TeX source,
// with HTML conversion and sanitization left to the rendering pipeline.
type RenderResult struct {
	// Body is the main content of this side.
	Body string
	// Extra is the additional fragments shown in order on this side (the items a list reveals one by one);
	// empty when there are no extra fragments.
	Extra []string
}

// CardType is the interface a card type must implement; the method set is deliberately kept minimal.
type CardType interface {
	// Validate checks fields. Errors must be readable English and name the field.
	Validate(fields map[string]any) error
	// Cards derives card identities from the note; Template must be unique within a note.
	Cards(note Note) []Card
	// Render returns the content to show for card on the given side.
	Render(card Card, side Side) (RenderResult, error)
	// Label returns the i18n language pack key, not user-visible copy.
	Label() string
	// Describe returns the card type's self-description (field table, front/back fields, answer controls), for the editor and review pages to render.
	// Graded is not declared here; the registry derives it by doing an interface assertion on Grader while enumerating.
	Describe() Description
}

// Grader is an optional capability: answer-based card types implement machine grading.
// Card types that do not implement it are not asserted on and the core pipeline does not depend on it; those that do are graded server-side and cannot be self-graded by the client.
//
// The three methods cover all card-type-specific knowledge about grading, so the transport and service layers need no branching by card type:
// how to decode an answer from JSON (ParseAnswer), how to grade it (Grade), and how to turn the answer back into readable text afterwards (GivenText).
type Grader interface {
	// ParseAnswer decodes the JSON answer submitted by the client into this card type's Grade input. An empty raw or JSON null
	// means "no answer": card types that can grade an empty answer as wrong return the corresponding input, otherwise they return an error.
	ParseAnswer(gc GradeContext, raw json.RawMessage) (any, error)
	// Grade grades the user input. rating is 1–4 (Again/Hard/Good/Easy),
	// detail is persisted to reviews.grade_detail_json, and ok=false means the input cannot be graded.
	Grade(input any) (rating int, detail map[string]any, ok bool)
	// GivenText turns the grading detail back into display answer text (for example, an option index into the option text);
	// true/false cards return "true"/"false", and localization is the front end's job.
	GivenText(fields map[string]any, detail map[string]any) string
}

// PromptContext is the question context returned by PromptContexter, for use by future LLM scoring.
type PromptContext struct {
	Question  string
	Reference string
}

// PromptContexter is an optional capability: it supplies the question and reference answer for LLM scoring.
type PromptContexter interface {
	PromptContext(note Note) PromptContext
}

// ReferenceRefer is an optional capability: it returns the knowledge base fragment references this note depends on.
type ReferenceRefer interface {
	ReferenceRefs(note Note) []string
}

// Registry is the kind → card type implementation registry. The zero value is not usable; construct one with NewRegistry.
type Registry struct {
	types map[string]CardType
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{types: make(map[string]CardType)}
}

// Register registers a card type keyed by kind; an empty kind, a nil implementation, or a duplicate registration all return an error.
func (r *Registry) Register(kind string, t CardType) error {
	if kind == "" {
		return errors.New("cardtype: kind must not be empty")
	}
	if t == nil {
		return fmt.Errorf("cardtype: type %q must not be nil", kind)
	}
	if _, ok := r.types[kind]; ok {
		return fmt.Errorf("cardtype: type %q is already registered", kind)
	}
	r.types[kind] = t
	return nil
}

// Lookup finds a card type by kind.
func (r *Registry) Lookup(kind string) (CardType, bool) {
	t, ok := r.types[kind]
	return t, ok
}

// Kinds returns the registered kinds in lexicographic order.
func (r *Registry) Kinds() []string {
	out := make([]string, 0, len(r.types))
	for k := range r.types {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Validate validates the fields for kind; an unknown kind returns a readable error.
func (r *Registry) Validate(kind string, fields map[string]any) error {
	t, ok := r.Lookup(kind)
	if !ok {
		return UnknownKindError(kind)
	}
	return t.Validate(fields)
}

// Cards expands the note into cards using the registry; an unknown kind returns a readable error.
func (r *Registry) Cards(note Note) ([]Card, error) {
	t, ok := r.Lookup(note.Kind)
	if !ok {
		return nil, UnknownKindError(note.Kind)
	}
	return t.Cards(note), nil
}

// UnknownKindError builds the uniform unknown-card-type error; callers need not care about the exact wording.
func UnknownKindError(kind string) error {
	return fmt.Errorf("unknown card type %q", kind)
}

// Default is the card type registry; built-in card types are registered in builtin.go's init, and the package-level functions all act on it.
var Default = NewRegistry()

// Validate validates the fields for kind in the Default registry.
func Validate(kind string, fields map[string]any) error { return Default.Validate(kind, fields) }

// Cards expands the note into cards using the Default registry.
func Cards(note Note) ([]Card, error) { return Default.Cards(note) }

// Lookup finds a card type by kind in the Default registry.
func Lookup(kind string) (CardType, bool) { return Default.Lookup(kind) }

// Kinds returns the kinds registered in the Default registry.
func Kinds() []string { return Default.Kinds() }

// errUnknownTemplate builds the error for a template that does not belong to the card type.
func errUnknownTemplate(template string) error {
	return fmt.Errorf("unknown card template %q", template)
}
