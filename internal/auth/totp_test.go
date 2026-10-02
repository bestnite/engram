package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"example.com/engram/internal/store"
)

// newTOTPFixture 构造真实 SQLite + TOTP 存储 + 服务的环境；不 mock 数据库（AGENTS.md §2.5）。
// testEnv 默认按 AllModels() 迁移，TOTP 表在新文件里定义，因此这里显式补一次。
func newTOTPFixture(t *testing.T) (*TOTPService, *store.TOTPStore, *testEnv) {
	t.Helper()
	e := newTestEnv(t)
	if err := e.db.AutoMigrate(store.TOTPModels()...); err != nil {
		t.Fatalf("auto migrate totp tables: %v", err)
	}
	codec, err := store.NewSecretCodec(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x2b}, 32)))
	if err != nil {
		t.Fatalf("NewSecretCodec() error = %v", err)
	}
	st := store.NewTOTPStore(e.db)
	svc, err := NewTOTPService(st, codec, DefaultTOTPIssuer)
	if err != nil {
		t.Fatalf("NewTOTPService() error = %v", err)
	}
	return svc, st, e
}

// TestTOTPSecretIsEncryptedAtRest 断言 secret 只以密文落库：列里没有明文，前缀是 v1:。
func TestTOTPSecretIsEncryptedAtRest(t *testing.T) {
	svc, st, e := newTOTPFixture(t)
	u := e.createUser(t, "totp-enc")

	secret, otpauth, err := svc.Begin(context.Background(), u.ID, u.Username)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	if secret == "" {
		t.Fatal("Begin() returned an empty secret")
	}
	if !strings.Contains(otpauth, "otpauth://totp/") || !strings.Contains(otpauth, "secret="+secret) {
		t.Errorf("otpauth link %q does not carry the secret", otpauth)
	}

	row, err := st.Get(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if row.SecretCiphertext == secret || strings.Contains(row.SecretCiphertext, secret) {
		t.Fatalf("secret is stored in plaintext: %q", row.SecretCiphertext)
	}
	if !strings.HasPrefix(row.SecretCiphertext, "v1:") {
		t.Errorf("ciphertext %q has no version prefix", row.SecretCiphertext)
	}
	if row.ConfirmedAt != nil {
		t.Error("Begin() must not confirm the binding")
	}
}

// TestTOTPConfirmEnablesAndVerifies 断言确认后才生效，且验证码校验正确。
func TestTOTPConfirmEnablesAndVerifies(t *testing.T) {
	svc, _, e := newTOTPFixture(t)
	u := e.createUser(t, "totp-confirm")
	ctx := context.Background()

	secret, _, err := svc.Begin(ctx, u.ID, u.Username)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	if enabled, _ := svc.Enabled(ctx, u.ID); enabled {
		t.Fatal("Enabled() = true before confirmation")
	}
	// 未确认前，第二步校验必须失败。
	if _, ok, _ := svc.VerifySecondFactor(ctx, u.ID, mustCode(t, secret)); ok {
		t.Fatal("VerifySecondFactor() succeeded before confirmation")
	}
	// 用错误的验证码确认必须被拒。
	if _, err := svc.Confirm(ctx, u.ID, "000000"); !errors.Is(err, ErrTOTPInvalidCode) {
		t.Fatalf("Confirm(wrong code) = %v, want ErrTOTPInvalidCode", err)
	}

	codes, err := svc.Confirm(ctx, u.ID, mustCode(t, secret))
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if len(codes) != RecoveryCodeCount {
		t.Errorf("Confirm() returned %d recovery codes, want %d", len(codes), RecoveryCodeCount)
	}
	if enabled, _ := svc.Enabled(ctx, u.ID); !enabled {
		t.Fatal("Enabled() = false after confirmation")
	}
	if _, ok, _ := svc.VerifySecondFactor(ctx, u.ID, "000000"); ok {
		t.Error("VerifySecondFactor(fixed wrong code) = true, want false")
	}
	used, ok, err := svc.VerifySecondFactor(ctx, u.ID, mustCode(t, secret))
	if err != nil || !ok || used {
		t.Fatalf("VerifySecondFactor(valid code) = (usedRecovery=%v, ok=%v, err=%v), want (false, true, nil)", used, ok, err)
	}
}

// TestRecoveryCodeIsSingleUse 是 M1-16 验收②：恢复码只能用一次。
func TestRecoveryCodeIsSingleUse(t *testing.T) {
	svc, st, e := newTOTPFixture(t)
	u := e.createUser(t, "totp-recovery")
	ctx := context.Background()

	secret, _, err := svc.Begin(ctx, u.ID, u.Username)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	codes, err := svc.Confirm(ctx, u.ID, mustCode(t, secret))
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}

	used, ok, err := svc.VerifySecondFactor(ctx, u.ID, codes[0])
	if err != nil || !ok || !used {
		t.Fatalf("first use = (usedRecovery=%v, ok=%v, err=%v), want (true, true, nil)", used, ok, err)
	}
	// 同一个恢复码第二次必须失败。
	usedAgain, okAgain, err := svc.VerifySecondFactor(ctx, u.ID, codes[0])
	if err != nil {
		t.Fatalf("second use error = %v", err)
	}
	if okAgain || usedAgain {
		t.Fatalf("reused recovery code = (usedRecovery=%v, ok=%v), want (false, false)", usedAgain, okAgain)
	}
	// 其它码仍然可用，余量也相应减少。
	if _, okOther, _ := svc.VerifySecondFactor(ctx, u.ID, strings.ToLower(codes[1])); !okOther {
		t.Error("a different recovery code was rejected; want it to still work (case-insensitive)")
	}
	n, err := st.UnusedRecoveryCodeCount(ctx, u.ID)
	if err != nil {
		t.Fatalf("UnusedRecoveryCodeCount() error = %v", err)
	}
	if n != RecoveryCodeCount-2 {
		t.Errorf("unused recovery codes = %d, want %d", n, RecoveryCodeCount-2)
	}
}

// TestDisableRemovesSecretAndRecoveryCodes 断言关闭后 secret 行与恢复码全部消失。
func TestDisableRemovesSecretAndRecoveryCodes(t *testing.T) {
	svc, st, e := newTOTPFixture(t)
	u := e.createUser(t, "totp-disable")
	ctx := context.Background()

	secret, _, err := svc.Begin(ctx, u.ID, u.Username)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	if _, err := svc.Confirm(ctx, u.ID, mustCode(t, secret)); err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if err := svc.Disable(ctx, u.ID); err != nil {
		t.Fatalf("Disable() error = %v", err)
	}
	if enabled, _ := svc.Enabled(ctx, u.ID); enabled {
		t.Error("Enabled() = true after Disable()")
	}
	if _, err := st.Get(ctx, u.ID); !store.IsNotFound(err) {
		t.Errorf("Get() after Disable() = %v, want not found", err)
	}
	if n, _ := st.UnusedRecoveryCodeCount(ctx, u.ID); n != 0 {
		t.Errorf("unused recovery codes after Disable() = %d, want 0", n)
	}
	if err := svc.Disable(ctx, u.ID); !errors.Is(err, ErrTOTPNotEnabled) {
		t.Errorf("second Disable() = %v, want ErrTOTPNotEnabled", err)
	}
}

// TestBeginRejectedWhenAlreadyEnabled 断言已启用的账号不能悄悄换绑。
func TestBeginRejectedWhenAlreadyEnabled(t *testing.T) {
	svc, _, e := newTOTPFixture(t)
	u := e.createUser(t, "totp-rebind")
	ctx := context.Background()
	secret, _, err := svc.Begin(ctx, u.ID, u.Username)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	if _, err := svc.Confirm(ctx, u.ID, mustCode(t, secret)); err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if _, _, err := svc.Begin(ctx, u.ID, u.Username); !errors.Is(err, ErrTOTPAlreadyEnabled) {
		t.Fatalf("Begin() while enabled = %v, want ErrTOTPAlreadyEnabled", err)
	}
}

// mustCode 用当前时间生成一个验证码；失败即测试失败。
func mustCode(t *testing.T, secret string) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode() error = %v", err)
	}
	return code
}
