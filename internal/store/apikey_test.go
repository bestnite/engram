package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestAPIKeyCreateStoresHashOnly 是 M4-1 的验收用例之一：明文绝不落库。
// 库里只允许出现 sha256 摘要与展示前缀。
func TestAPIKeyCreateStoresHashOnly(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			user := seedUsers(t, db, "apikey_owner")[0]
			keys := NewAPIKeyStore(db)
			expires := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)

			created, err := keys.Create(ctx, CreateAPIKeyParams{
				UserID: user, Name: "agent", Scopes: []string{ScopeWrite, ScopeRead}, ExpiresAt: &expires,
			})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			if !strings.HasPrefix(created.Plaintext, APIKeyPlaintextPrefix) {
				t.Fatalf("plaintext %q does not start with %q", created.Plaintext, APIKeyPlaintextPrefix)
			}
			if created.Key.KeyHash != HashAPIKey(created.Plaintext) {
				t.Errorf("KeyHash = %q, want sha256 of the plaintext", created.Key.KeyHash)
			}
			if created.Key.KeyHash == created.Plaintext {
				t.Error("KeyHash equals the plaintext")
			}

			stored, err := keys.ByID(ctx, created.Key.ID)
			if err != nil {
				t.Fatalf("ByID() error = %v", err)
			}
			if stored.KeyHash != HashAPIKey(created.Plaintext) {
				t.Errorf("stored KeyHash = %q, want the sha256 digest", stored.KeyHash)
			}
			if stored.KeyHash == created.Plaintext {
				t.Error("the plaintext was persisted in key_hash")
			}
			if stored.Prefix == created.Plaintext {
				t.Error("the full plaintext was persisted in prefix")
			}
			if !strings.HasPrefix(created.Plaintext, stored.Prefix) {
				t.Errorf("prefix %q is not a prefix of the plaintext", stored.Prefix)
			}
			// 整行序列化后也不得出现明文（覆盖 prefix/name/scopes 等所有列）。
			raw, err := json.Marshal(stored)
			if err != nil {
				t.Fatalf("marshal stored key: %v", err)
			}
			if strings.Contains(string(raw), created.Plaintext) {
				t.Errorf("plaintext persisted in a column: %s", raw)
			}
			// scopes 归一化为规范顺序并去重。
			if stored.Scopes != "read,write" {
				t.Errorf("stored scopes = %q, want %q", stored.Scopes, "read,write")
			}
		})
	}
}

// TestAPIKeyAuthenticateAndRevokeIsImmediate 是 M4-1 的另一个验收用例：
// 正常 key 能通过验证并刷新 last_used_at；撤销后同一明文立即被拒。
func TestAPIKeyAuthenticateAndRevokeIsImmediate(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			user := seedUsers(t, db, "apikey_auth_owner")[0]
			keys := NewAPIKeyStore(db)
			now := time.Now().UTC()

			created, err := keys.Create(ctx, CreateAPIKeyParams{UserID: user, Name: "ci"})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}

			got, err := keys.Authenticate(ctx, created.Plaintext, now)
			if err != nil {
				t.Fatalf("Authenticate() error = %v", err)
			}
			if got.ID != created.Key.ID {
				t.Errorf("Authenticate() id = %d, want %d", got.ID, created.Key.ID)
			}
			if got.LastUsedAt == nil {
				t.Error("Authenticate() did not set LastUsedAt")
			}
			reloaded, err := keys.ByID(ctx, created.Key.ID)
			if err != nil {
				t.Fatalf("ByID() error = %v", err)
			}
			if reloaded.LastUsedAt == nil {
				t.Error("last_used_at was not persisted")
			}

			// 未注册的明文按不存在处理。
			if _, err := keys.Authenticate(ctx, APIKeyPlaintextPrefix+"unknown", now); !errors.Is(err, ErrAPIKeyNotFound) {
				t.Errorf("Authenticate(unknown) error = %v, want ErrAPIKeyNotFound", err)
			}

			// 撤销后同一明文立即无法通过验证。
			if err := keys.Revoke(ctx, user, created.Key.ID, now); err != nil {
				t.Fatalf("Revoke() error = %v", err)
			}
			if _, err := keys.Authenticate(ctx, created.Plaintext, now); !errors.Is(err, ErrAPIKeyRevoked) {
				t.Errorf("Authenticate(revoked) error = %v, want ErrAPIKeyRevoked", err)
			}
			// 重复撤销幂等。
			if err := keys.Revoke(ctx, user, created.Key.ID, now); err != nil {
				t.Errorf("second Revoke() error = %v, want idempotent nil", err)
			}
		})
	}
}

// TestAPIKeyExpiry 断言过期 key 立即被拒。
func TestAPIKeyExpiry(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			user := seedUsers(t, db, "apikey_expiry_owner")[0]
			keys := NewAPIKeyStore(db)
			now := time.Now().UTC()
			past := now.Add(-time.Minute)

			created, err := keys.Create(ctx, CreateAPIKeyParams{UserID: user, Name: "expired", ExpiresAt: &past})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			if _, err := keys.Authenticate(ctx, created.Plaintext, now); !errors.Is(err, ErrAPIKeyExpired) {
				t.Errorf("Authenticate(expired) error = %v, want ErrAPIKeyExpired", err)
			}
		})
	}
}

// TestAPIKeyCreateValidation 覆盖创建时的三类拒绝路径。
func TestAPIKeyCreateValidation(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			user := seedUsers(t, db, "apikey_validate_owner")[0]
			keys := NewAPIKeyStore(db)

			if _, err := keys.Create(ctx, CreateAPIKeyParams{Name: "no-user"}); !errors.Is(err, ErrAPIKeyUserRequired) {
				t.Errorf("Create(no user) error = %v, want ErrAPIKeyUserRequired", err)
			}
			if _, err := keys.Create(ctx, CreateAPIKeyParams{UserID: user, Name: "  "}); !errors.Is(err, ErrAPIKeyNameRequired) {
				t.Errorf("Create(blank name) error = %v, want ErrAPIKeyNameRequired", err)
			}
			if _, err := keys.Create(ctx, CreateAPIKeyParams{UserID: user, Name: "bad", Scopes: []string{"root"}}); !errors.Is(err, ErrAPIKeyInvalidScope) {
				t.Errorf("Create(bad scope) error = %v, want ErrAPIKeyInvalidScope", err)
			}
		})
	}
}

// TestAPIKeyRevokeRequiresOwner 断言非 owner 无法撤销他人 key，且不泄露其存在性。
func TestAPIKeyRevokeRequiresOwner(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "apikey_o1", "apikey_o2")
			owner, stranger := users[0], users[1]
			keys := NewAPIKeyStore(db)

			created, err := keys.Create(ctx, CreateAPIKeyParams{UserID: owner, Name: "mine"})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			if err := keys.Revoke(ctx, stranger, created.Key.ID, time.Now().UTC()); !errors.Is(err, ErrAPIKeyNotFound) {
				t.Errorf("non-owner Revoke() error = %v, want ErrAPIKeyNotFound", err)
			}
			reloaded, err := keys.ByID(ctx, created.Key.ID)
			if err != nil {
				t.Fatalf("ByID() error = %v", err)
			}
			if reloaded.RevokedAt != nil {
				t.Error("a non-owner revoked the key")
			}
			if _, err := keys.Authenticate(ctx, created.Plaintext, time.Now().UTC()); err != nil {
				t.Errorf("Authenticate() after denied revoke error = %v, want nil", err)
			}
		})
	}
}

// TestAPIKeyScopeHelpers 表驱动覆盖四档 scope 的判定与归一化。
func TestAPIKeyScopeHelpers(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
		has  bool
	}{
		{"read covers read", ScopeRead, ScopeRead, true},
		{"read lacks write", ScopeRead, ScopeWrite, false},
		{"admin implies write", ScopeAdmin, ScopeWrite, true},
		{"admin implies review", ScopeAdmin, ScopeReview, true},
		{"write does not imply admin", ScopeWrite, ScopeAdmin, false},
		{"multi covers review", "read,review", ScopeReview, true},
		{"multi lacks admin", "read,review", ScopeAdmin, false},
		{"empty lacks anything", "", ScopeRead, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasScope(tc.raw, tc.want); got != tc.has {
				t.Errorf("HasScope(%q, %q) = %v, want %v", tc.raw, tc.want, got, tc.has)
			}
			key := &APIKey{ID: 1, Scopes: tc.raw}
			if got := key.HasScope(tc.want); got != tc.has {
				t.Errorf("(*APIKey).HasScope(%q) = %v, want %v", tc.want, got, tc.has)
			}
		})
	}

	t.Run("normalize dedupes and orders", func(t *testing.T) {
		got, err := NormalizeScopes([]string{ScopeWrite, ScopeRead, ScopeWrite})
		if err != nil {
			t.Fatalf("NormalizeScopes() error = %v", err)
		}
		if got != "read,write" {
			t.Errorf("NormalizeScopes() = %q, want %q", got, "read,write")
		}
	})
	t.Run("empty defaults to read", func(t *testing.T) {
		got, err := NormalizeScopes(nil)
		if err != nil {
			t.Fatalf("NormalizeScopes(nil) error = %v", err)
		}
		if got != ScopeRead {
			t.Errorf("NormalizeScopes(nil) = %q, want %q", got, ScopeRead)
		}
	})
	t.Run("invalid scope rejected", func(t *testing.T) {
		if _, err := NormalizeScopes([]string{"root"}); !errors.Is(err, ErrAPIKeyInvalidScope) {
			t.Errorf("NormalizeScopes(root) error = %v, want ErrAPIKeyInvalidScope", err)
		}
	})
}
