package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/pgtest"
)

// gateDatabases returns production-shaped SQLite (store.Open: single connection) and optionally PostgreSQL.
// The gate relies on single-connection serialization on SQLite, so the ordinary multi-connection test DB cannot be used here.
func gateDatabases(t *testing.T) map[string]*gorm.DB {
	t.Helper()
	sqliteDB, err := Open("sqlite", filepath.Join(t.TempDir(), "gate.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	out := map[string]*gorm.DB{"sqlite": sqliteDB}
	if pg, ok := pgtest.Open(t); ok {
		out["postgres"] = pg
	}
	for name, db := range out {
		if err := AutoMigrate(context.Background(), db); err != nil {
			t.Fatalf("AutoMigrate %s: %v", name, err)
		}
	}
	return out
}

func gateUser(name, role string) *User {
	return &User{Username: name, Email: name + "@example.com", DisplayName: name, Role: role,
		Status: StatusActive, Locale: "en", Timezone: "UTC", CreatedAt: time.Now().UTC()}
}

// TestAdminGateSerializesBootstrap is a concurrency counter-case: when several bootstrap requests arrive at once only one admin is created,
// the rest get ErrAdminExists.
func TestAdminGateSerializesBootstrap(t *testing.T) {
	for driver, db := range gateDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			const racers = 8
			errs := make([]error, racers)
			var wg sync.WaitGroup
			for i := 0; i < racers; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					errs[i] = WithAdminGate(ctx, db, func(tx *gorm.DB, admins int64) error {
						if admins > 0 {
							return ErrAdminExists
						}
						// leave time between the check and the write: without the gate other requests would read the same 0 here.
						time.Sleep(20 * time.Millisecond)
						return tx.Create(gateUser(fmt.Sprintf("boot%d", i), RoleAdmin)).Error
					})
				}(i)
			}
			wg.Wait()
			created, refused := 0, 0
			for _, err := range errs {
				switch {
				case err == nil:
					created++
				case errors.Is(err, ErrAdminExists):
					refused++
				default:
					t.Errorf("unexpected error: %v", err)
				}
			}
			if created != 1 || refused != racers-1 {
				t.Fatalf("created %d, refused %d; want exactly 1 and %d", created, refused, racers-1)
			}
		})
	}
}

// TestAdminGateKeepsTheLastAdmin is a concurrency counter-case: when two admins disable each other at once only one succeeds,
// and an active admin always remains in the system.
func TestAdminGateKeepsTheLastAdmin(t *testing.T) {
	for driver, db := range gateDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			a, b := gateUser("alpha", RoleAdmin), gateUser("beta", RoleAdmin)
			for _, u := range []*User{a, b} {
				if err := db.Create(u).Error; err != nil {
					t.Fatalf("create admin: %v", err)
				}
			}
			disable := func(targetID uint64) error {
				return WithAdminGate(ctx, db, func(tx *gorm.DB, admins int64) error {
					var fresh User
					if err := tx.First(&fresh, "id = ?", targetID).Error; err != nil {
						return err
					}
					if RemovesLastAdmin(&fresh, admins) {
						return ErrLastAdmin
					}
					time.Sleep(20 * time.Millisecond)
					return tx.Model(&User{}).Where("id = ?", targetID).Update("status", StatusDisabled).Error
				})
			}
			var wg sync.WaitGroup
			errs := make([]error, 2)
			for i, target := range []uint64{a.ID, b.ID} {
				wg.Add(1)
				go func(i int, target uint64) {
					defer wg.Done()
					errs[i] = disable(target)
				}(i, target)
			}
			wg.Wait()
			var active int64
			if err := db.Model(&User{}).Where("role = ? AND status = ?", RoleAdmin, StatusActive).Count(&active).Error; err != nil {
				t.Fatalf("count admins: %v", err)
			}
			if active != 1 {
				t.Fatalf("active admins after the race = %d (errors %v), want 1", active, errs)
			}
			lastAdmin := 0
			for _, err := range errs {
				if errors.Is(err, ErrLastAdmin) {
					lastAdmin++
				} else if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
			if lastAdmin != 1 {
				t.Errorf("ErrLastAdmin returned %d times, want 1", lastAdmin)
			}
		})
	}
}
