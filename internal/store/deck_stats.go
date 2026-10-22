package store

import (
	"context"
	"fmt"
)

// DeckSummary is one row of the deck list page: the deck itself plus its card count.
// The card count comes back in one grouped aggregate at the storage layer, avoiding one query per deck (N+1).
//
// 'how many cards can I review today' is not computed here: it must first pass the queue's daily quota (schedule.DeckCounts),
// obtained by the web layer through the same card-fetching path — a second raw backlog count at the storage layer would only drift from the queue's rules, since what is needed
// is the two numbers 'due count / new count per deck', not the raw backlog.
type DeckSummary struct {
	Deck Deck
	// CardCount is the number of undeleted cards produced by the deck's undeleted notes.
	CardCount int64
}

// countCardsByDeck counts the live cards of each deck.
// It writes explicit conditions with Table/Joins instead of relying on the model's automatic soft delete, keeping the conditions in one place for review.
func (s *DeckStore) countCardsByDeck(ctx context.Context, deckIDs []uint64) (map[uint64]int64, error) {
	type row struct {
		DeckID uint64
		N      int64
	}
	var rows []row
	if err := s.db.WithContext(ctx).
		Table("cards AS c").
		Select("n.deck_id AS deck_id, COUNT(c.id) AS n").
		Joins("JOIN notes AS n ON n.id = c.note_id AND n.deleted_at IS NULL").
		Where("c.deleted_at IS NULL AND n.deck_id IN ?", deckIDs).
		Group("n.deck_id").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("count cards by deck: %w", err)
	}
	out := make(map[uint64]int64, len(rows))
	for _, r := range rows {
		out[r.DeckID] = r.N
	}
	return out, nil
}
