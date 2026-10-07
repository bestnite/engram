package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrShareInviteNotFound 表示该 (卡组, 用户) 没有待接受的邀请。它是**正常状态**（没有邀请），
// 用哨兵是为了让调用方把「没有」与「查库失败」分开。
var ErrShareInviteNotFound = errors.New("deck share invite not found")

// DeckShareInviteStore 读写待接受的共享邀请（DESIGN.md §4.4）。
//
// 授权（deck_grants）只在接受时才写，所以这张表**不参与任何可见性判定**：待接受的邀请
// 不该让别人看到卡组内容。
type DeckShareInviteStore struct{ db *gorm.DB }

// NewDeckShareInviteStore 构造邀请存储。
func NewDeckShareInviteStore(db *gorm.DB) *DeckShareInviteStore {
	return &DeckShareInviteStore{db: db}
}

// Invite 写入或刷新一条邀请。同一 (卡组, 用户) 只有一行，重复邀请刷新角色与有效期，
// 不叠加——否则「邀请两次」会在列表里出现两条一样的待接受项。
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

// ByDeckAndUser 取一条邀请；没有则 ErrShareInviteNotFound。
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

// DeckShareInviteView 是一条带展示信息的待接受邀请。
//
// 收件人视角用 DeckName + InviterName（卡组名、谁邀请我）；属主视角用 Username
// （我邀请了谁）。两个视角共用这一个结构，因为它们是同一行的两种读法。
type DeckShareInviteView struct {
	DeckID   uint64 `json:"deck_id"`
	DeckName string `json:"deck_name"`
	UserID   uint64 `json:"user_id"`
	// Username 是被邀请者的用户名（属主视角）。
	Username string `json:"username"`
	Role     string `json:"role"`
	// InvitedBy 与 InviterName 是邀请人（收件人视角）。
	InvitedBy   uint64    `json:"invited_by"`
	InviterName string    `json:"inviter_name"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// ListForDeck 列出某卡组待接受的邀请（属主视角：我邀请了谁还没答应），按邀请时间倒序。
func (s *DeckShareInviteStore) ListForDeck(ctx context.Context, deckID uint64) ([]DeckShareInviteView, error) {
	var rows []DeckShareInviteView
	err := s.db.WithContext(ctx).
		Table("deck_share_invites AS i").
		Select("i.deck_id, '' AS deck_name, i.user_id, COALESCE(u.username, '') AS username, i.role, i.invited_by, COALESCE(v.display_name, v.username, '') AS inviter_name, i.created_at, i.expires_at").
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

// ListForUser 列出某人待接受的邀请，按邀请时间倒序。
//
// 未过期的才算：过期行可能还没被回收 worker 清掉，读路径必须自己判——否则界面会出现
// 「点了接受却什么也没发生」的幽灵条目。
func (s *DeckShareInviteStore) ListForUser(ctx context.Context, userID uint64) ([]DeckShareInviteView, error) {
	var rows []DeckShareInviteView
	err := s.db.WithContext(ctx).
		Table("deck_share_invites AS i").
		Select("i.deck_id, d.name AS deck_name, i.user_id, '' AS username, i.role, i.invited_by, COALESCE(u.display_name, u.username, '') AS inviter_name, i.created_at, i.expires_at").
		// decks 是**硬删除**（DeckStore.Delete 显式级联清关联表），没有 deleted_at 列；
		// 卡组被删时邀请行由那个级联一起清掉，所以这里不需要软删过滤。
		Joins("JOIN decks AS d ON d.id = i.deck_id").
		Joins("LEFT JOIN users AS u ON u.id = i.invited_by").
		Where("i.user_id = ? AND i.expires_at > ?", userID, time.Now().UTC()).
		Order("i.created_at DESC").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list deck share invites: %w", err)
	}
	return rows, nil
}

// Delete 删掉一条邀请（接受或拒绝之后）。删不存在的行不算失败：意图是「这条不该再挂着」。
func (s *DeckShareInviteStore) Delete(ctx context.Context, deckID, userID uint64) error {
	err := s.db.WithContext(ctx).
		Where("deck_id = ? AND user_id = ?", deckID, userID).
		Delete(&DeckShareInvite{}).Error
	if err != nil {
		return fmt.Errorf("delete deck share invite: %w", err)
	}
	return nil
}

// DeleteForDeck 清掉某个卡组的全部邀请（卡组被删除时级联，别留孤儿行）。
func (s *DeckShareInviteStore) DeleteForDeck(ctx context.Context, deckID uint64) error {
	err := s.db.WithContext(ctx).Where("deck_id = ?", deckID).Delete(&DeckShareInvite{}).Error
	if err != nil {
		return fmt.Errorf("delete deck share invites for deck: %w", err)
	}
	return nil
}

// DeleteExpired 删除已过期的邀请（internal/retention 的 Expirer）。
func (s *DeckShareInviteStore) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	res := s.db.WithContext(ctx).
		Where("expires_at <= ?", before.UTC()).
		Delete(&DeckShareInvite{})
	if res.Error != nil {
		return 0, fmt.Errorf("delete expired deck share invites: %w", res.Error)
	}
	return res.RowsAffected, nil
}
