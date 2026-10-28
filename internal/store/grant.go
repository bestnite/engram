package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// Deck role constants. The role set is deliberately only three:
// owner has full rights; editor can change card content but not deck settings / grants; reader is read-only and can only review for themselves.
const (
	RoleOwner  = "owner"
	RoleEditor = "editor"
	RoleReader = "reader"
)

// roleRank gives a role's permission strength, used for requireRole's 'at least' comparison.
// A larger number means stronger rights; an unknown role name (including the empty string) counts as no rights.
// The order owner > editor > reader mirrors the permission table: whoever can change content can necessarily see it, whoever can change settings can necessarily change content.
func roleRank(role string) int {
	switch role {
	case RoleOwner:
		return 3
	case RoleEditor:
		return 2
	case RoleReader:
		return 1
	default:
		return 0
	}
}

// ValidRole reports whether role is one of the three legal roles; narrow with it before writing a grant.
func ValidRole(role string) bool { return roleRank(role) > 0 }

// RoleAllows reports whether the have role satisfies the want role ('at least' semantics).
// An empty or invalid want is always refused — when a caller misspells a role name it is better to refuse than to let it through because the comparison 'looks equal'.
func RoleAllows(have, want string) bool {
	w := roleRank(want)
	return w > 0 && roleRank(have) >= w
}

// GrantStore wraps the deck_grants table: grant, change role, revoke, query.
//
// Revocation deletes the whole row instead of writing a revoked flag: revocation must take effect immediately, and deleting means the next
// requireRole finds no row at all, which is inherently immediate and needs no extra filter on the read path.
type GrantStore struct {
	db *gorm.DB
}

// NewGrantStore constructs the grant store.
func NewGrantStore(db *gorm.DB) *GrantStore { return &GrantStore{db: db} }

// Role returns the user's explicit grant role on a deck; with no grant row it returns an empty string and nil ('no grant' is not an error).
func (s *GrantStore) Role(ctx context.Context, deckID, userID uint64) (string, error) {
	var g DeckGrant
	err := s.db.WithContext(ctx).
		Where("deck_id = ? AND user_id = ?", deckID, userID).First(&g).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read deck grant: %w", err)
	}
	return g.Role, nil
}

// Grant writes or changes the role (UPSERT): it creates the row when absent and updates only role when present.
// role must be one of the three legal values, otherwise it returns an error instead of persisting a dirty role (a dirty role would break requireRole's rank comparison).
// createdBy records only the first granter; changing the role keeps the original created_by / created_at.
func (s *GrantStore) Grant(ctx context.Context, deckID, userID uint64, role string, createdBy *uint64) error {
	if !ValidRole(role) {
		return fmt.Errorf("invalid deck role %q", role)
	}
	var existing DeckGrant
	err := s.db.WithContext(ctx).
		Where("deck_id = ? AND user_id = ?", deckID, userID).First(&existing).Error
	switch {
	case err == nil:
		if err := s.db.WithContext(ctx).Model(&DeckGrant{}).
			Where("deck_id = ? AND user_id = ?", deckID, userID).
			Update("role", role).Error; err != nil {
			return fmt.Errorf("update deck grant: %w", err)
		}
		return nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		row := DeckGrant{DeckID: deckID, UserID: userID, Role: role, CreatedBy: createdBy, CreatedAt: time.Now().UTC()}
		if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
			return fmt.Errorf("create deck grant: %w", err)
		}
		// a new member starts from their own default preset and default caps, not the owner's settings.
		return ensureMemberSettings(ctx, s.db, deckID, userID)
	default:
		return fmt.Errorf("read deck grant: %w", err)
	}
}

// Revoke deletes the grant row; the very next request is refused ('revocation takes effect immediately').
// The member's study settings on that deck, the extra new-card quota for the day and the member's pause are deleted together with the grant.
func (s *GrantStore) Revoke(ctx context.Context, deckID, userID uint64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("deck_id = ? AND user_id = ?", deckID, userID).
			Delete(&DeckGrant{}).Error; err != nil {
			return fmt.Errorf("revoke deck grant: %w", err)
		}
		if err := tx.Where("deck_id = ? AND user_id = ?", deckID, userID).
			Delete(&DeckMemberSetting{}).Error; err != nil {
			return fmt.Errorf("delete member settings: %w", err)
		}
		if err := tx.Where("deck_id = ? AND user_id = ?", deckID, userID).
			Delete(&DeckDailyExtra{}).Error; err != nil {
			return fmt.Errorf("delete daily extras: %w", err)
		}
		if err := tx.Where("deck_id = ? AND user_id = ?", deckID, userID).
			Delete(&DeckPause{}).Error; err != nil {
			return fmt.Errorf("delete deck pause: %w", err)
		}
		return nil
	})
}

// ListByDeck lists all grants of a deck; the sharing management page uses it to display the current grant list.
func (s *GrantStore) ListByDeck(ctx context.Context, deckID uint64) ([]DeckGrant, error) {
	var rows []DeckGrant
	if err := s.db.WithContext(ctx).
		Where("deck_id = ?", deckID).Order("user_id ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list deck grants: %w", err)
	}
	return rows, nil
}

// RolesForUser returns the deck_id -> role map of all of a user's explicit grants.
//
// The list endpoint uses it to tell 'decks shared with me' from 'my own decks': own decks are decided by decks.owner_user_id,
// and only explicit grant rows count as shared. One query fetches everything, avoiding one query per deck in the list.
// Every grant row is the result of an explicit act (an invite was accepted, or a link holder redeemed it), so each entry in the map
// corresponds to a real act of acceptance.
func (s *GrantStore) RolesForUser(ctx context.Context, userID uint64) (map[uint64]string, error) {
	var rows []DeckGrant
	if err := s.db.WithContext(ctx).Where("user_id = ?", userID).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list granted deck roles: %w", err)
	}
	out := make(map[uint64]string, len(rows))
	for _, r := range rows {
		out[r.DeckID] = r.Role
	}
	return out, nil
}
