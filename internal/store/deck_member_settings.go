package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Per-user, per-deck study settings (scheduling preset and daily caps).
//
// Progress has long been separated per user (the card_states primary key is (card_id, user_id)); study settings must be separated per user
// for the same reason: members of a shared deck review with their own memory parameters and introduce new cards at their own pace, not governed by the owner's preset and caps.
//
// Storage lives in two places by identity, and reads and writes only go through StudySettings / SetStudySettings in this file:
//   - owner: the preset_id / new_per_day / reviews_per_day columns on the decks table (they have always been the owner's settings,
//     left as they are to avoid data migrations and touching many call sites);
//   - other members: the deck_member_settings table, one row per (deck, user). The row is created when a grant is written,
//     initialized to the member's own default preset and default caps; it is deleted with the grant, the deck or the user.

// DeckMemberSetting is one non-owner member's study settings for a shared deck.
// The two cap columns carry no database default: 0 is a legal value (unlimited), and an integer zero with a default would be omitted by GORM and overwritten by that default.
type DeckMemberSetting struct {
	DeckID        uint64    `gorm:"primaryKey" json:"-"`
	UserID        uint64    `gorm:"primaryKey;index" json:"-"`
	PresetID      uint64    `gorm:"not null;index" json:"-"`
	NewPerDay     int       `gorm:"not null" json:"new_per_day"`
	ReviewsPerDay int       `gorm:"not null" json:"reviews_per_day"`
	UpdatedAt     time.Time `gorm:"not null" json:"updated_at"`
}

func (DeckMemberSetting) TableName() string { return "deck_member_settings" }

// StudySettings is the settings in effect when a user reviews a deck.
type StudySettings struct {
	PresetID uint64
	Caps     DeckCaps
}

// StudySettingsFor returns userID's study settings on each given deck (keys are deck primary keys).
//
// The owner reads the deck columns; a member reads deck_member_settings. When the member row is missing (e.g. a grant made before
// this table existed, before the migration backfills it) it falls back to the member's own default preset and default caps — never to the owner's settings, which would apply the owner's parameters to someone else.
func StudySettingsFor(ctx context.Context, db *gorm.DB, userID uint64, decks []Deck) (map[uint64]StudySettings, error) {
	out := make(map[uint64]StudySettings, len(decks))
	var memberDecks []uint64
	for _, d := range decks {
		if d.OwnerUserID == userID {
			out[d.ID] = StudySettings{PresetID: d.PresetID, Caps: DeckCaps{NewPerDay: d.NewPerDay, ReviewsPerDay: d.ReviewsPerDay}}
			continue
		}
		memberDecks = append(memberDecks, d.ID)
	}
	if len(memberDecks) == 0 {
		return out, nil
	}
	var rows []DeckMemberSetting
	if err := db.WithContext(ctx).Where("user_id = ? AND deck_id IN ?", userID, memberDecks).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("store: load member study settings: %w", err)
	}
	for _, r := range rows {
		out[r.DeckID] = StudySettings{PresetID: r.PresetID, Caps: DeckCaps{NewPerDay: r.NewPerDay, ReviewsPerDay: r.ReviewsPerDay}}
	}
	if len(rows) == len(memberDecks) {
		return out, nil
	}
	fallback, err := defaultStudySettings(ctx, db, userID)
	if err != nil {
		return nil, err
	}
	for _, id := range memberDecks {
		if _, ok := out[id]; !ok {
			out[id] = fallback
		}
	}
	return out, nil
}

// StudySettings returns userID's study settings on a single deck (rules in StudySettingsFor).
func (s *DeckStore) StudySettings(ctx context.Context, userID uint64, deck *Deck) (StudySettings, error) {
	all, err := StudySettingsFor(ctx, s.db, userID, []Deck{*deck})
	if err != nil {
		return StudySettings{}, err
	}
	return all[deck.ID], nil
}

// defaultStudySettings is a member's initial settings: their own default preset (created if absent) and the deck's default caps.
func defaultStudySettings(ctx context.Context, db *gorm.DB, userID uint64) (StudySettings, error) {
	presets, err := EnsureDefaultPreset(ctx, db, userID)
	if err != nil {
		return StudySettings{}, fmt.Errorf("store: ensure default preset for user %d: %w", userID, err)
	}
	p := DefaultPreset(presets)
	if p == nil {
		return StudySettings{}, ErrNoDefaultPreset
	}
	return StudySettings{PresetID: p.ID, Caps: DeckCaps{NewPerDay: DefaultNewPerDay, ReviewsPerDay: DefaultReviewsPerDay}}, nil
}

// ensureMemberSettings creates a row with the initial values when the member has no settings row yet; an existing row is left alone (a role change does not reset settings).
func ensureMemberSettings(ctx context.Context, db *gorm.DB, deckID, userID uint64) error {
	initial, err := defaultStudySettings(ctx, db, userID)
	if err != nil {
		return err
	}
	row := DeckMemberSetting{
		DeckID: deckID, UserID: userID, PresetID: initial.PresetID,
		NewPerDay: initial.Caps.NewPerDay, ReviewsPerDay: initial.Caps.ReviewsPerDay, UpdatedAt: time.Now().UTC(),
	}
	if err := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return fmt.Errorf("store: create member study settings: %w", err)
	}
	return nil
}

// SetStudySettings changes userID's study settings on a deck; fields whose presetID or caps is nil are left unchanged.
//
// The preset must belong to userID itself (otherwise ErrDeckPresetInvalid): hanging someone else's preset on the deck would let
// their parameters be read through 'the deck is readable'. The caller must confirm that userID is at least a reader of the deck.
func (s *DeckStore) SetStudySettings(ctx context.Context, userID uint64, deck *Deck, presetID *uint64, caps *DeckCaps) error {
	updates := map[string]any{}
	if presetID != nil {
		var count int64
		if err := s.db.WithContext(ctx).Model(&Preset{}).
			Where("id = ? AND owner_user_id = ?", *presetID, userID).Count(&count).Error; err != nil {
			return fmt.Errorf("check preset ownership: %w", err)
		}
		if count == 0 {
			return ErrDeckPresetInvalid
		}
		updates["preset_id"] = *presetID
	}
	if caps != nil {
		if err := validDeckCaps(*caps); err != nil {
			return err
		}
		updates["new_per_day"] = caps.NewPerDay
		updates["reviews_per_day"] = caps.ReviewsPerDay
	}
	if len(updates) == 0 {
		return nil
	}
	if deck.OwnerUserID == userID {
		if err := s.db.WithContext(ctx).Model(&Deck{}).Where("id = ?", deck.ID).Updates(updates).Error; err != nil {
			return fmt.Errorf("update deck %d settings: %w", deck.ID, err)
		}
		return nil
	}
	if err := ensureMemberSettings(ctx, s.db, deck.ID, userID); err != nil {
		return err
	}
	updates["updated_at"] = time.Now().UTC()
	res := s.db.WithContext(ctx).Model(&DeckMemberSetting{}).
		Where("deck_id = ? AND user_id = ?", deck.ID, userID).Updates(updates)
	if res.Error != nil {
		return fmt.Errorf("update member settings of deck %d: %w", deck.ID, res.Error)
	}
	if res.RowsAffected == 0 {
		return errors.New("store: member settings row disappeared during update")
	}
	return nil
}
