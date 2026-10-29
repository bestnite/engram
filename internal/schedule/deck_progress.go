package schedule

import (
	"context"
	"errors"
	"fmt"

	"git.nite07.com/nite/engram/internal/store"
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

// DeckProgress returns each deck's studied and remaining card counts for userID.
//
// Remaining counts store.UserNewCards, the queue's own definition of a new card, including buried ones. Studied counts the
// user's state rows that have left the new state on live cards, suspended or not. Another user's progress on a shared deck
// never enters either count, because both are keyed on userID. A deck with nothing studied and nothing remaining has no
// entry in the map; callers read it as zero.
func (b *QueueBuilder) DeckProgress(ctx context.Context, userID uint64, deckIDs []uint64) (map[uint64]DeckProgress, error) {
	if userID == 0 {
		return nil, errors.New("schedule: deck progress: user id is required")
	}
	ids := dedupeIDs(deckIDs)
	out := make(map[uint64]DeckProgress, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	db := b.db.WithContext(ctx)
	remaining, err := countByDeck(store.UserNewCards(db, userID, ids))
	if err != nil {
		return nil, fmt.Errorf("schedule: count remaining new cards: %w", err)
	}
	studied, err := countByDeck(db.Table("card_states AS cs").
		Joins("JOIN cards AS cards ON cards.id = cs.card_id AND cards.deleted_at IS NULL").
		Joins("JOIN notes AS notes ON notes.id = cards.note_id AND notes.deleted_at IS NULL").
		Where("cs.user_id = ? AND cs.state <> ? AND notes.deck_id IN ?", userID, store.CardStateNew, ids))
	if err != nil {
		return nil, fmt.Errorf("schedule: count studied cards: %w", err)
	}
	for _, id := range ids {
		if remaining[id] == 0 && studied[id] == 0 {
			continue
		}
		out[id] = DeckProgress{Studied: studied[id], Remaining: remaining[id]}
	}
	return out, nil
}
