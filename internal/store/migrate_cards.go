package store

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"git.nite07.com/nite/engram/internal/notetype"
	"gorm.io/gorm"
)

// removeUnresolvableCards is migration 0019: it soft-deletes every live card whose stored template
// its note's note type cannot resolve, so the card leaves the review queue.
//
// Such a card can never be rendered or rated, and no note write generates it again. The known source
// is the cloze parser used before note types: it accepted {{c0::...}} and stored "cloze:0" cards,
// while the note type rules follow Anki and ignore c0. Left in place, such a card enters the queue and
// its render fails on every attempt.
//
// The cards are soft-deleted, not removed: card_states and reviews follow the card id, and the review
// log is never discarded by a content repair. A card whose note type row cannot be loaded is left
// alone, because nothing can tell whether its template is valid. A second run finds no such card and
// writes nothing.
func removeUnresolvableCards(tx *gorm.DB) error {
	m := tx.Migrator()
	if !m.HasTable("cards") || !m.HasTable("notes") || !m.HasTable("note_types") {
		return nil
	}
	const batchSize = 500
	configs := make(map[uint64]*notetype.Config)
	now := time.Now().UTC()
	removed := 0
	var last uint64
	for {
		var batch []struct {
			ID         uint64
			Template   string
			NoteTypeID uint64 `gorm:"column:note_type_id"`
		}
		if err := tx.Table("cards").
			Select("cards.id, cards.template, notes.note_type_id").
			Joins("JOIN notes ON notes.id = cards.note_id").
			Where("cards.deleted_at IS NULL AND cards.id > ?", last).
			Order("cards.id ASC").Limit(batchSize).
			Scan(&batch).Error; err != nil {
			return fmt.Errorf("remove unresolvable cards: scan cards after id %d: %w", last, err)
		}
		if len(batch) == 0 {
			break
		}
		var stale []uint64
		for _, row := range batch {
			last = row.ID
			cfg, ok := configs[row.NoteTypeID]
			if !ok {
				var nt NoteType
				err := tx.Select("id", "config_json").First(&nt, row.NoteTypeID).Error
				switch {
				case errors.Is(err, gorm.ErrRecordNotFound):
					cfg = nil
				case err != nil:
					return fmt.Errorf("remove unresolvable cards: card %d: load note type %d: %w", row.ID, row.NoteTypeID, err)
				default:
					decoded, err := nt.Config()
					if err != nil {
						return fmt.Errorf("remove unresolvable cards: card %d: %w", row.ID, err)
					}
					cfg = &decoded
				}
				configs[row.NoteTypeID] = cfg
			}
			if cfg == nil {
				continue
			}
			if _, _, err := notetype.CardTemplate(*cfg, row.Template); err != nil {
				stale = append(stale, row.ID)
			}
		}
		if len(stale) > 0 {
			if err := tx.Table("cards").Where("id IN ?", stale).Update("deleted_at", now).Error; err != nil {
				return fmt.Errorf("remove unresolvable cards: soft-delete %d cards: %w", len(stale), err)
			}
			removed += len(stale)
		}
	}
	if removed > 0 {
		slog.Info("removed cards whose template the note type cannot resolve", "cards", removed)
	}
	return nil
}
