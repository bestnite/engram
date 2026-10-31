package store

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"
)

// TestSetMediaQuota checks the three stored meanings - NULL follows the instance, 0 is unlimited, a positive value is
// the limit - and that a negative value or an unknown user is refused without writing.
func TestSetMediaQuota(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := AutoMigrate(context.Background(), db); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := NewUserStore(db)
			id := seedUsers(t, db, "quota_store")[0]
			read := func() *int64 {
				t.Helper()
				u, err := users.ByID(ctx, id)
				if err != nil {
					t.Fatalf("load user: %v", err)
				}
				return u.MediaQuotaBytes
			}
			if got := read(); got != nil {
				t.Fatalf("new user quota = %d, want NULL", *got)
			}
			for _, want := range []*int64{Ptr(int64(4096)), Ptr(int64(0)), nil} {
				if err := users.SetMediaQuota(ctx, id, want); err != nil {
					t.Fatalf("SetMediaQuota(%v): %v", want, err)
				}
				got := read()
				if (got == nil) != (want == nil) || (got != nil && *got != *want) {
					t.Errorf("stored quota = %v, want %v", got, want)
				}
			}
			if err := users.SetMediaQuota(ctx, id, Ptr(int64(-1))); err == nil {
				t.Errorf("negative quota accepted")
			}
			if got := read(); got != nil {
				t.Errorf("a refused write changed the quota to %d", *got)
			}
			if err := users.SetMediaQuota(ctx, id+1000, Ptr(int64(1))); !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Errorf("unknown user error = %v, want ErrRecordNotFound", err)
			}
		})
	}
}
