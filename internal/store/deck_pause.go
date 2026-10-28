package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DeckPause records that one user paused one deck for themselves.
//
// Pausing is a study choice of the user, not a property of the deck: the owner and every member of a shared deck pause
// for themselves only, so the row is keyed (deck, user) and decks.archived_at is never involved. A row means "paused";
// resuming deletes the row, so no row is the normal state.
//
// A paused deck leaves the user's default scope (activeDeckIDsQuery): the queue with no deck named, the due count of the
// stats summary, the unfiltered due forecast and the reminder mail. It stays visible, and naming it explicitly still studies it.
type DeckPause struct {
	DeckID   uint64    `gorm:"primaryKey" json:"-"`
	UserID   uint64    `gorm:"primaryKey;index" json:"-"`
	PausedAt time.Time `gorm:"not null" json:"paused_at"`
}

func (DeckPause) TableName() string { return "deck_pauses" }

// pausedDeckIDsQuery returns the subquery of the deck ids userID has paused.
func pausedDeckIDsQuery(db *gorm.DB, userID uint64) *gorm.DB {
	return db.Model(&DeckPause{}).Select("deck_id").Where("user_id = ?", userID)
}

// activeDeckIDsQuery returns the subquery of the deck ids in userID's default study scope: the visible decks minus the paused ones.
// It is the single source of that rule; the queue's whole-library mode (ActiveIDs), the stats summary's due count and the unfiltered
// due forecast all use it, so the home number, the queue and the digest cannot disagree about which decks count.
func activeDeckIDsQuery(db *gorm.DB, userID uint64) *gorm.DB {
	return db.Model(&Deck{}).Select("id").
		Where("id IN (?) AND id NOT IN (?)", visibleDeckIDsQuery(db, userID), pausedDeckIDsQuery(db, userID))
}

// ActiveIDs returns the ids of the decks in userID's default study scope (visible and not paused), in ascending order.
// An empty set means there is nothing to study by default, not an error.
func (s *DeckStore) ActiveIDs(ctx context.Context, userID uint64) ([]uint64, error) {
	var ids []uint64
	if err := s.db.WithContext(ctx).Model(&Deck{}).
		Where("id IN (?)", activeDeckIDsQuery(s.db, userID)).
		Order("id ASC").Pluck("id", &ids).Error; err != nil {
		return nil, fmt.Errorf("list active deck ids: %w", err)
	}
	return ids, nil
}

// IsPaused reports whether userID has paused deckID.
func (s *DeckStore) IsPaused(ctx context.Context, userID, deckID uint64) (bool, error) {
	var row DeckPause
	err := s.db.WithContext(ctx).Where("deck_id = ? AND user_id = ?", deckID, userID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read deck pause: %w", err)
	}
	return true, nil
}

// SetPaused pauses or resumes deckID for userID and reports whether anything changed.
// Both directions are idempotent: pausing a paused deck keeps the original paused_at, and resuming an active deck writes nothing.
// The caller checks that the user can see the deck; this method only writes the row.
func (s *DeckStore) SetPaused(ctx context.Context, userID, deckID uint64, paused bool, at time.Time) (bool, error) {
	db := s.db.WithContext(ctx)
	if !paused {
		res := db.Where("deck_id = ? AND user_id = ?", deckID, userID).Delete(&DeckPause{})
		if res.Error != nil {
			return false, fmt.Errorf("resume deck: %w", res.Error)
		}
		return res.RowsAffected > 0, nil
	}
	if at.IsZero() {
		at = time.Now()
	}
	row := DeckPause{DeckID: deckID, UserID: userID, PausedAt: at.UTC()}
	res := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if res.Error != nil {
		return false, fmt.Errorf("pause deck: %w", res.Error)
	}
	return res.RowsAffected > 0, nil
}
