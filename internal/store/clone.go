package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/notetype"
)

// Clone copies all content of a deck (notes and cards) into targetUserID's account ('clone/fork').
//
// Key semantics (content and progress kept separate):
//   - only content columns are copied (note type / fields_json / tags_json / external_ref / source / reference_refs);
//   - each source note type is copied as a new row of the cloned deck, with the same config and styling;
//   - new notes regenerate cards from the card type, yielding a set of cards with **brand-new ids and brand-new rows**;
//   - no progress is ever copied: the new cards have no card_states rows, progress in the caller's account starts from zero;
//   - grants are per-instance state and are not copied with the content — the cloned deck has the caller as its only owner.
//
// The whole copy happens in one transaction: any failure rolls back and no half-made copy is left behind.
// The source deck is read-only and is not modified by this operation.
func (s *DeckStore) Clone(ctx context.Context, src *Deck, targetUserID uint64, newName string, presetID uint64) (*Deck, error) {
	if src == nil || src.ID == 0 {
		return nil, errors.New("clone deck: source deck is required")
	}
	if targetUserID == 0 {
		return nil, errors.New("clone deck: target user is required")
	}
	if newName == "" {
		return nil, ErrDeckNameRequired
	}
	if presetID == 0 {
		return nil, ErrDeckPresetRequired
	}
	// It uses the same name/description validation as deck creation and rename: cloning used to check only for non-emptiness and then tx.Create, so it could
	// store a deck over 200 characters long or one containing control characters, which every later rename/import entry point would reject.
	dst := Deck{
		OwnerUserID: targetUserID,
		Name:        newName,
		Description: src.Description,
		PresetID:    presetID,
		CreatedAt:   time.Now().UTC(),
	}
	if err := validateDeckForWrite(&dst, true); err != nil {
		return nil, err
	}
	// the cloner = the target user: the copy's note references must be readable to them (validated before writing).
	// When the cloner can see the source deck, the source note mapping already implies readability, so a legitimate clone is not blocked;
	// after cloning, the copy's mapping points at the new notes, so readers no longer depend on the source deck staying visible.
	ctx = WithActor(ctx, targetUserID)

	var out Deck
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&dst).Error; err != nil {
			return fmt.Errorf("create cloned deck: %w", err)
		}

		// only non-soft-deleted notes are taken (GORM default scope); deleted content must not be copied.
		var notes []Note
		if err := tx.Where("deck_id = ?", src.ID).Order("id ASC").Find(&notes).Error; err != nil {
			return fmt.Errorf("list source notes for clone: %w", err)
		}

		// it reuses NoteStore's card-type validation and card generation pipeline, so cloning and manual card creation share one path.
		notesStore := NewNoteStore(s.db)
		// A note type belongs to exactly one deck, so the clone cannot reuse the source row. The config is copied
		// verbatim into a new row instead: rebuilding a stock type from a recognised kind would lose the field and
		// template ids of an imported type and every custom template or style the source deck carried.
		clonedTypes := make(map[uint64]*NoteType)
		for i := range notes {
			original := notes[i]
			fields, err := ParseFields(original.FieldsJSON)
			if err != nil {
				return fmt.Errorf("clone note %d: decode fields: %w", original.ID, err)
			}
			srcType, srcConfig, err := noteTypeFor(ctx, tx, &original)
			if err != nil {
				return fmt.Errorf("clone note %d: %w", original.ID, err)
			}
			noteType, ok := clonedTypes[srcType.ID]
			if !ok {
				noteType, err = cloneNoteType(ctx, tx, dst.ID, srcType, srcConfig)
				if err != nil {
					return fmt.Errorf("clone note %d: %w", original.ID, err)
				}
				clonedTypes[srcType.ID] = noteType
			}
			note := &Note{
				DeckID:        dst.ID,
				NoteTypeID:    Ptr(noteType.ID),
				TagsJSON:      original.TagsJSON,
				ExternalRef:   original.ExternalRef,
				Source:        original.Source,
				ReferenceRefs: original.ReferenceRefs,
				CreatedBy:     Ptr(targetUserID),
			}
			if _, err := notesStore.SaveInTx(ctx, tx, note, fields); err != nil {
				return fmt.Errorf("clone note %d: %w", original.ID, err)
			}
		}
		out = dst
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// cloneNoteType writes a copy of a source deck's note type into the cloned deck.
//
// The name and config are validated like every other note type write, so a stored config the store would reject cannot
// enter the clone. The media reference set is written in the same transaction because the garbage collector resolves
// `_`-prefixed assets through it: a copied type without its references would let the clone's own assets be reclaimed.
func cloneNoteType(ctx context.Context, tx *gorm.DB, deckID uint64, src *NoteType, cfg notetype.Config) (*NoteType, error) {
	now := time.Now().UTC()
	nt := &NoteType{DeckID: deckID, CreatedAt: now, UpdatedAt: now}
	if err := nt.SetConfig(src.Name, cfg); err != nil {
		return nil, fmt.Errorf("copy note type %d: %w", src.ID, err)
	}
	if err := tx.WithContext(ctx).Create(nt).Error; err != nil {
		return nil, fmt.Errorf("copy note type %d: %w", src.ID, err)
	}
	if err := NewMediaNoteTypeStore(tx).Replace(ctx, nt.ID, noteTypeMediaNames(cfg)); err != nil {
		return nil, fmt.Errorf("copy media references of note type %d: %w", src.ID, err)
	}
	return nt, nil
}
