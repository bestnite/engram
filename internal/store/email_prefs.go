package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// This file is the persistence layer for (the mail type catalog and per-user preferences).
//
// Why a separate table instead of adding a JSON column to users: models.go is a single-writer hotspot (AGENTS.md §6.4),
// so adding a column to User means editing that hotspot file; a separate table needs only one line in AllModels() (this task may only change that line),
// and preferences decouple from the account lifecycle — deleting an account makes the preference rows vanish with it, with no extra users cleanup.
//
// Storage keeps only the **user's explicit choices**, never defaults derived from the catalog:
// 'can it be turned off, is it on or off by default' is the sole responsibility of the internal/mail catalog; storing a default copy here too would
// make old rows disagree with the catalog once it changes. Readers take choices and hand them to mail.ResolveEnabled for the effective value.
//
// Choices are stored as a JSON object (TEXT, works on both databases, AGENTS.md §2.3 item 4): keys are the
// string form of mail.Type, values are that type's explicit toggle. A key absent from the JSON means 'the user never chose', falling back to the catalog default.

// EmailPref is a user's mail type preferences; at most one row per user.
type EmailPref struct {
	UserID uint64 `gorm:"primaryKey;column:user_id" json:"user_id"`
	// ChoicesJSON is the JSON text of {\"<type>\": bool}; an empty string or \"{}\" means no explicit choices at all.
	ChoicesJSON string    `gorm:"column:choices_json;not null" json:"choices_json"`
	UpdatedAt   time.Time `gorm:"not null" json:"updated_at"`
}

func (EmailPref) TableName() string { return "email_prefs" }

// EmailPrefStore wraps GORM access to email_prefs.
type EmailPrefStore struct {
	db *gorm.DB
}

// NewEmailPrefStore constructs the mail preference store.
func NewEmailPrefStore(db *gorm.DB) *EmailPrefStore { return &EmailPrefStore{db: db} }

// Choices returns the user's explicit choices; returns an empty map (not nil, so callers can read it directly) when there are none.
// A missing row and empty JSON are both treated as 'no choices': the same semantics for a brand-new user and for one who cleared their preferences.
func (s *EmailPrefStore) Choices(ctx context.Context, userID uint64) (map[string]bool, error) {
	var row EmailPref
	err := s.db.WithContext(ctx).First(&row, "user_id = ?", userID).Error
	if err == gorm.ErrRecordNotFound {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load email preferences: %w", err)
	}
	return decodeChoices(row.ChoicesJSON)
}

// SetChoices overwrites all of the user's explicit choices (upsert, clause.OnConflict on both databases).
// Passing nil or an empty map is equivalent to clearing the choices, stored as \"{}\"; the row is kept, because 'exists but empty' and 'does not exist' mean the same thing.
func (s *EmailPrefStore) SetChoices(ctx context.Context, userID uint64, choices map[string]bool, at time.Time) error {
	encoded, err := encodeChoices(choices)
	if err != nil {
		return err
	}
	row := EmailPref{UserID: userID, ChoicesJSON: encoded, UpdatedAt: at.UTC()}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.Assignments(map[string]any{"choices_json": encoded, "updated_at": at.UTC()}),
	}).Create(&row).Error; err != nil {
		return fmt.Errorf("save email preferences: %w", err)
	}
	return nil
}

// decodeChoices decodes the stored JSON text into a map; an empty string / empty object returns an empty map.
// Corrupt JSON is a data defect, so it errors instead of silently becoming empty choices — silence would let users believe their settings are intact.
func decodeChoices(raw string) (map[string]bool, error) {
	if raw == "" || raw == "{}" {
		return map[string]bool{}, nil
	}
	var out map[string]bool
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("decode email preferences %q: %w", raw, err)
	}
	if out == nil {
		out = map[string]bool{}
	}
	return out, nil
}

// encodeChoices encodes the map into JSON text; nil / empty map encodes as \"{}\".
// json.Marshal sorts map keys, so the same choice set always produces identical bytes on store, which helps comparison and testing.
func encodeChoices(choices map[string]bool) (string, error) {
	if len(choices) == 0 {
		return "{}", nil
	}
	b, err := json.Marshal(choices)
	if err != nil {
		return "", fmt.Errorf("encode email preferences: %w", err)
	}
	return string(b), nil
}
