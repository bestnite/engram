package mail

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// testDB opens a temporary SQLite database and migrates every model (mail_outbox included).
func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "mail.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	return db
}

// testCodec builds a codec from a programmatically generated placeholder key; no real key literal
// lives in the repository.
func testCodec(t *testing.T) *store.SecretCodec {
	t.Helper()
	// A 32-byte base64 value (repeated 0x2a), for tests only.
	key := "KioqKioqKioqKioqKioqKioqKioqKioqKioqKioqKio="
	codec, err := store.NewSecretCodec(key)
	if err != nil {
		t.Fatalf("NewSecretCodec: %v", err)
	}
	return codec
}

// resolverWith builds a resolver that uses a map as the environment variables.
func resolverWith(t *testing.T, db *gorm.DB, codec *store.SecretCodec, env map[string]string) *Resolver {
	t.Helper()
	return &Resolver{
		DB:      db,
		Secrets: codec,
		LookupEnv: func(k string) (string, bool) {
			v, ok := env[k]
			return v, ok
		},
	}
}

// setSettings writes a number of SMTP settings into the settings table.
func setSettings(t *testing.T, db *gorm.DB, kv map[string]string) {
	t.Helper()
	now := time.Now().UTC()
	for k, v := range kv {
		if err := store.PutSetting(context.Background(), db, k, v, nil, now); err != nil {
			t.Fatalf("PutSetting(%s): %v", k, err)
		}
	}
}

// splitHostPort splits "host:port" into two parts; for test assertions, invalid input fails
// immediately.
func splitHostPort(t *testing.T, addr string) (string, string) {
	t.Helper()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", addr, err)
	}
	return host, port
}
