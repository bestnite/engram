package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testCodec 用程序化生成的占位密钥（32 个相同字节的 base64）构造编解码器，
// 不在仓库里放任何真实密钥字面量（AGENTS.md §2.2 脱敏要求）。
func testCodec(t *testing.T, fill byte) *SecretCodec {
	t.Helper()
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{fill}, secretKeyBytes))
	codec, err := NewSecretCodec(key)
	if err != nil {
		t.Fatalf("NewSecretCodec: %v", err)
	}
	return codec
}

// TestSecretRoundTrip 是第一条验收：加密值能正确往返，且密文带算法版本前缀。
func TestSecretRoundTrip(t *testing.T) {
	codec := testCodec(t, 0x2a)
	const plaintext = "oidc-client-secret-value"
	ciphertext, err := codec.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if !strings.HasPrefix(ciphertext, secretVersionPrefixV1+":") {
		t.Fatalf("ciphertext %q does not carry the %q version prefix", ciphertext, secretVersionPrefixV1)
	}
	if strings.Contains(ciphertext, plaintext) {
		t.Fatalf("ciphertext leaks plaintext: %q", ciphertext)
	}
	got, err := codec.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if got != plaintext {
		t.Fatalf("round-trip = %q, want %q", got, plaintext)
	}
	t.Logf("round-trip ok: plaintext=%q ciphertext=%s", plaintext, ciphertext)
}

// TestSecretWrongKeyFailsLoudly 是第二条验收：用错主密钥解密必须报错，不得返回空值。
func TestSecretWrongKeyFailsLoudly(t *testing.T) {
	encryptor := testCodec(t, 0x2a)
	decryptor := testCodec(t, 0x2b) // 不同的主密钥
	ciphertext, err := encryptor.Encrypt("llm-api-key-value")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	got, err := decryptor.Decrypt(ciphertext)
	if err == nil {
		t.Fatalf("Decrypt with wrong key returned %q, error = nil; want a loud failure", got)
	}
	if !errors.Is(err, ErrSecretDecrypt) {
		t.Fatalf("Decrypt error = %v, want it to wrap ErrSecretDecrypt", err)
	}
	if got != "" {
		t.Fatalf("Decrypt with wrong key returned %q, want empty alongside the error", got)
	}
	t.Logf("wrong key -> error: %v", err)
}

// TestSecretFormatAndVersionErrors 覆盖版本前缀与格式错误：都必须报错而不是空值。
func TestSecretFormatAndVersionErrors(t *testing.T) {
	codec := testCodec(t, 0x2a)
	ciphertext, err := codec.Encrypt("x")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	cases := []struct {
		name    string
		input   string
		wantErr error
	}{
		{"no version prefix", strings.TrimPrefix(ciphertext, secretVersionPrefixV1+":"), ErrSecretFormat},
		{"unknown version", "v9:" + strings.TrimPrefix(ciphertext, secretVersionPrefixV1+":"), ErrSecretVersion},
		{"bad base64 payload", secretVersionPrefixV1 + ":not base64!!", ErrSecretFormat},
		{"truncated payload", secretVersionPrefixV1 + ":" + base64.StdEncoding.EncodeToString([]byte{1, 2, 3}), ErrSecretFormat},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := codec.Decrypt(tc.input)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Decrypt(%q) error = %v, want %v", tc.input, err, tc.wantErr)
			}
			if got != "" {
				t.Fatalf("Decrypt(%q) returned %q, want empty alongside the error", tc.input, got)
			}
		})
	}
}

// TestSecretKeyValidation 覆盖主密钥格式校验：空值与非 32 字节都必须在构造期大声失败。
func TestSecretKeyValidation(t *testing.T) {
	for _, bad := range []string{"", "   ", "CHANGE_ME_BASE64_32_BYTES", base64.StdEncoding.EncodeToString([]byte("short"))} {
		if _, err := NewSecretCodec(bad); !errors.Is(err, ErrSecretKeyInvalid) {
			t.Fatalf("NewSecretCodec(%q) error = %v, want ErrSecretKeyInvalid", bad, err)
		}
	}
}

// TestIsSensitiveSettingKey 覆盖敏感键的命名约定判定。
func TestIsSensitiveSettingKey(t *testing.T) {
	sensitive := []string{"oidc_client_secret", "llm_api_key", "smtp_password", "webhook_token"}
	for _, key := range sensitive {
		if !IsSensitiveSettingKey(key) {
			t.Fatalf("IsSensitiveSettingKey(%q) = false, want true", key)
		}
	}
	for _, key := range []string{"registration_policy", "http_addr", "media_max_bytes"} {
		if IsSensitiveSettingKey(key) {
			t.Fatalf("IsSensitiveSettingKey(%q) = true, want false", key)
		}
	}
}

// TestSecretSettingChannel 覆盖 settings 表上的加密通道：写入即密文、读回即明文、
// 以及只暴露「已配置/未配置」的查询。
func TestSecretSettingChannel(t *testing.T) {
	db, err := Open("sqlite", filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	codec := testCodec(t, 0x2a)
	ctx := context.Background()

	configured, err := SecretConfigured(ctx, db, "oidc_client_secret")
	if err != nil {
		t.Fatalf("SecretConfigured(before): %v", err)
	}
	if configured {
		t.Fatalf("SecretConfigured(before) = true, want false")
	}

	const plaintext = "super-secret-oidc-value"
	if err := PutSecret(ctx, db, codec, "oidc_client_secret", plaintext, nil, time.Now()); err != nil {
		t.Fatalf("PutSecret: %v", err)
	}

	// 落库的必须是密文：直接读原始行，确认不含明文。
	var row Setting
	if err := db.WithContext(ctx).First(&row, "key = ?", "oidc_client_secret").Error; err != nil {
		t.Fatalf("load raw row: %v", err)
	}
	if strings.Contains(row.Value, plaintext) {
		t.Fatalf("stored value leaks plaintext: %q", row.Value)
	}

	got, ok, err := GetSecret(ctx, db, codec, "oidc_client_secret")
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}
	if !ok || got != plaintext {
		t.Fatalf("GetSecret = (%q, %v), want (%q, true)", got, ok, plaintext)
	}
	configured, err = SecretConfigured(ctx, db, "oidc_client_secret")
	if err != nil {
		t.Fatalf("SecretConfigured(after): %v", err)
	}
	if !configured {
		t.Fatalf("SecretConfigured(after) = false, want true")
	}

	// upsert 必须覆盖而不是重复插入。
	if err := PutSecret(ctx, db, codec, "oidc_client_secret", "rotated-value", nil, time.Now()); err != nil {
		t.Fatalf("PutSecret(update): %v", err)
	}
	got, ok, err = GetSecret(ctx, db, codec, "oidc_client_secret")
	if err != nil || !ok || got != "rotated-value" {
		t.Fatalf("GetSecret after update = (%q, %v, %v), want (rotated-value, true, nil)", got, ok, err)
	}

	// 换错主密钥读取存储值：必须报错，绝不返回空值。
	wrong := testCodec(t, 0x2b)
	if _, _, err := GetSecret(ctx, db, wrong, "oidc_client_secret"); !errors.Is(err, ErrSecretDecrypt) {
		t.Fatalf("GetSecret with wrong codec error = %v, want ErrSecretDecrypt", err)
	}
}
