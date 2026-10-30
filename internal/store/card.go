package store

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/notetype"
)

// CardStore wraps GORM access to the cards table.
//
// Visibility rule: when a note is soft-deleted, its cards become invisible as well — that filter is implemented in the query
// layer as a join with notes, not by rewriting cards rows at delete time. That way progress data (attached to the card) is never lost because content was deleted.
type CardStore struct {
	db *gorm.DB
}

// NewCardStore constructs the card store.
func NewCardStore(db *gorm.DB) *CardStore { return &CardStore{db: db} }

// visibleCards is the single query starting point for \"card visible\": GORM adds cards.deleted_at IS NULL automatically,
// and the join condition further requires the owning note not to be soft-deleted.
func (s *CardStore) visibleCards(ctx context.Context) *gorm.DB {
	return s.db.WithContext(ctx).Model(&Card{}).
		Joins("JOIN notes ON notes.id = cards.note_id AND notes.deleted_at IS NULL")
}

// ByID fetches one visible card; returns gorm.ErrRecordNotFound when the owning note is soft-deleted.
func (s *CardStore) ByID(ctx context.Context, id uint64) (*Card, error) {
	var c Card
	if err := s.visibleCards(ctx).Select("cards.*").Where("cards.id = ?", id).First(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

// ByNote lists a note's visible cards, ordered by ordinal, then id ascending; returns an empty slice when the note is soft-deleted.
// The returned card templates and ordinals line up with notetype's card generation.
func (s *CardStore) ByNote(ctx context.Context, noteID uint64) ([]Card, error) {
	var cards []Card
	if err := s.visibleCards(ctx).Select("cards.*").
		Where("cards.note_id = ?", noteID).
		Order("cards.ordinal ASC, cards.id ASC").Find(&cards).Error; err != nil {
		return nil, fmt.Errorf("list cards by note: %w", err)
	}
	return cards, nil
}

// syncCards aligns a note's card set with the wanted set produced by notetype.GenerateCards.
//
// Invariants (AGENTS.md §2.3 item 1, content and progress kept separate):
//   - an existing card and its id are always kept: progress hangs off the card, deleting the row loses progress with it;
//   - an existing template reuses the original row (a soft-deleted one is restored here, ordinal aligned), the id stays unchanged;
//   - only newly appearing templates are inserted;
//   - a template that exists but is no longer generated is **soft-deleted**: after removing {{c2::}} the cloze:2 card can no longer
//     enter the review queue (it renders no blank at all). Soft delete writes only cards.deleted_at; card_states and reviews
//     stay as they are, and when the template reappears the reuse branch above restores the same row, bringing the progress back.
//     No row is ever hard-deleted here, and the explicit empty-card cleanup is a separate user action.
//
// The stored template value is the card's identity inside a note, not the ordinal: a normal note type's ordinals shift when a
// template in the middle is removed, while cards.template keeps naming the same card. The (note_id, template) unique index is
// the database backstop for that rule.
//
// If a duplicate template shows up within one sync, that is an error.
func syncCards(tx *gorm.DB, noteID uint64, wanted []notetype.GeneratedCard, now time.Time) ([]Card, error) {
	// soft-deleted rows must be included: the unique index does not exclude them, so only reusing by template avoids hitting the unique constraint.
	var existing []Card
	if err := tx.Unscoped().Where("note_id = ?", noteID).Find(&existing).Error; err != nil {
		return nil, fmt.Errorf("load cards for note %d: %w", noteID, err)
	}
	byTemplate := make(map[string]*Card, len(existing))
	for i := range existing {
		byTemplate[existing[i].Template] = &existing[i]
	}

	out := make([]Card, 0, len(wanted))
	seen := make(map[string]bool, len(wanted))
	for _, want := range wanted {
		if want.Template == "" {
			return nil, fmt.Errorf("note %d produced a card with an empty template", noteID)
		}
		if seen[want.Template] {
			return nil, fmt.Errorf("note %d produced duplicate card template %q", noteID, want.Template)
		}
		seen[want.Template] = true

		if row, ok := byTemplate[want.Template]; ok {
			// reuse the original row: write to the DB only when a soft-deleted row must be restored or the ordinal aligned, otherwise write nothing and keep the id.
			if row.DeletedAt.Valid || row.Ordinal != want.Ordinal {
				updates := map[string]any{"ordinal": want.Ordinal, "deleted_at": nil}
				if err := tx.Unscoped().Model(&Card{}).Where("id = ?", row.ID).Updates(updates).Error; err != nil {
					return nil, fmt.Errorf("update card %d: %w", row.ID, err)
				}
				row.Ordinal = want.Ordinal
				row.DeletedAt = gorm.DeletedAt{}
			}
			out = append(out, *row)
			continue
		}

		card := Card{NoteID: noteID, Template: want.Template, Ordinal: want.Ordinal, CreatedAt: now}
		if err := tx.Create(&card).Error; err != nil {
			return nil, fmt.Errorf("create card %q for note %d: %w", want.Template, noteID, err)
		}
		out = append(out, card)
	}

	// wrap-up: soft-delete cards that are still alive but no longer in wanted. Written per primary key, over the same set loaded above.
	var stale []uint64
	for i := range existing {
		if !existing[i].DeletedAt.Valid && !seen[existing[i].Template] {
			stale = append(stale, existing[i].ID)
		}
	}
	if len(stale) > 0 {
		if err := tx.Model(&Card{}).Where("id IN ?", stale).Update("deleted_at", now).Error; err != nil {
			return nil, fmt.Errorf("soft-delete stale cards of note %d: %w", noteID, err)
		}
	}
	return out, nil
}

// SuspendedNoteIDs returns the notes in noteIDs for which userID has suspended at least one undeleted card (suspension applies to the caller only).
func SuspendedNoteIDs(ctx context.Context, db *gorm.DB, userID uint64, noteIDs []uint64) (map[uint64]bool, error) {
	out := map[uint64]bool{}
	if len(noteIDs) == 0 {
		return out, nil
	}
	var ids []uint64
	if err := db.WithContext(ctx).Table("card_states AS cs").
		Joins("JOIN cards AS c ON c.id = cs.card_id AND c.deleted_at IS NULL").
		Where("cs.user_id = ? AND cs.suspended_at IS NOT NULL AND c.note_id IN ?", userID, noteIDs).
		Distinct("c.note_id").Pluck("c.note_id", &ids).Error; err != nil {
		return nil, fmt.Errorf("store: list suspended notes: %w", err)
	}
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}
