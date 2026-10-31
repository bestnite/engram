package api

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/answer"
	"git.nite07.com/nite/engram/internal/notetype"
	"git.nite07.com/nite/engram/internal/store"
)

// This file holds the view of a note used by the REST and MCP payloads.
//
// A note stores only its note type id and HTML field values keyed by field id. The helpers here read the note type
// config; the field values themselves are returned exactly as stored, because a field value is HTML and any reshaping
// would lose the formatting an editor writes back.

// noteTypeView is one note's note type together with its decoded config, cached per request by noteTypeCache.
type noteTypeView struct {
	noteType *store.NoteType
	config   notetype.Config
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
	view := noteTypeView{noteType: nt, config: cfg}
	c.byID[*note.NoteTypeID] = view
	return view
}

// fieldOrder returns the note type's field ids in config order; the field value map keeps no order of its own.
func fieldOrder(cfg notetype.Config) []string {
	if len(cfg.Fields) == 0 {
		return nil
	}
	out := make([]string, 0, len(cfg.Fields))
	for _, field := range cfg.Fields {
		out = append(out, field.ID)
	}
	return out
}

// templateAnswer returns the answer spec of the template a card uses, or nil for a self-graded card.
func templateAnswer(cfg notetype.Config, cardTemplate string) (*notetype.AnswerSpec, int, error) {
	tmpl, clozeOrdinal, err := notetype.CardTemplate(cfg, cardTemplate)
	if err != nil {
		return nil, 0, err
	}
	return tmpl.Answer, clozeOrdinal, nil
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
