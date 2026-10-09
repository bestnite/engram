package store

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// 「活跃管理员集合」的变更闸门。
//
// 三类操作都是「先数活跃管理员、再按结果写」：首个管理员引导（没有管理员才允许建）、
// 无管理员时注册即成为管理员、以及禁用 / 删除 / 降权管理员（不能清空管理员）。
// 数与写分在两条语句里时，两个并发请求可以都看到同一个计数再各自写入：两个引导请求会建出
// 两个管理员；两个管理员同时禁用对方会把管理员清空，引导页随之对所有人重新开放。
// 闸门把「数」与「写」放进同一个事务，并让这些事务彼此串行。

// ErrAdminExists 表示引导时已经存在活跃管理员（另一个请求先完成了引导）。
var ErrAdminExists = errors.New("store: an active administrator already exists")

// ErrLastAdmin 表示该操作会让系统没有任何活跃管理员。
var ErrLastAdmin = errors.New("store: the operation would leave no active administrator")

// adminGateLockKey 是 PostgreSQL 事务级 advisory lock 的键；任意固定值，只在本闸门使用。
const adminGateLockKey = 7_301_942_118

// WithAdminGate 在一个与其他闸门事务互斥的事务里执行 fn，并把事务内数出的活跃管理员数传给它。
//
// 互斥的实现按驱动区分：PostgreSQL 用事务级 advisory lock（提交或回滚时自动释放）；
// SQLite 由单写连接保证（store.Open 把连接数设为 1，整个事务独占连接）。
func WithAdminGate(ctx context.Context, db *gorm.DB, fn func(tx *gorm.DB, activeAdmins int64) error) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if tx.Dialector.Name() == "postgres" {
			if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", adminGateLockKey).Error; err != nil {
				return fmt.Errorf("store: acquire admin gate: %w", err)
			}
		}
		var n int64
		if err := tx.Model(&User{}).Where("role = ? AND status = ?", RoleAdmin, StatusActive).Count(&n).Error; err != nil {
			return fmt.Errorf("store: count active admins: %w", err)
		}
		return fn(tx, n)
	})
}

// RemovesLastAdmin 判断在活跃管理员数为 activeAdmins 时，让 target 失去管理员身份
// （禁用、删除或降权）是否会清空管理员。target 必须是在闸门事务里重新读出的行。
func RemovesLastAdmin(target *User, activeAdmins int64) bool {
	return target.Role == RoleAdmin && target.Status == StatusActive && activeAdmins <= 1
}
