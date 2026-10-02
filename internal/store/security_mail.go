package store

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// 本文件是 M1-19（A 类事务安全邮件）所需的用户列写入扩展：邮箱验证时间与邮箱更新。
//
// 放在独立文件而不是塞进 user.go / models.go：这两处是其它里程碑的单写者热点
// （AGENTS.md §6.4），并行工作时尽量不动它们。这里只新增方法，不改动既有行为。
// 也刻意不新增 users 列——email_verified_at 已存在于 User 模型，无需迁移。

// SetEmailVerifiedAt 只写 email_verified_at 一列，避免整行覆盖带来的并发丢写。
// at 传 nil 表示清除验证标记（改邮箱后新地址尚未验证）。
func (s *UserStore) SetEmailVerifiedAt(ctx context.Context, id uint64, at *time.Time) error {
	var value any
	if at != nil {
		u := at.UTC()
		value = u
	}
	if err := s.db.WithContext(ctx).Model(&User{}).Where("id = ?", id).
		Update("email_verified_at", value).Error; err != nil {
		return fmt.Errorf("set email verified at: %w", err)
	}
	return nil
}

// SetEmail 只改邮箱一列。邮箱唯一约束冲突会原样返回，由调用方翻成稳定错误码。
// 改邮箱意味着新地址已通过确认链接验证，因此同一事务里把 email_verified_at 设为 at。
func (s *UserStore) SetEmail(ctx context.Context, id uint64, email string, at time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&User{}).Where("id = ?", id).
			Update("email", email).Error; err != nil {
			return fmt.Errorf("set email: %w", err)
		}
		if err := tx.Model(&User{}).Where("id = ?", id).
			Update("email_verified_at", at.UTC()).Error; err != nil {
			return fmt.Errorf("set email verified at: %w", err)
		}
		return nil
	})
}
