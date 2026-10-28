package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrShareInviteNotFound means there is no pending invite for that (deck, user). It is a **normal state** (no invite);
// the sentinel exists so callers can tell 'none' apart from 'the query failed'.
var ErrShareInviteNotFound = errors.New("deck share invite not found")

// DeckShareInviteStore reads and writes pending share invites.
//
// Grants (deck_grants) are written only on accept, so this table **takes part in no visibility decision**: a pending invite
// must not let anyone see the deck's content.
type DeckShareInviteStore struct{ db *gorm.DB }

// NewDeckShareInviteStore constructs the invite store.
func NewDeckShareInviteStore(db *gorm.DB) *DeckShareInviteStore {
	return &DeckShareInviteStore{db: db}
}

// Invite writes or refreshes an invite. There is only one row per (deck, user); inviting again refreshes the role and expiry
// instead of stacking — otherwise 'invited twice' would show two identical pending entries in the list.
func (s *DeckShareInviteStore) Invite(ctx context.Context, inv DeckShareInvite) error {
	if inv.CreatedAt.IsZero() {
		inv.CreatedAt = time.Now().UTC()
	}
	err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "deck_id"}, {Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"role", "invited_by", "created_at", "expires_at"}),
	}).Create(&inv).Error
	if err != nil {
		return fmt.Errorf("save deck share invite: %w", err)
	}
	return nil
}

// ByDeckAndUser fetches one invite; ErrShareInviteNotFound when there is none.
func (s *DeckShareInviteStore) ByDeckAndUser(ctx context.Context, deckID, userID uint64) (*DeckShareInvite, error) {
	var inv DeckShareInvite
	err := s.db.WithContext(ctx).
		Where("deck_id = ? AND user_id = ?", deckID, userID).
		Take(&inv).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrShareInviteNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load deck share invite: %w", err)
	}
	return &inv, nil
}

// DeckShareInviteView is a pending invite with display information.
//
// The recipient's view uses DeckName + InviterName (deck name, who invited me); the owner's view uses Username
// (whom I invited). Both views share this struct because they are two readings of the same row.
// All three ids are public ids (UUIDv7 text): numeric primary keys are not exposed, the query joins public_id and fills it in.
type DeckShareInviteView struct {
	DeckID   string `json:"deck_id"`
	DeckName string `json:"deck_name"`
	UserID   string `json:"user_id"`
	// Username is the invitee's username (owner's view).
	Username string `json:"username"`
	Role     string `json:"role"`
	// InvitedBy and InviterName are the inviter (recipient's view).
	InvitedBy   string    `json:"invited_by"`
	InviterName string    `json:"inviter_name"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// ListForDeck lists a deck's pending invites (owner's view: whom I invited who has not answered yet), newest first.
func (s *DeckShareInviteStore) ListForDeck(ctx context.Context, deckID uint64) ([]DeckShareInviteView, error) {
	// Start from an empty slice, not nil: Scan leaves a nil slice untouched when no row matches, and a nil slice encodes as JSON null.
	rows := []DeckShareInviteView{}
	err := s.db.WithContext(ctx).
		Table("deck_share_invites AS i").
		Select("COALESCE(d.public_id, '') AS deck_id, '' AS deck_name, COALESCE(u.public_id, '') AS user_id, COALESCE(u.username, '') AS username, i.role, COALESCE(v.public_id, '') AS invited_by, COALESCE(v.display_name, v.username, '') AS inviter_name, i.created_at, i.expires_at").
		Joins("LEFT JOIN decks AS d ON d.id = i.deck_id").
		Joins("LEFT JOIN users AS u ON u.id = i.user_id").
		Joins("LEFT JOIN users AS v ON v.id = i.invited_by").
		Where("i.deck_id = ? AND i.expires_at > ?", deckID, time.Now().UTC()).
		Order("i.created_at DESC").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list deck share invites for deck: %w", err)
	}
	return rows, nil
}

// ListForUser lists a user's pending invites, newest first.
//
// Only unexpired ones count: expired rows may not have been cleaned up by the retention worker yet, so the read path must judge for itself — otherwise the UI shows
// ghost entries where 'clicked accept and nothing happened'.
func (s *DeckShareInviteStore) ListForUser(ctx context.Context, userID uint64) ([]DeckShareInviteView, error) {
	// Empty rather than nil for the same reason as ListForDeck: the SPA reads the list's length directly.
	rows := []DeckShareInviteView{}
	err := s.db.WithContext(ctx).
		Table("deck_share_invites AS i").
		Select("d.public_id AS deck_id, d.name AS deck_name, COALESCE(w.public_id, '') AS user_id, '' AS username, i.role, COALESCE(u.public_id, '') AS invited_by, COALESCE(u.display_name, u.username, '') AS inviter_name, i.created_at, i.expires_at").
		// decks rows are **hard-deleted** (DeckStore.Delete explicitly cascades the related tables) and have no deleted_at column;
		// when a deck is deleted the invite rows go with that cascade, so no soft-delete filter is needed here.
		Joins("JOIN decks AS d ON d.id = i.deck_id").
		Joins("LEFT JOIN users AS w ON w.id = i.user_id").
		Joins("LEFT JOIN users AS u ON u.id = i.invited_by").
		Where("i.user_id = ? AND i.expires_at > ?", userID, time.Now().UTC()).
		Order("i.created_at DESC").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list deck share invites: %w", err)
	}
	return rows, nil
}

// Delete removes one invite (after accept or decline). Deleting a non-existent row is not a failure: the intent is 'this should no longer be pending'.
func (s *DeckShareInviteStore) Delete(ctx context.Context, deckID, userID uint64) error {
	err := s.db.WithContext(ctx).
		Where("deck_id = ? AND user_id = ?", deckID, userID).
		Delete(&DeckShareInvite{}).Error
	if err != nil {
		return fmt.Errorf("delete deck share invite: %w", err)
	}
	return nil
}

// DeleteExpired deletes expired invites (the Expirer of internal/retention).
func (s *DeckShareInviteStore) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	res := s.db.WithContext(ctx).
		Where("expires_at <= ?", before.UTC()).
		Delete(&DeckShareInvite{})
	if res.Error != nil {
		return 0, fmt.Errorf("delete expired deck share invites: %w", res.Error)
	}
	return res.RowsAffected, nil
}
