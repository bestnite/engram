package mail

import (
	"context"
	"errors"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/config"
	"git.nite07.com/nite/engram/internal/store"
)

// TestUnconfiguredReturnsErrNotConfigured 是门禁核心：未配置 SMTP 时
// Configured() 为 false，Enqueue 返回 ErrNotConfigured（不静默丢弃），且不写任何行。
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
	// 未配置时不得写进队列（否则会积压一堆永远不会发的邮件）。
	var count int64
	if err := db.Model(&store.OutboxMessage{}).Count(&count).Error; err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if count != 0 {
		t.Errorf("outbox rows = %d, want 0 when Enqueue is refused", count)
	}
}

// TestConfiguredRequiresHostAndFrom 证明只填一部分不算已配置。
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

// TestResolutionOrderEnvBeatsDBBeatsDefault 覆盖取值优先级与来源标注。
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

	// 未在任一来源出现的项回落到内置默认值：端口 587、TLS starttls。
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

// TestPasswordConfiguredNeverTouchesPlaintext 证明口令状态只看配置与否，且明文经加密存储后可读回。
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

	// Config 能解出明文口令供投递使用。
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

	// 库里存的必须是密文（含版本前缀），不含明文。
	var row store.Setting
	if err := db.First(&row, "key = ?", SettingKeySMTPPassword).Error; err != nil {
		t.Fatalf("load stored password: %v", err)
	}
	if contains(row.Value, "s3cret-pw") {
		t.Errorf("stored SMTP password leaks plaintext: %q", row.Value)
	}
}

// TestConfigPortFallsBackOnGarbage 证明一个坏端口不会让整条配置不可用。
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

// TestNormalizeAndValidateTLSMode 覆盖 TLS 模式的归一化与校验。
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

// contains 是一个不看大小写的子串判断，避免直接引 strings 造成噪音。
func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// nowUTC 是测试里的统一时间源。
func nowUTC() time.Time { return time.Now().UTC() }
