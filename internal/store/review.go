package store

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/reviewday"
)

// ReviewStore wraps reads of the reviews table. reviews is append-only,
// so there are only reads and streaming iteration here, no update/delete methods.
type ReviewStore struct {
	db *gorm.DB
}

// NewReviewStore builds the review log store.
func NewReviewStore(db *gorm.DB) *ReviewStore { return &ReviewStore{db: db} }

// OptimizerReviewLog is one standard optimizer review log line; the field names match the upstream `review_logs` schema exactly,
// so it can be fed straight to the Rust adapter.
//
// The upstream document it follows (fields and value ranges):
// https://github.com/open-spaced-repetition/fsrs-optimizer#review-logs-schema
//
//	card_id         unique identifier of the card
//	review_time     moment of the review, UTC milliseconds
//	review_rating   rating 1=Again 2=Hard 3=Good 4=Easy
//	review_state    card state before the rating 0=New 1=Learning 2=Review 3=Relearning
//	review_duration duration of this review, milliseconds, non-negative
//	timezone        the user's IANA timezone, used to define "a new day"
//	day_start       the local hour (0-23) at which a new review day begins
//
// These fields map one to one onto (card_id, reviewed_at, rating, state_before, elapsed_ms) of the reviews table
// and (timezone, day_cutoff_hour) of the users table; the integer values fall inside the upstream ranges by construction (validated when written).
type OptimizerReviewLog struct {
	// CardID corresponds to reviews.card_id.
	CardID uint64 `json:"card_id"`
	// ReviewTime is reviews.reviewed_at in UTC milliseconds (UnixMilli); the database always stores UTC.
	ReviewTime int64 `json:"review_time"`
	// ReviewRating is reviews.rating (1-4).
	ReviewRating int `json:"review_rating"`
	// ReviewState is reviews.state_before (0-3).
	ReviewState int `json:"review_state"`
	// ReviewDuration is reviews.elapsed_ms, in milliseconds and non-negative; missing or anomalous values become 0, so the field always parses.
	ReviewDuration int `json:"review_duration"`
	// Timezone is users.timezone (an IANA name).
	Timezone string `json:"timezone"`
	// DayStart is users.day_cutoff_hour (0-23).
	DayStart int `json:"day_start"`
}

// presetDeckIDsQuery is the subquery for "which decks userID currently applies presetID to": the preset column of their own decks,
// plus their member settings on shared decks. Optimizing a preset uses only the review records of those decks.
func presetDeckIDsQuery(db *gorm.DB, userID, presetID uint64) *gorm.DB {
	return db.Raw("SELECT id FROM decks WHERE owner_user_id = ? AND preset_id = ? "+
		"UNION SELECT deck_id FROM deck_member_settings WHERE user_id = ? AND preset_id = ?",
		userID, presetID, userID, presetID)
}

// presetReviewsQuery is the full review log of userID on the decks carrying presetID.
//
// Scoped by deck ownership rather than all of a user's logs: one user may give different decks different presets (language and maths kept apart, say),
// and training on all logs would give every preset the same weights, leaving "optimize per preset" in name only. Ownership is judged by the deck's **current**
// preset: when a deck switches presets, its history goes with the deck. Soft-deleting a card does not matter (the history is still valid); once a deck is deleted
// its review records have no owner and take part in no preset's optimization.
func presetReviewsQuery(ctx context.Context, db *gorm.DB, userID, presetID uint64) *gorm.DB {
	return db.WithContext(ctx).Table("reviews AS r").
		Joins("JOIN cards AS c ON c.id = r.card_id").
		Joins("JOIN notes AS n ON n.id = c.note_id").
		Where("r.user_id = ?", userID).
		Where("n.deck_id IN (?)", presetDeckIDsQuery(db, userID, presetID))
}

// StreamForPreset reads row by row through a database cursor the review log of userID on the decks carrying presetID,
// ordered by reviewed_at, id ascending (chronological, also the optimizer's implicit assumption about the log), calling emit on each row;
// it stops immediately when emit returns an error.
//
// Why streaming: the optimizer threshold is "at least several hundred reviews", but a real user may have hundreds of thousands;
// using a Rows() cursor instead of Find() keeps only the current row in memory at any moment, so peak memory does not grow with the log size
// (the same technique as ExportCards in internal/api, the precedent).
func (s *ReviewStore) StreamForPreset(ctx context.Context, userID, presetID uint64, emit func(*Review) error) error {
	rows, err := presetReviewsQuery(ctx, s.db, userID, presetID).
		Select("r.id, r.card_id, r.user_id, r.rating, r.state_before, r.reviewed_at, r.elapsed_ms").
		Order("r.reviewed_at ASC, r.id ASC").
		Rows()
	if err != nil {
		return fmt.Errorf("stream reviews for user %d preset %d: %w", userID, presetID, err)
	}
	defer rows.Close()

	for rows.Next() {
		var r Review
		if err := s.db.ScanRows(rows, &r); err != nil {
			return fmt.Errorf("scan review row for user %d: %w", userID, err)
		}
		if err := emit(&r); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate reviews for user %d: %w", userID, err)
	}
	return nil
}

// ExportOptimizerLog streams the review log of userID on the decks carrying presetID to w as JSONL in the standard schema (one JSON object per line)
// for the adapter to read. Timezone and day cutoff come from the user's row: the optimizer needs them to assign reviews that cross midnight
// to the correct day.
//
// The return value is non-nil only when reading the user, iterating the log or writing fails; a single line's encoding failure (a full disk, say) aborts the export
// rather than silently producing half a file -- the caller marks the job failed on that basis.
func (s *ReviewStore) ExportOptimizerLog(ctx context.Context, userID, presetID uint64, w io.Writer) error {
	var user User
	if err := s.db.WithContext(ctx).
		Select("timezone", "day_cutoff_hour").
		First(&user, "id = ?", userID).Error; err != nil {
		return fmt.Errorf("load user %d for review-log export: %w", userID, err)
	}
	tz := user.Timezone
	if tz == "" {
		// The column is NOT NULL, so an empty value can only come from anomalous data; the upstream wants an IANA timezone string, and falling back to UTC keeps the file valid.
		tz = "UTC"
	}
	// The export must use the same cutoff as grading, the queue and stats: NULL means 4, an explicit 0 means midnight.
	dayStart := reviewday.ResolveCutoff(user.DayCutoffHour)

	enc := json.NewEncoder(w)
	return s.StreamForPreset(ctx, userID, presetID, func(r *Review) error {
		return enc.Encode(OptimizerReviewLog{
			CardID:         r.CardID,
			ReviewTime:     r.ReviewedAt.UTC().UnixMilli(),
			ReviewRating:   r.Rating,
			ReviewState:    r.StateBefore,
			ReviewDuration: clampDurationMS(r.ElapsedMS),
			Timezone:       tz,
			DayStart:       dayStart,
		})
	})
}

// clampDurationMS turns a nullable elapsed_ms into non-negative milliseconds: NULL or a negative value becomes 0. The upstream requires
// review_duration to be non-negative and omittable; writing 0 explicitly serves downstream row-by-row parsing better than omitting it.
func clampDurationMS(ms *int) int {
	if ms == nil || *ms < 0 {
		return 0
	}
	return *ms
}
