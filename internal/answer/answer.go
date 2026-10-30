// Package answer is the answer-control registry. A control is the unit that
// replaces one of engram's graded card kinds: it renders the input a card face
// shows at its type placeholder, renders the result breakdown on the back, and
// grades the answer the learner submits.
//
// # Registration
//
// Every control lives in its own file and registers itself in init. The
// registry therefore needs no change when a control is added; adding the file
// is the whole change. A duplicate, empty or nil registration is an error, so
// two files cannot silently claim the same name.
//
// # Roles
//
// An answer spec binds role names to field ids. A control declares the roles it
// reads, whether each is required, and how the field value is parsed, so the
// same declaration drives validation, binding resolution and grading. A
// template with an answer spec must also carry exactly one type placeholder on
// its front; notetype.Config.Validate enforces that structural rule, while
// ValidateSpec here enforces that the control exists and every required role is
// bound. The two checks are split because notetype cannot import this package
// without an import cycle, so the note-type write path runs both.
//
// # Field values
//
// Field values are HTML. A role declares one of four parses: text (PlainText,
// trimmed), lines (PlainText split at line breaks, one entry per line, empty
// lines dropped), bool (the lowercased text is "true" or "false") and number
// (a decimal). ResolveBindings performs the parse once per role, so a control
// never re-parses a field.
//
// # Rendering contract
//
// RenderFront returns the input markup that replaces the placeholder;
// RenderBack returns the breakdown markup for a graded answer. The caller
// substitutes the returned markup for RenderContext.Marker in the rendered
// face. Inputs carry a stable id from RenderContext.ElementID, the value the
// frame reads when it posts {value}, and a data-control attribute naming the
// control. Breakdown markup carries data-correct attributes instead of
// localised wording, so the view layer can style it and the catalogs keep
// owning visible text.
package answer

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"git.nite07.com/nite/engram/internal/notetype"
)

// Control is the interface an answer control implements. RenderFront and
// RenderBack return markup to insert at the card's type placeholder; Grade
// decodes the answer JSON in GradeContext and returns the score, the mapped
// rating and the detail written to reviews.grade_detail_json.
type Control interface {
	// Name returns the control's registry name.
	Name() string
	// RenderFront returns the input markup the front face shows.
	RenderFront(ctx RenderContext) (string, error)
	// RenderBack returns the result breakdown the back face shows.
	RenderBack(ctx RenderContext) (string, error)
	// Grade parses ctx.Answer and grades it against ctx.Bindings.
	Grade(ctx GradeContext) (GradeResult, error)
}

// Factory builds a control. Controls are stateless, so the registry may build a
// fresh value per lookup and concurrent callers never share one.
type Factory func() Control

// Roles is implemented by every control to declare the roles its answer spec
// binds. ValidateSpec and ResolveBindings read this declaration instead of
// knowing individual controls.
type Roles interface {
	Roles() []Role
}

// Registry maps a control name to its factory.
type Registry struct {
	controls map[string]Factory
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{controls: make(map[string]Factory)}
}

// Register adds a control. An empty name, a nil factory and a duplicate name
// are rejected.
func (r *Registry) Register(name string, factory Factory) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("answer: control name must not be empty")
	}
	if factory == nil {
		return fmt.Errorf("answer: control %q must have a factory", name)
	}
	if _, exists := r.controls[name]; exists {
		return fmt.Errorf("answer: control %q is already registered", name)
	}
	r.controls[name] = factory
	return nil
}

// Lookup builds the named control. The second result is false for an unknown
// name.
func (r *Registry) Lookup(name string) (Control, bool) {
	factory, ok := r.controls[name]
	if !ok {
		return nil, false
	}
	return factory(), true
}

// Names returns the registered names in lexicographic order.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.controls))
	for name := range r.controls {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Default is the process registry; the built-in controls register themselves
// from their own files.
var Default = NewRegistry()

// Register adds a control to Default.
func Register(name string, factory Factory) error { return Default.Register(name, factory) }

// Lookup builds a control from Default.
func Lookup(name string) (Control, bool) { return Default.Lookup(name) }

// Names returns the control names registered in Default.
func Names() []string { return Default.Names() }

// mustRegister registers a built-in control, panicking on a programming error
// such as a duplicate name or a missing factory.
func mustRegister(name string, factory Factory) {
	if err := Register(name, factory); err != nil {
		panic(err)
	}
}

// ErrPlaceholderMissing means the face to render does not contain the type
// placeholder the control was asked to fill.
var ErrPlaceholderMissing = errors.New("answer: type placeholder is not present in the rendered face")

// RenderSide renders one side of a card face: it calls RenderFront or
// RenderBack according to ctx.Side and substitutes the returned markup for
// ctx.Marker in face. A marker that does not occur is a caller error, because
// the note-type validation already guarantees exactly one placeholder.
func RenderSide(control Control, ctx RenderContext, face string) (string, error) {
	var markup string
	var err error
	if ctx.Side == notetype.SideBack {
		markup, err = control.RenderBack(ctx)
	} else {
		markup, err = control.RenderFront(ctx)
	}
	if err != nil {
		return "", err
	}
	if ctx.Marker == "" || !strings.Contains(face, ctx.Marker) {
		return "", ErrPlaceholderMissing
	}
	return strings.Replace(face, ctx.Marker, markup, 1), nil
}

// ValidateSpec checks that the spec names a registered control and binds every
// required role. It does not inspect the bound fields, because it has no note
// to read them from.
func ValidateSpec(spec notetype.AnswerSpec) error {
	if strings.TrimSpace(spec.Control) == "" {
		return errors.New("answer: answer spec has no control")
	}
	control, ok := Lookup(spec.Control)
	if !ok {
		return fmt.Errorf("answer: unknown answer control %q", spec.Control)
	}
	declared, ok := control.(Roles)
	if !ok {
		return fmt.Errorf("answer: control %q does not declare its roles", spec.Control)
	}
	for _, role := range declared.Roles() {
		if !role.Required {
			continue
		}
		if strings.TrimSpace(spec.Bindings[role.Name]) == "" {
			return fmt.Errorf("answer: control %q is missing required role %q", spec.Control, role.Name)
		}
	}
	return nil
}

// ResolveBindings looks up the field id each role names, parses the field value
// with the role's parse, and returns the result. A required role must be bound
// to a field that exists; an optional role that is not bound is skipped.
func ResolveBindings(spec notetype.AnswerSpec, fields map[string]string) (Bindings, error) {
	control, ok := Lookup(spec.Control)
	if !ok {
		return nil, fmt.Errorf("answer: unknown answer control %q", spec.Control)
	}
	declared, ok := control.(Roles)
	if !ok {
		return nil, fmt.Errorf("answer: control %q does not declare its roles", spec.Control)
	}
	bindings := make(Bindings, len(declared.Roles()))
	for _, role := range declared.Roles() {
		fieldID := strings.TrimSpace(spec.Bindings[role.Name])
		if fieldID == "" {
			if role.Required {
				return nil, fmt.Errorf("answer: control %q is missing required role %q", spec.Control, role.Name)
			}
			continue
		}
		html, exists := fields[fieldID]
		if !exists {
			if role.Required {
				return nil, fmt.Errorf("answer: field %q bound to role %q is not present", fieldID, role.Name)
			}
			continue
		}
		value, err := parseValue(role.Kind, html)
		if err != nil {
			return nil, fmt.Errorf("answer: role %q: %w", role.Name, err)
		}
		bindings[role.Name] = Binding{HTML: html, Value: value}
	}
	return bindings, nil
}
