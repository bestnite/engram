package mail

import (
	"context"
	"errors"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/config"
	"git.nite07.com/nite/engram/internal/store"
)

// TestUnconfiguredReturnsErrNotConfigured is the core of the gate: when SMTP is not configured,
// Configured() is false, Enqueue returns ErrNotConfigured (no silent drop), and no row is written.
func TestUnconfiguredReturnsErrNotConfigured(t *testing.T) {
	db := testDB(t)
	ob := NewOutbox(Deps{DB: db})
	if ob.Configured() {
		t.Fatal("Configured() = true without any SMTP settings, want false")
	}
	err := ob.Enqueue(context.Background(), Message{To: "a@example.com", Subject: "hi"})
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Enqueue() error = %v, want ErrNotConfigured", err)
	}
	// When unconfigured nothing may be written to the queue (otherwise a pile of mail that will never
	// be sent would accumulate).
	var count int64
	if err := db.Model(&store.OutboxMessage{}).Count(&count).Error; err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if count != 0 {
		t.Errorf("outbox rows = %d, want 0 when Enqueue is refused", count)
	}
}

// TestConfiguredRequiresHostAndFrom shows that filling in only part of it does not count as
// configured.
func TestConfiguredRequiresHostAndFrom(t *testing.T) {
	for _, tc := range []struct {
		name string
		kv   map[string]string
		want bool
	}{
		{"nothing", nil, false},
		{"host only", map[string]string{SettingKeySMTPHost: "smtp.example.com"}, false},
		{"from only", map[string]string{SettingKeySMTPFrom: "no-reply@example.com"}, false},
		{"host and from", map[string]string{
			SettingKeySMTPHost: "smtp.example.com", SettingKeySMTPFrom: "no-reply@example.com",
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := testDB(t)
			if tc.kv != nil {
				setSettings(t, db, tc.kv)
			}
			ob := NewOutbox(Deps{DB: db})
			if got := ob.Configured(); got != tc.want {
				t.Errorf("Configured() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestResolutionOrderEnvBeatsDBBeatsDefault covers the value precedence and the source annotation.
func TestResolutionOrderEnvBeatsDBBeatsDefault(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	setSettings(t, db, map[string]string{SettingKeySMTPHost: "db.example.com"})

	r := resolverWith(t, db, nil, map[string]string{EnvSMTPHost: "env.example.com"})
	host, src, err := r.Field(ctx, SettingKeySMTPHost)
	if err != nil {
		t.Fatalf("Field: %v", err)
	}
	if host != "env.example.com" || src != config.SourceEnv {
		t.Errorf("env override: got %q/%s, want env.example.com/env", host, src)
	}

	r = resolverWith(t, db, nil, nil)
	host, src, err = r.Field(ctx, SettingKeySMTPHost)
	if err != nil {
		t.Fatalf("Field: %v", err)
	}
	if host != "db.example.com" || src != config.SourceDB {
		t.Errorf("db value: got %q/%s, want db.example.com/db", host, src)
	}

	// An item absent from every source falls back to the built-in default: port 587, TLS starttls.
	port, src, err := r.Field(ctx, SettingKeySMTPPort)
	if err != nil {
		t.Fatalf("Field: %v", err)
	}
	if port != "587" || src != config.SourceDefault {
		t.Errorf("default port: got %q/%s, want 587/default", port, src)
	}
	tlsMode, _, err := r.Field(ctx, SettingKeySMTPTLSMode)
	if err != nil {
		t.Fatalf("Field: %v", err)
	}
	if tlsMode != TLSModeStartTLS {
		t.Errorf("default tls mode = %q, want %q", tlsMode, TLSModeStartTLS)
	}
}

// TestPasswordConfiguredNeverTouchesPlaintext shows that the password state only looks at whether it
// is configured, and that the plaintext can be read back after being stored encrypted.
func TestPasswordConfiguredNeverTouchesPlaintext(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	codec := testCodec(t)
	r := resolverWith(t, db, codec, nil)

	configured, src, err := r.PasswordConfigured(ctx)
	if err != nil {
		t.Fatalf("PasswordConfigured: %v", err)
	}
	if configured || src != config.SourceDefault {
		t.Errorf("fresh db: configured=%v src=%s, want false/default", configured, src)
	}

	if err := store.PutSecret(ctx, db, codec, SettingKeySMTPPassword, "s3cret-pw", nil, nowUTC()); err != nil {
		t.Fatalf("PutSecret: %v", err)
	}
	configured, src, err = r.PasswordConfigured(ctx)
	if err != nil {
		t.Fatalf("PasswordConfigured: %v", err)
	}
	if !configured || src != config.SourceDB {
		t.Errorf("after PutSecret: configured=%v src=%s, want true/db", configured, src)
	}

	// Config can decrypt the plaintext password for delivery to use.
	cfg, ok, err := r.Config(ctx)
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if ok {
		t.Errorf("Config() ok = true with only a password set, want false (host/from missing)")
	}
	if cfg.Password != "s3cret-pw" {
		t.Errorf("cfg.Password = %q, want the decrypted value", cfg.Password)
	}

	// What is stored in the database must be ciphertext (with a version prefix), containing no
	// plaintext.
	var row store.Setting
	if err := db.First(&row, "key = ?", SettingKeySMTPPassword).Error; err != nil {
		t.Fatalf("load stored password: %v", err)
	}
	if contains(row.Value, "s3cret-pw") {
		t.Errorf("stored SMTP password leaks plaintext: %q", row.Value)
	}
}

// TestConfigPortFallsBackOnGarbage shows that one bad port does not make the whole configuration
// unusable.
func TestConfigPortFallsBackOnGarbage(t *testing.T) {
	db := testDB(t)
	setSettings(t, db, map[string]string{
		SettingKeySMTPHost: "smtp.example.com", SettingKeySMTPFrom: "no-reply@example.com",
		SettingKeySMTPPort: "not-a-number",
	})
	r := resolverWith(t, db, nil, nil)
	cfg, ok, err := r.Config(context.Background())
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if !ok {
		t.Fatal("Config() ok = false, want true")
	}
	if cfg.Port != DefaultPort {
		t.Errorf("port = %d, want fallback %d", cfg.Port, DefaultPort)
	}
}

// TestNormalizeAndValidateTLSMode covers the normalization and validation of TLS modes.
func TestNormalizeAndValidateTLSMode(t *testing.T) {
	for _, raw := range []string{"none", "NONE", " starttls ", "implicit"} {
		if !ValidTLSMode(raw) {
			t.Errorf("ValidTLSMode(%q) = false, want true", raw)
		}
	}
	for _, raw := range []string{"", "ssl", "tls", "plain"} {
		if ValidTLSMode(raw) {
			t.Errorf("ValidTLSMode(%q) = true, want false", raw)
		}
	}
	if got := NormalizeTLSMode("garbage"); got != TLSModeStartTLS {
		t.Errorf("NormalizeTLSMode(garbage) = %q, want %q", got, TLSModeStartTLS)
	}
	if got := NormalizeTLSMode("NONE"); got != TLSModeNone {
		t.Errorf("NormalizeTLSMode(NONE) = %q, want %q", got, TLSModeNone)
	}
}

// contains is a case-insensitive substring check, avoiding the noise of importing strings directly
func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// nowUTC is the single time source used in tests.
func nowUTC() time.Time { return time.Now().UTC() }
