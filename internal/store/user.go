package store

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// UserStore 封装 users 表的 GORM 访问；具体类型优先，接口留给消费方按需定义（AGENTS.md §2.4）。
type UserStore struct {
	db *gorm.DB
}

// NewUserStore 构造用户存储。
func NewUserStore(db *gorm.DB) *UserStore { return &UserStore{db: db} }

// Create 写入一个新用户；唯一约束冲突由调用方翻译成稳定错误码。
func (s *UserStore) Create(ctx context.Context, u *User) error {
	if err := s.db.WithContext(ctx).Create(u).Error; err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	return nil
}

// ByID 按主键取用户；不存在时返回 gorm.ErrRecordNotFound。
func (s *UserStore) ByID(ctx context.Context, id uint64) (*User, error) {
	var u User
	if err := s.db.WithContext(ctx).First(&u, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

// ByUsername 按登录名取用户（登录路径使用）。
func (s *UserStore) ByUsername(ctx context.Context, username string) (*User, error) {
	var u User
	if err := s.db.WithContext(ctx).First(&u, "username = ?", username).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

// ByEmail 按邮箱取用户（OIDC 自动绑定与管理员查找使用）。
func (s *UserStore) ByEmail(ctx context.Context, email string) (*User, error) {
	var u User
	if err := s.db.WithContext(ctx).First(&u, "email = ?", email).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

// Update 保存用户全部字段；Save 在两库都生成正确的 upsert（DESIGN.md §2.3）。
func (s *UserStore) Update(ctx context.Context, u *User) error {
	if err := s.db.WithContext(ctx).Save(u).Error; err != nil {
		return fmt.Errorf("update user: %w", err)
	}
	return nil
}

// SetPasswordHash 只改密码哈希一列，避免整行覆盖带来的并发丢写。
func (s *UserStore) SetPasswordHash(ctx context.Context, id uint64, hash string) error {
	if err := s.db.WithContext(ctx).Model(&User{}).Where("id = ?", id).
		Update("password_hash", hash).Error; err != nil {
		return fmt.Errorf("set password hash: %w", err)
	}
	return nil
}

// SetStatus 只改账号状态一列（active | disabled）。
func (s *UserStore) SetStatus(ctx context.Context, id uint64, status string) error {
	if err := s.db.WithContext(ctx).Model(&User{}).Where("id = ?", id).
		Update("status", status).Error; err != nil {
		return fmt.Errorf("set user status: %w", err)
	}
	return nil
}

// CountActiveAdmins 统计仍可登录的管理员数；用于禁止把最后一个管理员禁用（M1-5 引导页依赖它）。
func (s *UserStore) CountActiveAdmins(ctx context.Context) (int64, error) {
	var n int64
	if err := s.db.WithContext(ctx).Model(&User{}).
		Where("role = ? AND status = ?", RoleAdmin, StatusActive).Count(&n).Error; err != nil {
		return 0, fmt.Errorf("count active admins: %w", err)
	}
	return n, nil
}
