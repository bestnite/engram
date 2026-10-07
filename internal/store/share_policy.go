package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SharePolicyStore 读写「谁可以把卡组分享给我」这一策略与它的白名单。
//
// 判定只有一个入口 **Allows**：策略与白名单分散判断必然漂移，而漂移的表现是「白名单配了
// 却拦不住人」这种很难被发现的越权。
type SharePolicyStore struct{ db *gorm.DB }

// NewSharePolicyStore 构造策略存储。
func NewSharePolicyStore(db *gorm.DB) *SharePolicyStore { return &SharePolicyStore{db: db} }

// Policy 返回某用户的策略；未设置或值不认识时按 ShareAcceptAnyone（与上线前的行为一致）。
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

// SetPolicy 写入策略；传空串等价于回到默认（anyone）。
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

// Allow 允许 from 把卡组分享给 to（写到 to 的白名单里）。
func (s *SharePolicyStore) Allow(ctx context.Context, toUserID, fromUserID uint64) error {
	row := ShareAllow{FromUserID: fromUserID, ToUserID: toUserID, CreatedAt: time.Now().UTC()}
	err := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
	if err != nil {
		return fmt.Errorf("save share allow: %w", err)
	}
	return nil
}

// RevokeAllow 从白名单里移除一行。
func (s *SharePolicyStore) RevokeAllow(ctx context.Context, toUserID, fromUserID uint64) error {
	err := s.db.WithContext(ctx).
		Where("to_user_id = ? AND from_user_id = ?", toUserID, fromUserID).
		Delete(&ShareAllow{}).Error
	if err != nil {
		return fmt.Errorf("delete share allow: %w", err)
	}
	return nil
}

// AllowList 返回 to 的白名单（from 的 user id 列表，按加入时间）。
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

// Allows 判定 from 是否可以把卡组分享给 to。**这是唯一的判定入口。**
//
// 自己分享给自己不在此判（分享者与接收者相同由调用方拦掉）。
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
