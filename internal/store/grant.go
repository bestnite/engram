package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// 卡组角色常量。角色集合故意只有三个：
// owner 全权；editor 能改卡片内容但不能改卡组设置/授权；reader 只读、只能自己复习。
const (
	RoleOwner  = "owner"
	RoleEditor = "editor"
	RoleReader = "reader"
)

// roleRank 给出角色的权限强度，用于 requireRole 的“至少”比较。
// 数值越大权限越强；未知名角色（含空串）视为无权。
// owner > editor > reader 的次序对应权限表：能改内容者必能看，能改设置者必能改内容。
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

// ValidRole 报告 role 是否是三个合法角色之一；授权写入前必须用它收窄。
func ValidRole(role string) bool { return roleRank(role) > 0 }

// RoleAllows 报告 have 角色是否满足 want 角色（“至少”语义）。
// want 为空或非法时一律拒绝——调用方把角色名拼错时宁可拒绝，也不能因为比较“看起来相等”而放行。
func RoleAllows(have, want string) bool {
	w := roleRank(want)
	return w > 0 && roleRank(have) >= w
}

// GrantStore 封装 deck_grants 表：授予、改角色、撤销、查询。
//
// 撤销采用“删除整行”而不是写 revoked 标记：撤销要求立即生效，删除让下一次
// requireRole 查不到任何行，天然即时，且无需在读取路径上再加一层过滤。
type GrantStore struct {
	db *gorm.DB
}

// NewGrantStore 构造授权存储。
func NewGrantStore(db *gorm.DB) *GrantStore { return &GrantStore{db: db} }

// Role 返回用户在卡组上的显式授权角色；没有授权行时返回空串与 nil（“无授权”不是错误）。
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

// Grant 写入或修改角色（UPSERT）：不存在则新建，存在则只更新 role。
// role 必须在三个合法值内，否则返回错误而不是把脏角色落库（脏角色会让 requireRole 的等级比较失效）。
// createdBy 只记录首次授权者；改角色时保留原 created_by / created_at。
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
		return nil
	default:
		return fmt.Errorf("read deck grant: %w", err)
	}
}

// Revoke 删除授权行；删除后下一个请求即被拒（“撤销立即生效”）。
func (s *GrantStore) Revoke(ctx context.Context, deckID, userID uint64) error {
	if err := s.db.WithContext(ctx).
		Where("deck_id = ? AND user_id = ?", deckID, userID).
		Delete(&DeckGrant{}).Error; err != nil {
		return fmt.Errorf("revoke deck grant: %w", err)
	}
	return nil
}

// ListByDeck 列出卡组的全部授权；共享管理页用它展示当前授权列表。
func (s *GrantStore) ListByDeck(ctx context.Context, deckID uint64) ([]DeckGrant, error) {
	var rows []DeckGrant
	if err := s.db.WithContext(ctx).
		Where("deck_id = ?", deckID).Order("user_id ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list deck grants: %w", err)
	}
	return rows, nil
}

// DeckIDsForUser 返回某用户被显式授权的全部卡组 id；列表按权限过滤时与 owner 卡组合并。
func (s *GrantStore) DeckIDsForUser(ctx context.Context, userID uint64) ([]uint64, error) {
	var ids []uint64
	if err := s.db.WithContext(ctx).Model(&DeckGrant{}).
		Where("user_id = ?", userID).Pluck("deck_id", &ids).Error; err != nil {
		return nil, fmt.Errorf("list granted deck ids: %w", err)
	}
	return ids, nil
}
