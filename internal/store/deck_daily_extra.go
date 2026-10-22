package store

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DeckDailyExtra records the extra new-card quota a user added with 'one more round' on a deck, for one review day.
//
// The daily new-card cap is a study goal, not a hard limit: after finishing today's load, a user can pull in an extra batch of new
// cards for today and this deck only, and automatically returns to the original cap the next day. The quota must be stored, not passed as a request parameter, because the queue is
// rebuilt after every rating, so a page refresh or a different entry point (REST / MCP) must see the same extra amount.
//
// The primary key includes review_day, so 'valid only for today' needs no cleanup job: other review days cannot find this row.
// The write also deletes rows for earlier review days of the same (deck, user) pair, leaving at most one row per (deck, user).
// Owners and shared members write this table alike: the extra amount is the user's own study pace and does not affect other members of the deck.
type DeckDailyExtra struct {
	DeckID uint64 `gorm:"primaryKey" json:"-"`
	UserID uint64 `gorm:"primaryKey;index" json:"-"`
	// ReviewDay is the user's own review day (YYYY-MM-DD), same rule as reviews.review_day.
	ReviewDay string    `gorm:"primaryKey;size:10" json:"review_day"`
	ExtraNew  int       `gorm:"not null" json:"extra_new"`
	UpdatedAt time.Time `gorm:"not null" json:"updated_at"`
}

func (DeckDailyExtra) TableName() string { return "deck_daily_extras" }

// AddDailyExtraNew adds n to the extra new-card quota of (deck, user, review day) and returns the accumulated value.
// Rows for earlier review days of the same (deck, user) are deleted in the same transaction: they no longer take effect. n must be positive.
func AddDailyExtraNew(ctx context.Context, db *gorm.DB, deckID, userID uint64, day string, n int) (int, error) {
	if n <= 0 {
		return 0, fmt.Errorf("add daily extra new: n must be positive, got %d", n)
	}
	var total int
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("deck_id = ? AND user_id = ? AND review_day < ?", deckID, userID, day).
			Delete(&DeckDailyExtra{}).Error; err != nil {
			return fmt.Errorf("delete stale daily extras: %w", err)
		}
		now := time.Now().UTC()
		row := DeckDailyExtra{DeckID: deckID, UserID: userID, ReviewDay: day, ExtraNew: n, UpdatedAt: now}
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "deck_id"}, {Name: "user_id"}, {Name: "review_day"}},
			DoUpdates: clause.Assignments(map[string]any{
				"extra_new":  gorm.Expr("deck_daily_extras.extra_new + ?", n),
				"updated_at": now,
			}),
		}).Create(&row).Error; err != nil {
			return fmt.Errorf("upsert daily extra: %w", err)
		}
		var saved DeckDailyExtra
		if err := tx.Where("deck_id = ? AND user_id = ? AND review_day = ?", deckID, userID, day).
			Take(&saved).Error; err != nil {
			return fmt.Errorf("read daily extra: %w", err)
		}
		total = saved.ExtraNew
		return nil
	})
	return total, err
}

// DailyExtraNew returns, for userID on the given review day, the extra new-card quota per deck (keys are deck primary keys);
// decks with no extra do not appear in the result and callers treat them as 0.
func DailyExtraNew(ctx context.Context, db *gorm.DB, userID uint64, day string, deckIDs []uint64) (map[uint64]int, error) {
	out := make(map[uint64]int, len(deckIDs))
	if len(deckIDs) == 0 {
		return out, nil
	}
	var rows []DeckDailyExtra
	if err := db.WithContext(ctx).Where("user_id = ? AND review_day = ? AND deck_id IN ?", userID, day, deckIDs).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load daily extras: %w", err)
	}
	for _, r := range rows {
		out[r.DeckID] = r.ExtraNew
	}
	return out, nil
}
