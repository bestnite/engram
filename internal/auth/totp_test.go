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

	"git.nite07.com/nite/engram/internal/store"
)

// newTOTPFixture builds an environment of real SQLite + the TOTP store + the service; the database is not mocked (AGENTS.md §2.5).
// testEnv migrates by AllModels() by default and the TOTP table is defined in a new file, so it is added explicitly once here.
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

// TestTOTPSecretIsEncryptedAtRest asserts the secret is stored only as ciphertext: the column holds no plaintext and the prefix is v1:.
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

// TestTOTPConfirmEnablesAndVerifies asserts that TOTP only takes effect after confirmation, and that code verification is correct.
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
	// Before confirmation, the second-step check must fail.
	if _, ok, _ := svc.VerifySecondFactor(ctx, u.ID, mustCode(t, secret)); ok {
		t.Fatal("VerifySecondFactor() succeeded before confirmation")
	}
	// Confirming with a wrong code must be rejected.
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
	used, ok, err := svc.VerifySecondFactor(ctx, u.ID, mustNextCode(t, secret))
	if err != nil || !ok || used {
		t.Fatalf("VerifySecondFactor(valid code) = (usedRecovery=%v, ok=%v, err=%v), want (false, true, nil)", used, ok, err)
	}
}

// TestRecoveryCodeIsSingleUse is acceptance (2): a recovery code can be used only once.
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
	// The same recovery code must fail the second time.
	usedAgain, okAgain, err := svc.VerifySecondFactor(ctx, u.ID, codes[0])
	if err != nil {
		t.Fatalf("second use error = %v", err)
	}
	if okAgain || usedAgain {
		t.Fatalf("reused recovery code = (usedRecovery=%v, ok=%v), want (false, false)", usedAgain, okAgain)
	}
	// The other codes are still usable, and the remaining count drops accordingly.
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

// TestDisableRemovesSecretAndRecoveryCodes asserts that after disabling, the secret row and all recovery codes are gone.
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

// TestBeginRejectedWhenAlreadyEnabled asserts that an enabled account cannot quietly re-bind.
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

// mustCode generates a code from the current time; failure fails the test.
func mustCode(t *testing.T, secret string) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode() error = %v", err)
	}
	return code
}

// mustNextCode generates the code for the next time step.
// An "already accepted time step" may not pass a second time, and this case's confirmation code used up the current step,
// so the login acceptance has to use a later new step that is still within the ±1 window.
func mustNextCode(t *testing.T, secret string) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, time.Now().Add(totpPeriod*time.Second))
	if err != nil {
		t.Fatalf("GenerateCode(next step) error = %v", err)
	}
	return code
}
