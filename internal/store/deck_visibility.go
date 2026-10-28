package store

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// visibleDeckIDsQuery returns the subquery of 'deck ids visible to a user', the single source of the visibility predicate.
//
// A deck has only two kinds of ownership: one you created (decks.owner_user_id) and one someone explicitly granted you (deck_grants).
// There is no third kind: no deck is 'visible to everyone by default'. Making someone else's deck show up automatically in my list, my review queue
// and my stats would mean accepting on my behalf something I never agreed to — which is exactly why 'public decks' were removed.
//
// The list page (ListVisible), the queue's whole-DB rule (VisibleIDs) and the stats page's aggregate queries (StatsStore)
// all use it as their scope condition: separate copies would inevitably drift. The queue's whole-DB rule previously had no deck filter at all, so it counted other users'
// private decks' new cards into the current user's queue (a privilege leak); the stats page's deck dimension and due forecast made the same mistake
// (starting from cards and left-joining card_states only on user_id, cards in someone else's deck have no state row, so they were all counted
// as 'due' and grouped by their deck_id, and someone else's private deck surfaced in the deck dimension).
func visibleDeckIDsQuery(db *gorm.DB, userID uint64) *gorm.DB {
	granted := db.Model(&DeckGrant{}).Select("deck_id").Where("user_id = ?", userID)
	return db.Model(&Deck{}).Select("id").
		Where("owner_user_id = ? OR id IN (?)", userID, granted)
}

func (s *DeckStore) visibleDecksQuery(ctx context.Context, userID uint64) *gorm.DB {
	return s.db.WithContext(ctx).Model(&Deck{}).Where("id IN (?)", visibleDeckIDsQuery(s.db, userID))
}

// ListVisible returns the decks a user 'should see in the list' (list semantics), newest first.
func (s *DeckStore) ListVisible(ctx context.Context, userID uint64) ([]Deck, error) {
	var decks []Deck
	if err := s.visibleDecksQuery(ctx, userID).
		Order("created_at DESC, id DESC").Find(&decks).Error; err != nil {
		return nil, fmt.Errorf("list visible decks: %w", err)
	}
	return decks, nil
}

// VisibleIDs returns the id set of decks visible to the user, sharing the same predicate as ListVisible (see visibleDecksQuery).
//
// The queue's 'whole DB' rule (DeckID=0 and DeckIDs empty) resolves its scope with it: any deck visible on the list page may enter the queue,
// and not a single deck of someone else's can (unless the user was explicitly granted it). An empty set means the user has no visible decks, so the queue is empty (not an error).
func (s *DeckStore) VisibleIDs(ctx context.Context, userID uint64) ([]uint64, error) {
	var ids []uint64
	if err := s.visibleDecksQuery(ctx, userID).Order("id ASC").Pluck("id", &ids).Error; err != nil {
		return nil, fmt.Errorf("list visible deck ids: %w", err)
	}
	return ids, nil
}
