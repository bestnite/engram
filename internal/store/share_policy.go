package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SharePolicyStore reads and writes the "who may share a deck with me" policy and its allowlist.
//
// There is exactly one entry point for the decision, **Allows**: judging the policy and the allowlist separately will drift, and the drift shows up as "the allowlist is configured
// yet people still get through", an escalation that is very hard to notice.
type SharePolicyStore struct{ db *gorm.DB }

// NewSharePolicyStore builds the policy store.
func NewSharePolicyStore(db *gorm.DB) *SharePolicyStore { return &SharePolicyStore{db: db} }

// Policy returns a user's policy; when unset or unrecognized it is ShareAcceptAnyone (matching the behaviour from before launch).
func (s *SharePolicyStore) Policy(ctx context.Context, userID uint64) (ShareAcceptPolicy, error) {
	var u User
	if err := s.db.WithContext(ctx).Select("share_accept_from").Where("id = ?", userID).Take(&u).Error; err != nil {
		return ShareAcceptAnyone, fmt.Errorf("load share policy: %w", err)
	}
	if u.ShareAcceptFrom == nil {
		return ShareAcceptAnyone, nil
	}
	switch ShareAcceptPolicy(strings.TrimSpace(*u.ShareAcceptFrom)) {
	case ShareAcceptWhitelist:
		return ShareAcceptWhitelist, nil
	case ShareAcceptNobody:
		return ShareAcceptNobody, nil
	default:
		return ShareAcceptAnyone, nil
	}
}

// SetPolicy writes the policy; an empty string equals going back to the default (anyone).
func (s *SharePolicyStore) SetPolicy(ctx context.Context, userID uint64, policy ShareAcceptPolicy) error {
	value := strings.TrimSpace(string(policy))
	switch ShareAcceptPolicy(value) {
	case ShareAcceptAnyone, ShareAcceptWhitelist, ShareAcceptNobody:
	case "":
		value = ""
	default:
		return fmt.Errorf("invalid share accept policy %q", policy)
	}
	err := s.db.WithContext(ctx).Model(&User{}).
		Where("id = ?", userID).
		Update("share_accept_from", value).Error
	if err != nil {
		return fmt.Errorf("save share policy: %w", err)
	}
	return nil
}

// Allow permits from to share decks with to (by writing into to's allowlist).
func (s *SharePolicyStore) Allow(ctx context.Context, toUserID, fromUserID uint64) error {
	row := ShareAllow{FromUserID: fromUserID, ToUserID: toUserID, CreatedAt: time.Now().UTC()}
	err := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
	if err != nil {
		return fmt.Errorf("save share allow: %w", err)
	}
	return nil
}

// RevokeAllow removes one row from the allowlist.
func (s *SharePolicyStore) RevokeAllow(ctx context.Context, toUserID, fromUserID uint64) error {
	err := s.db.WithContext(ctx).
		Where("to_user_id = ? AND from_user_id = ?", toUserID, fromUserID).
		Delete(&ShareAllow{}).Error
	if err != nil {
		return fmt.Errorf("delete share allow: %w", err)
	}
	return nil
}

// AllowList returns to's allowlist (the user ids of the froms, in the order they were added).
func (s *SharePolicyStore) AllowList(ctx context.Context, toUserID uint64) ([]uint64, error) {
	var rows []ShareAllow
	if err := s.db.WithContext(ctx).
		Where("to_user_id = ?", toUserID).Order("created_at ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list share allow: %w", err)
	}
	out := make([]uint64, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.FromUserID)
	}
	return out, nil
}

// Allows decides whether from may share a deck with to. **This is the only entry point for the decision.**
//
// Sharing with oneself is not judged here (the caller rejects an identical sharer and recipient).
func (s *SharePolicyStore) Allows(ctx context.Context, toUserID, fromUserID uint64) (bool, error) {
	policy, err := s.Policy(ctx, toUserID)
	if err != nil {
		return false, err
	}
	switch policy {
	case ShareAcceptNobody:
		return false, nil
	case ShareAcceptAnyone:
		return true, nil
	case ShareAcceptWhitelist:
		var count int64
		err := s.db.WithContext(ctx).Model(&ShareAllow{}).
			Where("to_user_id = ? AND from_user_id = ?", toUserID, fromUserID).
			Count(&count).Error
		if err != nil {
			return false, fmt.Errorf("check share allow: %w", err)
		}
		return count > 0, nil
	default:
		return true, nil
	}
}
