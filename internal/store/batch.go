package store

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// batchLookupSize is the per-batch id cap for batched IN queries.
//
// A conservative 200: SQLite's bind-parameter cap is set at compile time by SQLITE_MAX_VARIABLE_NUMBER (historically 999,
// 32766 in newer versions), and the card_states batch query filters by (user_id, card_id IN (...)), so each id costs one
// parameter plus one for user_id. At 200 the parameter count per statement stays far below any default cap, with no reliance on a particular driver version.
const batchLookupSize = 200

// chunkIDs first removes duplicate ids, then cuts the set into slices of at most batchLookupSize, for batched IN queries.
//
// Dedup must happen before chunking: duplicate ids both waste parameter budget and make 'ids per batch' disagree with the number of rows actually fetched.
// First-appearance order is kept (IN queries do not care about order anyway; this only makes the result predictable).
func chunkIDs(ids []uint64) [][]uint64 {
	seen := make(map[uint64]bool, len(ids))
	unique := make([]uint64, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}
	var out [][]uint64
	for start := 0; start < len(unique); start += batchLookupSize {
		end := start + batchLookupSize
		if end > len(unique) {
			end = len(unique)
		}
		out = append(out, unique[start:end])
	}
	return out
}

// ByIDs batch-fetches non-soft-deleted notes by primary key, returning an id→note map; missing ids do not appear in the result.
//
// Why it is needed: queue assembly used to call ByID once per card, so the query count of one answer grew linearly with queue length.
// Batched set queries bring the query count of the assembly phase down to a constant independent of queue length.
func (s *NoteStore) ByIDs(ctx context.Context, ids []uint64) (map[uint64]*Note, error) {
	out := make(map[uint64]*Note, len(ids))
	for _, chunk := range chunkIDs(ids) {
		var notes []Note
		if err := s.db.WithContext(ctx).Where("id IN ?", chunk).Find(&notes).Error; err != nil {
			return nil, fmt.Errorf("list notes by ids: %w", err)
		}
		for i := range notes {
			out[notes[i].ID] = &notes[i]
		}
	}
	return out, nil
}

// ByIDs batch-fetches visible cards by primary key (owning note not soft-deleted), returning an id→card map.
// The visibility rule is exactly that of ByID (same visibleCards starting point), only 'one' is replaced by 'a batch'.
func (s *CardStore) ByIDs(ctx context.Context, ids []uint64) (map[uint64]*Card, error) {
	out := make(map[uint64]*Card, len(ids))
	for _, chunk := range chunkIDs(ids) {
		var cards []Card
		if err := s.visibleCards(ctx).Select("cards.*").Where("cards.id IN ?", chunk).Find(&cards).Error; err != nil {
			return nil, fmt.Errorf("list cards by ids: %w", err)
		}
		for i := range cards {
			out[cards[i].ID] = &cards[i]
		}
	}
	return out, nil
}

// ByIDs batch-fetches decks by primary key, returning an id→deck map; missing ids do not appear in the result.
func (s *DeckStore) ByIDs(ctx context.Context, ids []uint64) (map[uint64]*Deck, error) {
	out := make(map[uint64]*Deck, len(ids))
	for _, chunk := range chunkIDs(ids) {
		var decks []Deck
		if err := s.db.WithContext(ctx).Where("id IN ?", chunk).Find(&decks).Error; err != nil {
			return nil, fmt.Errorf("list decks by ids: %w", err)
		}
		for i := range decks {
			out[decks[i].ID] = &decks[i]
		}
	}
	return out, nil
}

// CardStatesByCardIDs batch-fetches one user's progress rows for a set of cards, returning a card_id→state map.
//
// card_states has no Store type of its own (reads are scattered through the api and store aggregates), so, like
// StudySettingsFor, it is provided as a package-level function with an explicit db, letting the api's due-queue assembly fetch by id set.
// When userID is 0 (not logged in / system call) it returns an empty map without issuing a query.
func CardStatesByCardIDs(ctx context.Context, db *gorm.DB, userID uint64, cardIDs []uint64) (map[uint64]CardState, error) {
	out := make(map[uint64]CardState, len(cardIDs))
	if userID == 0 {
		return out, nil
	}
	for _, chunk := range chunkIDs(cardIDs) {
		var states []CardState
		if err := db.WithContext(ctx).Where("user_id = ? AND card_id IN ?", userID, chunk).Find(&states).Error; err != nil {
			return nil, fmt.Errorf("list card states by card ids: %w", err)
		}
		for _, st := range states {
			out[st.CardID] = st
		}
	}
	return out, nil
}
