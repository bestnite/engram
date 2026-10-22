package store

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// The change gate for the 'active admin set'.
//
// All three operations follow 'count active admins, then write by the result': first-admin bootstrap
// (allowed only while none exists), a signup becoming admin when there is none, and disabling /
// deleting / demoting an admin (the set must not be emptied). With the count and the write in two
// statements, two concurrent requests can both see the same count and each write: two bootstrap
// requests create two admins; two admins disabling each other empty the set and reopen the bootstrap page to everyone. The gate puts count and write in one transaction and makes these transactions serialize against each other.

// ErrAdminExists means an active admin already exists at bootstrap time (another request bootstrapped first).
var ErrAdminExists = errors.New("store: an active administrator already exists")

// ErrLastAdmin means the operation would leave the system with no active admin.
var ErrLastAdmin = errors.New("store: the operation would leave no active administrator")

// adminGateLockKey is the key of the PostgreSQL transaction-level advisory lock; any fixed value, used only by this gate.
const adminGateLockKey = 7_301_942_118

// WithAdminGate runs fn in a transaction that is mutually exclusive with other gate transactions and passes it the active admin count read inside the transaction.
//
// Mutual exclusion is per driver: PostgreSQL uses a transaction-level advisory lock (released
// automatically on commit or rollback); SQLite relies on the single write connection (store.Open sets the pool size to 1, so the whole transaction owns the connection).
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

// RemovesLastAdmin reports whether taking the admin role away from target (disable, delete or demote)
// empties the admin set given activeAdmins active admins. target must be a row re-read inside the gate transaction.
func RemovesLastAdmin(target *User, activeAdmins int64) bool {
	return target.Role == RoleAdmin && target.Status == StatusActive && activeAdmins <= 1
}
