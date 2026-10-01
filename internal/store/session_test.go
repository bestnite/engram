package store

import (
	"context"
	"testing"
	"time"
)

func TestSessionStoreLifecycle(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			fx := seedFixture(t, db)
			sessions := NewSessionStore(db)
			now := time.Now().UTC()

			keep := &Session{ID: "sess-keep", UserID: fx.userA, CSRFToken: "csrf-1",
				CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
			doomed := &Session{ID: "sess-doomed", UserID: fx.userA, CSRFToken: "csrf-2",
				CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
			other := &Session{ID: "sess-other", UserID: fx.userB, CSRFToken: "csrf-3",
				CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
			for _, s := range []*Session{keep, doomed, other} {
				if err := sessions.Create(ctx, s); err != nil {
					t.Fatalf("Create(%s) error = %v", s.ID, err)
				}
			}

			if _, err := sessions.ByID(ctx, "sess-keep"); err != nil {
				t.Fatalf("ByID() error = %v", err)
			}

			// RevokeAllForUser 只影响目标用户，另一个用户的会话保留。
			if err := sessions.RevokeAllForUser(ctx, fx.userA, now); err != nil {
				t.Fatalf("RevokeAllForUser() error = %v", err)
			}
			got, err := sessions.ByID(ctx, "sess-keep")
			if err != nil {
				t.Fatalf("ByID() error = %v", err)
			}
			if got.RevokedAt == nil {
				t.Error("sess-keep should be revoked after RevokeAllForUser")
			}
			untouched, err := sessions.ByID(ctx, "sess-other")
			if err != nil {
				t.Fatalf("ByID(other) error = %v", err)
			}
			if untouched.RevokedAt != nil {
				t.Error("sess-other must not be revoked by another user's RevokeAllForUser")
			}

			// 过期回收只删过期的行。
			if err := db.Model(&Session{}).Where("id = ?", "sess-doomed").
				Update("expires_at", now.Add(-time.Hour)).Error; err != nil {
				t.Fatalf("expire session: %v", err)
			}
			n, err := sessions.DeleteExpired(ctx, now)
			if err != nil {
				t.Fatalf("DeleteExpired() error = %v", err)
			}
			if n != 1 {
				t.Errorf("DeleteExpired() deleted %d rows, want 1", n)
			}
		})
	}
}
