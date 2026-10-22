package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Send ledger for the study digest (class C).
//
// Why a separate table instead of reusing reminder_log: reminder_log's primary key is (user_id, day),
// where day is the 'user's local review day' — weekly granularity would collide with that key (one review day can belong to this week or
// the next), so the semantics cannot coexist. Adding a NOT NULL column to an existing table counts as a 'new non-null constraint' and requires a versioned destructive
// migration (AGENTS.md §2.3 item 5, store.Migration); a new table is purely additive, AutoMigrate handles
// it directly, and the migration surface is minimal.
//
// 'Exactly one mail per person per week' must hold across process restarts, so dedup lands in the database rather than an in-process counter: the unique
// constraint on the primary key (user_id, week_start) is the last line of defense — even if two workers decide concurrently, the DB accepts only one row.
//
// WeekStart is the YYYY-MM-DD of the user's **local week start day** (Monday), not a UTC date: users across timezones must land in
// the same week at local Monday early morning, otherwise 'two mails in one week' or 'one mail short' appears.
//
// The new table: a purely additive table, handled directly by AutoMigrate, needing no one-off migration function.
type DigestLog struct {
	UserID uint64 `gorm:"primaryKey;column:user_id" json:"user_id"`
	// WeekStart is the user's local week start day (Monday), YYYY-MM-DD.
	WeekStart string `gorm:"primaryKey;column:week_start" json:"week_start"`
	// SentAt is the actual enqueue time (UTC).
	SentAt time.Time `gorm:"column:sent_at;not null" json:"sent_at"`
}

// TableName pins the table name so pluralization rules cannot drift across GORM versions.
func (DigestLog) TableName() string { return "digest_log" }

// DigestCandidate holds the user fields the digest worker needs.
// Unlike reminders, the digest goes to every active user who can receive mail, with no due cards required — someone who has not
// reviewed receives a retrospective of 'no reviews in this period', which is exactly its positioning as a review and a sense of continuity (rather than a nudge).
type DigestCandidate struct {
	ID            uint64 `gorm:"column:id" json:"id"`
	Email         string `gorm:"column:email" json:"email"`
	Locale        string `gorm:"column:locale" json:"locale"`
	Timezone      string `gorm:"column:timezone" json:"timezone"`
	DayCutoffHour *int   `gorm:"column:day_cutoff_hour" json:"day_cutoff_hour"`
	// ReminderHour is the local send hour the user picked; NULL means unset (use the global default). The weekly digest and the review
	// reminder share the same hour, so they read the same column.
	ReminderHour *int `gorm:"column:reminder_hour" json:"reminder_hour,omitempty"`
}

// DigestCandidates returns every active user who can receive mail (has an email). Ordered by id ascending, so each round has a stable order.
func DigestCandidates(ctx context.Context, db *gorm.DB) ([]DigestCandidate, error) {
	var out []DigestCandidate
	err := db.WithContext(ctx).Table("users").
		Select("id AS id, email AS email, locale AS locale, timezone AS timezone, day_cutoff_hour AS day_cutoff_hour, reminder_hour AS reminder_hour").
		Where("status = ? AND email <> ''", StatusActive).
		Order("id asc").
		Scan(&out).Error
	if err != nil {
		return nil, fmt.Errorf("load digest candidates: %w", err)
	}
	return out, nil
}

// HasDigestBeenSent reports whether a digest was already sent to this user on their local week start day weekStart.
func HasDigestBeenSent(ctx context.Context, db *gorm.DB, userID uint64, weekStart string) (bool, error) {
	var n int64
	if err := db.WithContext(ctx).Model(&DigestLog{}).
		Where("user_id = ? AND week_start = ?", userID, weekStart).
		Count(&n).Error; err != nil {
		return false, fmt.Errorf("check digest log: %w", err)
	}
	return n > 0, nil
}

// LastDigestSentAt takes the most recent enqueue time across weeks; an empty ledger is not a query failure.
func LastDigestSentAt(ctx context.Context, db *gorm.DB, userID uint64) (time.Time, bool, error) {
	var row DigestLog
	err := db.WithContext(ctx).Select("sent_at").Where("user_id = ?", userID).Order("sent_at DESC").Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("load last digest sent time: %w", err)
	}
	return row.SentAt, true, nil
}

// RecordDigestSent records one enqueued digest. On primary key conflict it skips silently (DoNothing):
// the unique constraint is the backstop for 'one per week', and a duplicate record must not make the caller error, let alone block sending.
func RecordDigestSent(ctx context.Context, db *gorm.DB, userID uint64, weekStart string, at time.Time) error {
	row := DigestLog{UserID: userID, WeekStart: weekStart, SentAt: at.UTC()}
	if err := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return fmt.Errorf("record digest sent: %w", err)
	}
	return nil
}
