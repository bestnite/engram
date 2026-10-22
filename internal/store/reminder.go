package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The send ledger for review-due reminders (category C).
//
// Why a small table rather than something else:
//   - "at most one per user per day" must still hold across a process restart. An in-process counter resets to zero on restart,
//     so the same day would send another one after a restart -- exactly what the hard cap exists to prevent.
//   - the primary key (user_id, day) makes the unique constraint the last line of defence: even if two workers decide concurrently,
//     the database accepts only one row, without relying on the caller's timing being right.
//   - a separate table rather than a column on users: models.go is a single-writer hotspot (AGENTS.md §6.4),
//     adding a column to User means touching that hotspot file; a separate table needs one line in AllModels(), and the ledger is decoupled from
//     the account lifecycle -- deleting an account takes the row with it, with no extra cleanup.
//
// Day uses the "user's local review day" (YYYY-MM-DD, split by day_cutoff_hour), the same timezone/cutoff definition as reviews.review_day;
// a UTC date is not used, otherwise a user in another timezone would be judged as two different days in the local early morning.
type ReminderLog struct {
	UserID uint64 `gorm:"primaryKey;column:user_id" json:"user_id"`
	// Day is the user's local review day, YYYY-MM-DD (the same definition as reviews.review_day).
	Day string `gorm:"primaryKey;column:day" json:"day"`
	// SentAt is the moment it was actually enqueued (UTC).
	SentAt time.Time `gorm:"column:sent_at;not null" json:"sent_at"`
}

// TableName pins the table name so the pluralization rule cannot drift across GORM versions.
func (ReminderLog) TableName() string { return "reminder_log" }

// ReminderCandidate holds the user fields the reminder worker needs plus their current due-card count.
// Only active users with due cards appear in the result -- an account with nothing due should not be nudged.
type ReminderCandidate struct {
	ID            uint64 `gorm:"column:id" json:"id"`
	Email         string `gorm:"column:email" json:"email"`
	Locale        string `gorm:"column:locale" json:"locale"`
	Timezone      string `gorm:"column:timezone" json:"timezone"`
	DayCutoffHour *int   `gorm:"column:day_cutoff_hour" json:"day_cutoff_hour"`
	// ReminderHour is the local send hour the user chose; NULL means unset (use the global default).
	ReminderHour *int `gorm:"column:reminder_hour" json:"reminder_hour,omitempty"`
	// DueCount is the number of the user's card_states rows currently due (one card counts once per user).
	DueCount int64 `gorm:"column:due_count" json:"due_count"`
}

// ReminderCandidates returns the active users who have due cards and may receive mail.
//
// The due definition matches the schedule queue and DueQueueSize: only card_states that are scheduled with due_at <= now,
// excluding soft-deleted cards/notes and suspended cards. One user may have several rows, so GROUP BY
// merges them and yields the due count along the way.
func ReminderCandidates(ctx context.Context, db *gorm.DB, now time.Time) ([]ReminderCandidate, error) {
	var out []ReminderCandidate
	err := db.WithContext(ctx).Table("users AS u").
		Select("u.id AS id, u.email AS email, u.locale AS locale, u.timezone AS timezone, "+
			"u.day_cutoff_hour AS day_cutoff_hour, u.reminder_hour AS reminder_hour, COUNT(cs.card_id) AS due_count").
		Joins("JOIN card_states AS cs ON cs.user_id = u.id AND cs.due_at IS NOT NULL AND cs.due_at <= ?", now.UTC()).
		Joins("JOIN cards AS c ON c.id = cs.card_id AND c.deleted_at IS NULL AND cs.suspended_at IS NULL").
		Joins("JOIN notes AS n ON n.id = c.note_id AND n.deleted_at IS NULL").
		Where("u.status = ? AND u.email <> ''", StatusActive).
		Group("u.id, u.email, u.locale, u.timezone, u.day_cutoff_hour, u.reminder_hour").
		Order("u.id asc").
		Scan(&out).Error
	if err != nil {
		return nil, fmt.Errorf("load reminder candidates: %w", err)
	}
	return out, nil
}

// HasReminderBeenSent reports whether a reminder has already been sent to the user on their local review day day.
func HasReminderBeenSent(ctx context.Context, db *gorm.DB, userID uint64, day string) (bool, error) {
	var n int64
	if err := db.WithContext(ctx).Model(&ReminderLog{}).
		Where("user_id = ? AND day = ?", userID, day).
		Count(&n).Error; err != nil {
		return false, fmt.Errorf("check reminder log: %w", err)
	}
	return n > 0, nil
}

// RecordReminderSent records one enqueued reminder. On a primary-key conflict it skips silently (DoNothing):
// the unique constraint is the backstop for "one per day", and a duplicate record should not make the caller error, still less block sending.
func RecordReminderSent(ctx context.Context, db *gorm.DB, userID uint64, day string, at time.Time) error {
	row := ReminderLog{UserID: userID, Day: day, SentAt: at.UTC()}
	if err := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return fmt.Errorf("record reminder sent: %w", err)
	}
	return nil
}

// LastReminderSentAt returns the moment of the user's most recent successfully enqueued reminder (the largest SentAt across review days).
//
// It serves the sender's minimum-interval gate: the interval check wants "how long since the last send", which only the SentAt timestamp can give;
// Day is a YYYY-MM-DD string without a time of day, so it cannot tell "how many hours passed today", which is why this reads
// the SentAt column (reusing an existing column, adding none). ok=false means the user has never been sent a reminder.
func LastReminderSentAt(ctx context.Context, db *gorm.DB, userID uint64) (time.Time, bool, error) {
	var row ReminderLog
	err := db.WithContext(ctx).Model(&ReminderLog{}).
		Where("user_id = ?", userID).
		Order("sent_at desc").
		Limit(1).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("load last reminder sent: %w", err)
	}
	return row.SentAt.UTC(), true, nil
}
