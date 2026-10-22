package store

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// The mail outbox.
//
// Delivery design: enqueueing only writes this queue table; a background worker delivers with retries and backoff, and nothing ever sends mail synchronously on the request path.
// A send failure must not fail the operation that triggered it, so Enqueue only writes one pending row and the worker writes the delivery outcome back.
//
// State machine: pending -> sent | failed.
//   - pending: waiting for delivery; the worker only picks it up once next_attempt_at has arrived (this is where backoff lives).
//   - sent: delivered successfully, sent_at is recorded.
//   - failed: the maximum attempt count was exceeded; last_error is kept so the admin panel can show the final error.
//
// Both the backoff and the "last error is visible" requirement live in this one row, so the admin panel needs no separate report.
const (
	// OutboxStatusPending means waiting for delivery.
	OutboxStatusPending = "pending"
	// OutboxStatusSent means delivered successfully.
	OutboxStatusSent = "sent"
	// OutboxStatusFailed means permanently failed after the retries ran out.
	OutboxStatusFailed = "failed"
)

// OutboxMessage is one mail awaiting delivery (or already delivered).
//
// The table name is pinned to mail_outbox so the pluralization rule cannot drift. The body and custom headers are stored as TEXT,
// compatible with both databases (no jsonb/array). HeadersJSON is the JSON of a map[string]string.
type OutboxMessage struct {
	ID uint64 `gorm:"primaryKey" json:"id"`
	// To is the recipient address (the login email only).
	To string `gorm:"not null" json:"to"`
	// Type is a free-form mail type string; the catalogue is defined by , this only stores it verbatim.
	Type string `gorm:"not null" json:"type"`
	// Subject is the subject; user-visible text is already localized when enqueued, this only stores the result.
	Subject  string `gorm:"not null" json:"subject"`
	TextBody string `gorm:"column:text_body;not null" json:"text_body"`
	HTMLBody string `gorm:"column:html_body;not null" json:"html_body"`
	// HeadersJSON is the JSON encoding of extra mail headers (List-Unsubscribe, for instance).
	HeadersJSON string `gorm:"column:headers_json;not null" json:"headers_json"`
	// Status is pending | sent | failed; together with idx_outbox_due it serves the "due for delivery" query.
	Status string `gorm:"not null;index:idx_outbox_due,priority:1" json:"status"`
	// Attempts is the number of delivery attempts made.
	Attempts int `gorm:"not null;default:0" json:"attempts"`
	// NextAttemptAt is the next deliverable time (UTC); backoff reschedules a failure into the future.
	NextAttemptAt time.Time `gorm:"not null;index:idx_outbox_due,priority:2" json:"next_attempt_at"`
	// LastError is the server-side error text of the most recent delivery failure; empty on success or before any failure.
	LastError *string    `gorm:"column:last_error" json:"last_error,omitempty"`
	CreatedAt time.Time  `gorm:"not null" json:"created_at"`
	UpdatedAt time.Time  `gorm:"not null" json:"updated_at"`
	SentAt    *time.Time `json:"sent_at,omitempty"`
}

// TableName pins the table name so the pluralization rule cannot drift across GORM versions.
func (OutboxMessage) TableName() string { return "mail_outbox" }

// EnqueueOutboxMessage writes one pending mail: it forces pending / attempts=0 and sets the creation and first-attempt times.
// This is the only write entry point, so callers cannot get the status fields wrong.
func EnqueueOutboxMessage(ctx context.Context, db *gorm.DB, row *OutboxMessage, now time.Time) error {
	if row == nil {
		return fmt.Errorf("store: outbox message is required")
	}
	row.Status = OutboxStatusPending
	row.Attempts = 0
	row.LastError = nil
	row.SentAt = nil
	row.CreatedAt = now.UTC()
	row.UpdatedAt = now.UTC()
	row.NextAttemptAt = now.UTC()
	if err := db.WithContext(ctx).Create(row).Error; err != nil {
		return fmt.Errorf("enqueue outbox message: %w", err)
	}
	return nil
}

// DueOutboxMessages returns pending mails whose next_attempt_at has arrived (<= now), ordered by due time ascending.
// limit <= 0 means no limit (callers normally pass a batch cap).
func DueOutboxMessages(ctx context.Context, db *gorm.DB, now time.Time, limit int) ([]OutboxMessage, error) {
	q := db.WithContext(ctx).
		Where("status = ? AND next_attempt_at <= ?", OutboxStatusPending, now.UTC()).
		Order("next_attempt_at asc, id asc")
	if limit > 0 {
		q = q.Limit(limit)
	}
	var rows []OutboxMessage
	if err := q.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load due outbox messages: %w", err)
	}
	return rows, nil
}

// MarkOutboxSent marks a mail as delivered successfully.
func MarkOutboxSent(ctx context.Context, db *gorm.DB, id uint64, at time.Time) error {
	if err := db.WithContext(ctx).Model(&OutboxMessage{}).Where("id = ?", id).Updates(map[string]any{
		"status":     OutboxStatusSent,
		"sent_at":    at.UTC(),
		"updated_at": at.UTC(),
	}).Error; err != nil {
		return fmt.Errorf("mark outbox message %d sent: %w", id, err)
	}
	return nil
}

// MarkOutboxRetry records one failure and reschedules the mail to nextAt (backoff); the status stays pending.
func MarkOutboxRetry(ctx context.Context, db *gorm.DB, id uint64, attempts int, nextAt time.Time, lastErr string, at time.Time) error {
	if err := db.WithContext(ctx).Model(&OutboxMessage{}).Where("id = ?", id).Updates(map[string]any{
		"status":          OutboxStatusPending,
		"attempts":        attempts,
		"next_attempt_at": nextAt.UTC(),
		"last_error":      lastErr,
		"updated_at":      at.UTC(),
	}).Error; err != nil {
		return fmt.Errorf("reschedule outbox message %d: %w", id, err)
	}
	return nil
}

// MarkOutboxFailed marks a mail as permanently failed once the retries are exhausted, keeping the last error.
func MarkOutboxFailed(ctx context.Context, db *gorm.DB, id uint64, attempts int, lastErr string, at time.Time) error {
	if err := db.WithContext(ctx).Model(&OutboxMessage{}).Where("id = ?", id).Updates(map[string]any{
		"status":     OutboxStatusFailed,
		"attempts":   attempts,
		"last_error": lastErr,
		"updated_at": at.UTC(),
	}).Error; err != nil {
		return fmt.Errorf("mark outbox message %d failed: %w", id, err)
	}
	return nil
}

// OutboxSummary is the outbox reading the admin panel needs: counts per status plus the most recent error.
// It performs read-only aggregation and changes no row.
type OutboxSummary struct {
	Pending int64
	Sent    int64
	Failed  int64
	// LastError is the server-side error text of the most recent failure; empty when nothing has failed.
	LastError string
	// LastErrorAt is when the error above was recorded.
	LastErrorAt *time.Time
	// LastAttempts is that mail's attempt count when the error above was produced.
	LastAttempts int
}

// OutboxSummaryOf summarizes the outbox: count per status and the most recent row carrying an error as the "last error".
// Rows with an empty error do not take part in choosing LastError (otherwise a successful delivery would overwrite the previous error with empty).
func OutboxSummaryOf(ctx context.Context, db *gorm.DB) (OutboxSummary, error) {
	var out OutboxSummary
	for _, tc := range []struct {
		status string
		dst    *int64
	}{
		{OutboxStatusPending, &out.Pending},
		{OutboxStatusSent, &out.Sent},
		{OutboxStatusFailed, &out.Failed},
	} {
		if err := db.WithContext(ctx).Model(&OutboxMessage{}).Where("status = ?", tc.status).Count(tc.dst).Error; err != nil {
			return OutboxSummary{}, fmt.Errorf("count outbox messages (%s): %w", tc.status, err)
		}
	}
	var row OutboxMessage
	err := db.WithContext(ctx).
		Where("last_error IS NOT NULL AND last_error <> ''").
		Order("updated_at desc, id desc").
		First(&row).Error
	if err != nil {
		if IsNotFound(err) {
			return out, nil
		}
		return OutboxSummary{}, fmt.Errorf("load last outbox error: %w", err)
	}
	if row.LastError != nil {
		out.LastError = *row.LastError
	}
	at := row.UpdatedAt
	out.LastErrorAt = &at
	out.LastAttempts = row.Attempts
	return out, nil
}
