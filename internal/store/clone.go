package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// Clone copies all content of a deck (notes and cards) into targetUserID's account ('clone/fork').
//
// Key semantics (content and progress kept separate):
//   - only content columns are copied (kind / fields_json / tags_json / external_ref / source / reference_refs);
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
		for i := range notes {
			original := notes[i]
			fields, err := ParseFields(original.FieldsJSON)
			if err != nil {
				return fmt.Errorf("clone note %d: decode fields: %w", original.ID, err)
			}
			// The clone lives in another deck, so it needs that deck's note type for the same stock kind; the source
			// note type is not reused, because a note type belongs to exactly one deck.
			kind := kindOfNote(ctx, tx, &original)
			if kind == "" {
				return fmt.Errorf("clone note %d: its note type is not an engram stock type", original.ID)
			}
			noteType, err := ensureStockNoteType(ctx, tx, dst.ID, kind)
			if err != nil {
				return fmt.Errorf("clone note %d: %w", original.ID, err)
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
