package store

import (
	"context"
	"testing"
	"time"
)

func TestUserStoreCRUD(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := NewUserStore(db)
			hash := "$argon2id$v=19$m=1024,t=1,p=1$c2FsdA$aGFzaA"
			u := &User{Username: "alice", Email: "alice@example.com", DisplayName: "Alice",
				PasswordHash: &hash, Role: RoleUser, Status: StatusActive,
				Locale: "zh-CN", Timezone: "Asia/Shanghai", CreatedAt: time.Now().UTC()}
			if err := users.Create(ctx, u); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			if u.ID == 0 {
				t.Fatal("Create() did not populate ID")
			}
			got, err := users.ByUsername(ctx, "alice")
			if err != nil {
				t.Fatalf("ByUsername() error = %v", err)
			}
			if got.Email != "alice@example.com" {
				t.Errorf("ByUsername().Email = %q, want alice@example.com", got.Email)
			}
			if _, err := users.ByID(ctx, u.ID); err != nil {
				t.Errorf("ByID() error = %v", err)
			}
			if _, err := users.ByEmail(ctx, "alice@example.com"); err != nil {
				t.Errorf("ByEmail() error = %v", err)
			}
			if err := users.SetPasswordHash(ctx, u.ID, "$argon2id$v=19$m=2048,t=2,p=1$c2FsdA$aGFzaA"); err != nil {
				t.Fatalf("SetPasswordHash() error = %v", err)
			}
			if err := users.SetStatus(ctx, u.ID, StatusDisabled); err != nil {
				t.Fatalf("SetStatus() error = %v", err)
			}
			got, err = users.ByID(ctx, u.ID)
			if err != nil {
				t.Fatalf("ByID() after update error = %v", err)
			}
			if got.Status != StatusDisabled {
				t.Errorf("Status after SetStatus = %q, want disabled", got.Status)
			}
			if got.PasswordHash == nil || *got.PasswordHash == hash {
				t.Error("password hash was not updated")
			}
			if _, err := users.ByUsername(ctx, "missing"); !IsNotFound(err) {
				t.Errorf("ByUsername(missing) error = %v, want ErrRecordNotFound", err)
			}
		})
	}
}
