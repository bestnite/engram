package schedule

import (
	"context"
	"errors"
	"fmt"
)

// DeckProgress is one user's overall progress through a deck, independent of daily allowances.
type DeckProgress struct {
	// Studied is the number of the deck's live cards that this user has moved out of the new state.
	// A suspended card still counts here: suspending it does not undo what the user has already studied.
	Studied int
	// Remaining is the number of the deck's live cards that this user has not studied yet.
	// A suspended new card is excluded because the queue never offers it, so it is not work left to do.
	// A buried new card is included: burying only postpones it to a later review day.
	Remaining int
}

// DeckProgress returns each deck's studied and remaining card counts for userID in one grouped query.
//
// A card counts as new when the user has no state row for it or the row's state is new, the same rule the
// queue's new-card fetch (newCardsFrom) applies. Another user's progress on a shared deck never enters the counts,
// because the state join is keyed on userID. A deck without any live card has no entry in the map; callers read it as zero.
func (b *QueueBuilder) DeckProgress(ctx context.Context, userID uint64, deckIDs []uint64) (map[uint64]DeckProgress, error) {
	if userID == 0 {
		return nil, errors.New("schedule: deck progress: user id is required")
	}
	ids := dedupeIDs(deckIDs)
	out := make(map[uint64]DeckProgress, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	newState := StateNew.String()
	var rows []struct {
		DeckID    uint64 `gorm:"column:deck_id"`
		Studied   int    `gorm:"column:studied"`
		Remaining int    `gorm:"column:remaining"`
	}
	err := b.db.WithContext(ctx).Table("cards AS cards").
		Joins("JOIN notes AS notes ON notes.id = cards.note_id AND notes.deleted_at IS NULL").
		Joins("LEFT JOIN card_states AS cs ON cs.card_id = cards.id AND cs.user_id = ?", userID).
		Where("cards.deleted_at IS NULL AND notes.deck_id IN ?", ids).
		Select(`notes.deck_id AS deck_id,
			SUM(CASE WHEN cs.card_id IS NOT NULL AND cs.state <> ? THEN 1 ELSE 0 END) AS studied,
			SUM(CASE WHEN (cs.card_id IS NULL OR cs.state = ?) AND cs.suspended_at IS NULL THEN 1 ELSE 0 END) AS remaining`,
			newState, newState).
		Group("notes.deck_id").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("schedule: count deck progress: %w", err)
	}
	for _, r := range rows {
		out[r.DeckID] = DeckProgress{Studied: r.Studied, Remaining: r.Remaining}
	}
	return out, nil
}
