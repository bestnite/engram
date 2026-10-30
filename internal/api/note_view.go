package api

import (
	"context"
	"fmt"
	"strconv"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/answer"
	"git.nite07.com/nite/engram/internal/notetype"
	"git.nite07.com/nite/engram/internal/store"
)

// This file holds the transitional view of a note: the shape the SPA and the old REST contract still read while the
// note-type payload and the frame runtime replace them.
//
// A note stores only its note type id and HTML field values keyed by field id. The old contract speaks a card kind and
// a field map whose values follow the kind's editor controls (a string array for options, a boolean for true/false, a
// number for a tolerance). The helpers here derive that view from the note type config; nothing that decides behaviour
// reads the derived kind or the reshaped values.

// noteTypeView is one note's note type together with its decoded config, cached per request by noteTypeCache.
type noteTypeView struct {
	noteType *store.NoteType
	config   notetype.Config
	kind     string
}

// noteTypeCache loads note types by id within one call, so a page of notes costs one query rather than one per note.
type noteTypeCache struct {
	store *store.NoteTypeStore
	byID  map[uint64]noteTypeView
}

func newNoteTypeCache(db *gorm.DB) *noteTypeCache {
	return &noteTypeCache{store: store.NewNoteTypeStore(db), byID: map[uint64]noteTypeView{}}
}

// loadNoteType resolves the note type of a note (nil NoteTypeID or a missing row yields the zero view).
func (c *noteTypeCache) load(ctx context.Context, note *store.Note) noteTypeView {
	if note.NoteTypeID == nil {
		return noteTypeView{}
	}
	if view, ok := c.byID[*note.NoteTypeID]; ok {
		return view
	}
	nt, err := c.store.ByID(ctx, *note.NoteTypeID)
	if err != nil {
		return noteTypeView{}
	}
	cfg, err := nt.Config()
	if err != nil {
		return noteTypeView{}
	}
	view := noteTypeView{noteType: nt, config: cfg, kind: notetype.EngramStockKindFor(cfg)}
	c.byID[*note.NoteTypeID] = view
	return view
}

// transitionalFields presents stored field values in the typed shapes the legacy editor expects.
//
// The shape rule lives in the store, next to the format-1 package translation that needs the same mapping, so the two
// cannot drift.
func transitionalFields(kind string, values map[string]string) map[string]any {
	return store.LegacyNoteFields(kind, values)
}

// templateAnswer returns the answer spec of the template a card uses, or nil for a self-graded card.
func templateAnswer(cfg notetype.Config, cardTemplate string) (*notetype.AnswerSpec, int, error) {
	tmpl, clozeOrdinal, err := notetype.CardTemplate(cfg, cardTemplate)
	if err != nil {
		return nil, 0, err
	}
	return tmpl.Answer, clozeOrdinal, nil
}

// isMachineGraded reports whether an answer spec names a control that grades a machine answer. The short answer control
// carries a spec — it records the written answer — but grading stays self-assessed.
func isMachineGraded(spec *notetype.AnswerSpec) bool {
	if spec == nil {
		return false
	}
	if spec.Control == "short_answer" {
		return false
	}
	_, ok := answer.Lookup(spec.Control)
	return ok
}

// optionCount returns the number of options the answer spec's options role holds, and whether the control has one.
func optionCount(spec *notetype.AnswerSpec, values map[string]string) int {
	if spec == nil {
		return 0
	}
	control, ok := answer.Lookup(spec.Control)
	if !ok {
		return 0
	}
	roles, ok := control.(answer.Roles)
	if !ok {
		return 0
	}
	for _, role := range roles.Roles() {
		if role.Name != "options" {
			continue
		}
		return len(answer.ParseLines(values[spec.Bindings[role.Name]]))
	}
	return 0
}

// gradeMappingFor converts a preset's stored mapping into the grading package's type.
func gradeMappingFor(preset *store.Preset) *answer.GradeMapping {
	mapping, err := preset.GradeMapping()
	if err != nil {
		return nil
	}
	return &mapping
}

// noteTypeIDFor resolves the note type a write names with the transitional kind alias.
//
// The alias names a card kind; the note type is the deck's stock note type for that kind, created when the
// deck has none yet. An unknown kind is a request error naming the kind.
func (a *API) noteTypeIDFor(ctx context.Context, deckID uint64, kind string) (uint64, error) {
	nt, err := a.notes.EnsureStockNoteType(ctx, deckID, kind)
	if err != nil {
		return 0, err
	}
	return nt.ID, nil
}

// noteTypeIDForPublicID resolves a note type public id scoped to a deck.
//
// A public id that names no note type, or names one of another deck, yields the same error: a write
// cannot attach a note to a type from a different deck, and the response must not confirm that the id
// exists elsewhere.
func (a *API) noteTypeIDForPublicID(ctx context.Context, deckID uint64, publicID string) (uint64, error) {
	nt, err := a.noteTypes.ByPublicID(ctx, publicID)
	if err != nil || nt.DeckID != deckID {
		return 0, fmt.Errorf("unknown note type %q", publicID)
	}
	return nt.ID, nil
}

// noteKindOf returns the stock kind of a note's note type, or the empty string when it is not a stock type.
func (a *API) noteKindOf(ctx context.Context, note *store.Note) string {
	return a.notes.KindOf(ctx, note)
}

// unknownKindText is the stable English message for a kind the catalog does not know.
func unknownKindText(kind string) string {
	return "unknown note type kind " + strconv.Quote(kind)
}
