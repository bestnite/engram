package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// TestPublicIDGeneratedOnCreate 断言对外可见实体在创建时自动获得一个 UUIDv7，
// 且显式给出的 public_id 不会被钩子覆盖（钩子只填空值）。
func TestPublicIDGeneratedOnCreate(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			now := time.Now().UTC()
			u := User{Username: "pub", Email: "pub@example.com", DisplayName: "Pub", Role: "user",
				Status: "active", Locale: "en", Timezone: "UTC", CreatedAt: now}
			if err := db.Create(&u).Error; err != nil {
				t.Fatalf("create user: %v", err)
			}
			assertUUIDv7(t, "user", u.PublicID)

			explicit := "01890000-0000-7000-8000-000000000000"
			d := Deck{OwnerUserID: u.ID, Name: "D", Description: "",
				PresetID: 1, PublicID: explicit, CreatedAt: now}
			if err := db.Create(&d).Error; err != nil {
				t.Fatalf("create deck: %v", err)
			}
			if d.PublicID != explicit {
				t.Errorf("explicit public_id was overwritten: got %q, want %q", d.PublicID, explicit)
			}
		})
	}
}

// TestBackfillPublicIDsFillsLegacyRows 覆盖迁移 0003：迁移前写入、public_id 为空的行被补上
// v7；已经在用的 public_id 不被动，且回填后全表唯一。
func TestBackfillPublicIDsFillsLegacyRows(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			now := time.Now().UTC()
			// SkipHooks 造出「迁移前的旧行」：public_id 为空，绕开 BeforeCreate。
			legacy := User{Username: "legacy", Email: "legacy@example.com", DisplayName: "L",
				Role: "user", Status: "active", Locale: "en", Timezone: "UTC", CreatedAt: now}
			if err := db.Session(&gorm.Session{SkipHooks: true}).Create(&legacy).Error; err != nil {
				t.Fatalf("seed legacy user: %v", err)
			}
			fresh := User{Username: "fresh", Email: "fresh@example.com", DisplayName: "F",
				Role: "user", Status: "active", Locale: "en", Timezone: "UTC", CreatedAt: now}
			if err := db.Create(&fresh).Error; err != nil {
				t.Fatalf("create user: %v", err)
			}
			if err := backfillPublicIDs(db); err != nil {
				t.Fatalf("backfillPublicIDs() error = %v", err)
			}
			var rows []User
			if err := db.Table("users").Select("id", "public_id").Find(&rows).Error; err != nil {
				t.Fatalf("read users: %v", err)
			}
			seen := map[string]bool{}
			for _, row := range rows {
				if row.PublicID == "" {
					t.Fatalf("user id=%d still has an empty public_id after backfill", row.ID)
				}
				assertUUIDv7(t, "user", row.PublicID)
				if seen[row.PublicID] {
					t.Fatalf("duplicate public_id %q after backfill", row.PublicID)
				}
				seen[row.PublicID] = true
			}
			if !seen[fresh.PublicID] {
				t.Error("a pre-existing public_id was lost by the backfill")
			}
		})
	}
}

// TestPublicIDUniqueIndexRejectsDuplicate 断言唯一索引真的建了出来：两行同 public_id 必须被拒。
func TestPublicIDUniqueIndexRejectsDuplicate(t *testing.T) {
	db := newSQLite(t)
	if err := db.AutoMigrate(AllModels()...); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	now := time.Now().UTC()
	dup := "01890000-0000-7000-8000-000000000001"
	first := User{Username: "u1", Email: "u1@example.com", DisplayName: "U1", Role: "user",
		Status: "active", Locale: "en", Timezone: "UTC", PublicID: dup, CreatedAt: now}
	if err := db.Create(&first).Error; err != nil {
		t.Fatalf("create first user: %v", err)
	}
	second := User{Username: "u2", Email: "u2@example.com", DisplayName: "U2", Role: "user",
		Status: "active", Locale: "en", Timezone: "UTC", PublicID: dup, CreatedAt: now}
	if err := db.Create(&second).Error; err == nil {
		t.Error("duplicate public_id was accepted, want a unique-index violation")
	}
}

// assertUUIDv7 断言字符串是可解析的 UUID 且版本为 7。
func assertUUIDv7(t *testing.T, label, raw string) {
	t.Helper()
	parsed, err := uuid.Parse(raw)
	if err != nil {
		t.Fatalf("%s public_id %q is not a UUID: %v", label, raw, err)
	}
	if parsed.Version() != 7 {
		t.Errorf("%s public_id %q has version %d, want 7", label, raw, parsed.Version())
	}
}

// TestStoreByPublicID 断言按对外 id 能查到对应行；未知 id 与空串都返回未找到。
// 覆盖通用实现（Deck/User/APIKey）、自定义可见性实现（Card）与哨兵错误映射（Invite/Identity）。
func TestStoreByPublicID(t *testing.T) {
	db := newSQLite(t)
	if err := db.AutoMigrate(AllModels()...); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	u := User{Username: "pubid", Email: "pubid@example.com", DisplayName: "P", Role: "user",
		Status: "active", Locale: "en", Timezone: "UTC", CreatedAt: now}
	if err := db.Create(&u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	deck := Deck{OwnerUserID: u.ID, Name: "D", Description: "", PresetID: 1, CreatedAt: now}
	if err := db.Create(&deck).Error; err != nil {
		t.Fatalf("create deck: %v", err)
	}
	note := Note{DeckID: deck.ID, Kind: "basic", FieldsJSON: "{}", TagsJSON: "[]", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&note).Error; err != nil {
		t.Fatalf("create note: %v", err)
	}
	card := Card{NoteID: note.ID, Template: "forward", CreatedAt: now}
	if err := db.Create(&card).Error; err != nil {
		t.Fatalf("create card: %v", err)
	}
	invite := Invite{Token: "tok-pubid", Role: "user", CreatedAt: now}
	if err := db.Create(&invite).Error; err != nil {
		t.Fatalf("create invite: %v", err)
	}
	ident := Identity{UserID: u.ID, Provider: "p", Subject: "s", LinkedAt: now}
	if err := db.Create(&ident).Error; err != nil {
		t.Fatalf("create identity: %v", err)
	}
	key := APIKey{UserID: u.ID, Name: "k", Prefix: "pk", KeyHash: "hash-pubid", Scopes: "read", CreatedAt: now}
	if err := db.Create(&key).Error; err != nil {
		t.Fatalf("create api key: %v", err)
	}

	if got, err := NewDeckStore(db).ByPublicID(ctx, deck.PublicID); err != nil || got.ID != deck.ID {
		t.Errorf("DeckStore.ByPublicID = (%v, %v), want deck id %d", got, err, deck.ID)
	}
	if got, err := NewUserStore(db).ByPublicID(ctx, u.PublicID); err != nil || got.ID != u.ID {
		t.Errorf("UserStore.ByPublicID = (%v, %v), want user id %d", got, err, u.ID)
	}
	if got, err := NewAPIKeyStore(db).ByPublicID(ctx, key.PublicID); err != nil || got.ID != key.ID {
		t.Errorf("APIKeyStore.ByPublicID = (%v, %v), want key id %d", got, err, key.ID)
	}
	if got, err := NewCardStore(db).ByPublicID(ctx, card.PublicID); err != nil || got.ID != card.ID {
		t.Errorf("CardStore.ByPublicID = (%v, %v), want card id %d", got, err, card.ID)
	}
	if got, err := NewInviteStore(db).ByPublicID(ctx, invite.PublicID); err != nil || got.ID != invite.ID {
		t.Errorf("InviteStore.ByPublicID = (%v, %v), want invite id %d", got, err, invite.ID)
	}
	if got, err := NewIdentityStore(db).ByPublicID(ctx, ident.PublicID); err != nil || got.ID != ident.ID {
		t.Errorf("IdentityStore.ByPublicID = (%v, %v), want identity id %d", got, err, ident.ID)
	}

	if _, err := NewDeckStore(db).ByPublicID(ctx, "no-such-deck"); err == nil {
		t.Error("DeckStore.ByPublicID(unknown) returned a row, want not found")
	}
	if _, err := NewDeckStore(db).ByPublicID(ctx, ""); err == nil {
		t.Error("DeckStore.ByPublicID(\"\") returned a row, want not found")
	}
	if _, err := NewInviteStore(db).ByPublicID(ctx, "no-such-invite"); err != ErrInviteNotFound {
		t.Errorf("InviteStore.ByPublicID(unknown) error = %v, want ErrInviteNotFound", err)
	}
	if _, err := NewIdentityStore(db).ByPublicID(ctx, "no-such-identity"); err != ErrIdentityNotFound {
		t.Errorf("IdentityStore.ByPublicID(unknown) error = %v, want ErrIdentityNotFound", err)
	}
}
