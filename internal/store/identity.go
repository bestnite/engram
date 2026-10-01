package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// ErrIdentityNotFound 表示按条件未找到绑定；上层据此与真正的数据库故障区分。
var ErrIdentityNotFound = errors.New("identity not found")

// IdentityStore 封装 identities 表的 GORM 访问（DESIGN.md §2.2、§4.5）。
// (provider, subject) 唯一约束由模型定义，重复绑定会在 Create 时报错。
type IdentityStore struct {
	db *gorm.DB
}

// NewIdentityStore 构造外部身份存储。
func NewIdentityStore(db *gorm.DB) *IdentityStore { return &IdentityStore{db: db} }

// Create 写入一条绑定；LinkedAt 为零值时补当前 UTC 时间。
func (s *IdentityStore) Create(ctx context.Context, ident *Identity) error {
	if ident.LinkedAt.IsZero() {
		ident.LinkedAt = time.Now().UTC()
	}
	if err := s.db.WithContext(ctx).Create(ident).Error; err != nil {
		return fmt.Errorf("create identity: %w", err)
	}
	return nil
}

// ByProviderSubject 按 (provider, subject) 取绑定；未找到返回 ErrIdentityNotFound。
func (s *IdentityStore) ByProviderSubject(ctx context.Context, provider, subject string) (*Identity, error) {
	var ident Identity
	if err := s.db.WithContext(ctx).
		First(&ident, "provider = ? AND subject = ?", provider, subject).Error; err != nil {
		if IsNotFound(err) {
			return nil, ErrIdentityNotFound
		}
		return nil, fmt.Errorf("load identity: %w", err)
	}
	return &ident, nil
}

// ByID 按主键取绑定；未找到返回 ErrIdentityNotFound。
func (s *IdentityStore) ByID(ctx context.Context, id uint64) (*Identity, error) {
	var ident Identity
	if err := s.db.WithContext(ctx).First(&ident, "id = ?", id).Error; err != nil {
		if IsNotFound(err) {
			return nil, ErrIdentityNotFound
		}
		return nil, fmt.Errorf("load identity: %w", err)
	}
	return &ident, nil
}

// ListForUser 返回某内部用户已绑定的全部外部身份（一个用户可有多个，§4.5）。
// 管理面板（M6）查看绑定列表时使用；按绑定时间升序，保证展示顺序稳定。
func (s *IdentityStore) ListForUser(ctx context.Context, userID uint64) ([]Identity, error) {
	var rows []Identity
	if err := s.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("linked_at ASC, id ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list identities: %w", err)
	}
	return rows, nil
}

// ListAll 返回全部外部身份（含所属用户），供管理面板的「已绑定身份列表」展示（M6-4）。
// 按用户、再按绑定时间排序，保证跨请求顺序稳定；调用方按 user_id 关联用户信息。
func (s *IdentityStore) ListAll(ctx context.Context) ([]Identity, error) {
	var rows []Identity
	if err := s.db.WithContext(ctx).Order("user_id ASC, linked_at ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list identities: %w", err)
	}
	return rows, nil
}

// Delete 解绑一条外部身份（按主键）。行不存在时返回 ErrIdentityNotFound。
//
// 解绑是 M6 管理面板的后端能力：本轮只提供存储与测试，不接 UI（AGENTS.md §5 M1-12）。
// 删除整行而非软删除，是因为 identities 表没有 revoked_at 列（DESIGN.md §2.2），
// 擅自加列属于 models.go 单写者的活。
func (s *IdentityStore) Delete(ctx context.Context, id uint64) error {
	res := s.db.WithContext(ctx).Delete(&Identity{}, "id = ?", id)
	if res.Error != nil {
		return fmt.Errorf("delete identity: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrIdentityNotFound
	}
	return nil
}
