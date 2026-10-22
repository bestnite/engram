package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"git.nite07.com/nite/engram/internal/store"
)

// This file is the TOTP business layer: generating the secret, encrypting it into storage, verifying codes, one-time recovery codes, disabling.
//
// Dependency choices (AGENTS.md §2.4): the TOTP algorithm uses the de facto standard library
// github.com/pquerna/otp instead of hand-rolling HMAC. The library handles secret generation and code verification;
// assembling the otpauth:// link is mere format transcription, and using the same helper keeps generation and display consistent.

const (
	// DefaultTOTPIssuer is the issuer name shown in authenticator apps (the project's official name).
	DefaultTOTPIssuer = "Engram"
	// totpPeriod and totpSkew: 30 seconds per step, allowing one step of drift either way (client clock skew).
	totpPeriod = 30
	totpSkew   = 1
	// RecoveryCodeCount is how many recovery codes are generated at a time.
	RecoveryCodeCount = 10
	// recoveryCodeLength is the character count of a single recovery code (excluding the separator hyphens).
	recoveryCodeLength = 10
	// recoveryCodeAlphabet is the Base32 character set, with confusable characters such as 0/1/8/9 removed.
	recoveryCodeAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
)

// Stable TOTP-related error values; the transport layer maps them to language pack keys.
var (
	ErrTOTPNotEnabled     = errors.New("totp is not enabled")
	ErrTOTPAlreadyEnabled = errors.New("totp is already enabled")
	ErrTOTPNoPendingSetup = errors.New("no pending totp setup")
	ErrTOTPInvalidCode    = errors.New("invalid totp code")
)

// TOTPService owns the full TOTP lifecycle.
type TOTPService struct {
	store  *store.TOTPStore
	codec  *store.SecretCodec
	issuer string
	now    func() time.Time
}

// NewTOTPService constructs the TOTP service; store and codec are both mandatory (the secret must be stored encrypted).
func NewTOTPService(st *store.TOTPStore, codec *store.SecretCodec, issuer string) (*TOTPService, error) {
	if st == nil || codec == nil {
		return nil, errors.New("auth: totp store and secret codec are required")
	}
	if strings.TrimSpace(issuer) == "" {
		issuer = DefaultTOTPIssuer
	}
	return &TOTPService{store: st, codec: codec, issuer: issuer,
		now: func() time.Time { return time.Now().UTC() }}, nil
}

// Enabled reports whether the user has TOTP enabled (only a confirmed binding counts).
func (s *TOTPService) Enabled(ctx context.Context, userID uint64) (bool, error) {
	return s.store.Enabled(ctx, userID)
}

// Begin generates a new secret and stores it encrypted in an unconfirmed state; it returns the plaintext secret and the otpauth link.
// The plaintext is returned exactly once, at this moment (for page display); only the ciphertext is in storage. An enabled account may not re-bind.
func (s *TOTPService) Begin(ctx context.Context, userID uint64, accountName string) (secret, otpauth string, err error) {
	enabled, err := s.store.Enabled(ctx, userID)
	if err != nil {
		return "", "", err
	}
	if enabled {
		return "", "", ErrTOTPAlreadyEnabled
	}
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      s.issuer,
		AccountName: accountName,
		Period:      totpPeriod,
	})
	if err != nil {
		return "", "", fmt.Errorf("generate totp key: %w", err)
	}
	secret = key.Secret()
	ciphertext, err := s.codec.Encrypt(secret)
	if err != nil {
		return "", "", fmt.Errorf("encrypt totp secret: %w", err)
	}
	if err := s.store.UpsertPending(ctx, userID, ciphertext, s.now()); err != nil {
		return "", "", err
	}
	return secret, otpauthURL(s.issuer, accountName, secret), nil
}

// Pending retrieves the still-unconfirmed binding (plaintext secret and otpauth link) so the settings page can re-display it after a refresh.
// Returning (nil, nil, false, nil) means there is no pending binding.
func (s *TOTPService) Pending(ctx context.Context, userID uint64, accountName string) (secret, otpauth string, ok bool, err error) {
	row, err := s.store.Get(ctx, userID)
	if err != nil {
		if store.IsNotFound(err) {
			return "", "", false, nil
		}
		return "", "", false, err
	}
	if row.ConfirmedAt != nil {
		return "", "", false, nil
	}
	plain, err := s.codec.Decrypt(row.SecretCiphertext)
	if err != nil {
		// Failing to decrypt must fail loudly and never be treated as "not bound" (convention).
		return "", "", false, fmt.Errorf("decrypt pending totp secret: %w", err)
	}
	return plain, otpauthURL(s.issuer, accountName, plain), true, nil
}

// Confirm confirms the pending binding with a single code; on success it enables TOTP and generates a batch of one-time recovery codes.
// The returned plaintext recovery codes are handed out exactly once, at this moment.
// The code accepted at confirmation records its time step too: the second factor then takes effect immediately after confirmation;
// without that record, an observed confirmation code could still pass one login inside its window (exactly the replay path we want closed).
func (s *TOTPService) Confirm(ctx context.Context, userID uint64, code string) (recoveryCodes []string, err error) {
	row, err := s.store.Get(ctx, userID)
	if err != nil {
		if store.IsNotFound(err) {
			return nil, ErrTOTPNoPendingSetup
		}
		return nil, err
	}
	if row.ConfirmedAt != nil {
		return nil, ErrTOTPAlreadyEnabled
	}
	plain, err := s.codec.Decrypt(row.SecretCiphertext)
	if err != nil {
		return nil, fmt.Errorf("decrypt pending totp secret: %w", err)
	}
	ok, err := s.acceptCode(ctx, userID, plain, code)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrTOTPInvalidCode
	}
	if err := s.store.Confirm(ctx, userID, s.now()); err != nil {
		return nil, err
	}
	return s.RegenerateRecoveryCodes(ctx, userID)
}

// VerifySecondFactor verifies one login second-step input: it first checks a 6-digit code and, on failure, tries the recovery codes.
// The returned usedRecovery says this attempt passed with a recovery code (the caller audits on it).
// Not enabled or empty input always returns (false, false, nil) without distinguishing the reason, so a brute-forcer gains no extra information.
func (s *TOTPService) VerifySecondFactor(ctx context.Context, userID uint64, code string) (usedRecovery, ok bool, err error) {
	entry := strings.TrimSpace(code)
	if entry == "" {
		return false, false, nil
	}
	row, err := s.store.Get(ctx, userID)
	if err != nil {
		if store.IsNotFound(err) {
			return false, false, nil
		}
		return false, false, err
	}
	if row.ConfirmedAt == nil {
		return false, false, nil
	}
	plain, err := s.codec.Decrypt(row.SecretCiphertext)
	if err != nil {
		return false, false, fmt.Errorf("decrypt totp secret: %w", err)
	}
	if isSixDigits(entry) {
		accepted, err := s.acceptCode(ctx, userID, plain, entry)
		if err != nil {
			return false, false, err
		}
		if accepted {
			return false, true, nil
		}
	}
	consumed, err := s.store.ConsumeRecoveryCode(ctx, userID, hashRecoveryCode(entry), s.now())
	if err != nil {
		return false, false, err
	}
	return consumed, consumed, nil
}

// RegenerateRecoveryCodes generates a fresh batch of recovery codes (plaintext returned once) and invalidates the old unused ones.
func (s *TOTPService) RegenerateRecoveryCodes(ctx context.Context, userID uint64) ([]string, error) {
	plaintext, hashes, err := generateRecoveryCodes(RecoveryCodeCount)
	if err != nil {
		return nil, err
	}
	if err := s.store.ReplaceRecoveryCodes(ctx, userID, hashes, s.now()); err != nil {
		return nil, err
	}
	return plaintext, nil
}

// UnusedRecoveryCodeCount returns how many recovery codes remain usable.
func (s *TOTPService) UnusedRecoveryCodeCount(ctx context.Context, userID uint64) (int64, error) {
	return s.store.UnusedRecoveryCodeCount(ctx, userID)
}

// Disable turns TOTP off and deletes all recovery codes. The caller performs the password confirmation (the handler has the user context).
func (s *TOTPService) Disable(ctx context.Context, userID uint64) error {
	enabled, err := s.store.Enabled(ctx, userID)
	if err != nil {
		return err
	}
	if !enabled {
		return ErrTOTPNotEnabled
	}
	return s.store.Delete(ctx, userID)
}

// acceptCode verifies a 6-digit code and records its time step (the same code may not pass twice within the window).
//
// ok=false has two cases that are not distinguished externally: (1) the code is outside the ±totpSkew window;
// (2) the step is not later than the maximum accepted step (a replay, or an old code earlier than the accepted step).
// The record itself is an atomic conditional update (store.AcceptStep), so under concurrency only one request gets ok=true for the same code.
func (s *TOTPService) acceptCode(ctx context.Context, userID uint64, secret, code string) (bool, error) {
	step, ok := s.matchStep(secret, code, s.now())
	if !ok {
		return false, nil
	}
	return s.store.AcceptStep(ctx, userID, step)
}

// matchStep finds the time step that produces code within the ±totpSkew clock window; (0, false) when it is outside the window.
// It searches from the high step down: if under extreme circumstances two adjacent steps produce the same code, the higher step wins,
// pushing the maximum accepted step as far forward as possible (better to reject one extra time than to let a replay through).
func (s *TOTPService) matchStep(secret, code string, now time.Time) (int64, bool) {
	entry := strings.TrimSpace(code)
	base := now.Unix() / totpPeriod
	for offset := int64(totpSkew); offset >= -int64(totpSkew); offset-- {
		step := base + offset
		candidate, err := totp.GenerateCodeCustom(secret, time.Unix(step*totpPeriod, 0), totp.ValidateOpts{
			Period:    totpPeriod,
			Digits:    otp.DigitsSix,
			Algorithm: otp.AlgorithmSHA1,
		})
		if err != nil {
			return 0, false
		}
		if candidate == entry {
			return step, true
		}
	}
	return 0, false
}

// otpauthURL assembles the otpauth:// link (Google Authenticator Key URI format).
// Generation and display share this one implementation, so the two paths never yield different links.
func otpauthURL(issuer, account, secret string) string {
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", "6")
	q.Set("period", strconv.Itoa(totpPeriod))
	return "otpauth://totp/" + url.PathEscape(issuer+":"+account) + "?" + q.Encode()
}

// isSixDigits reports whether the input is exactly 6 digits (the shape of a verification code).
func isSixDigits(s string) bool {
	if len(s) != 6 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// normalizeRecoveryCode normalizes a recovery code: strip hyphens and spaces, uppercase.
func normalizeRecoveryCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		if strings.ContainsRune(recoveryCodeAlphabet, r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// hashRecoveryCode returns the sha256 hex digest of a recovery code.
// A recovery code is a 50-bit random value rather than a user-chosen password, so it needs no salt or slow hash;
// salting would also mean storing an extra salt per row, which is not worth it. Only the digest is stored; the plaintext never is.
func hashRecoveryCode(code string) string {
	sum := sha256.Sum256([]byte(normalizeRecoveryCode(code)))
	return hex.EncodeToString(sum[:])
}

// generateRecoveryCodes generates n recovery codes, returning the plaintexts (with hyphens) and the digests.
func generateRecoveryCodes(n int) (plaintext []string, hashes []string, err error) {
	plaintext = make([]string, 0, n)
	hashes = make([]string, 0, n)
	for i := 0; i < n; i++ {
		raw, err := randomRecoveryCode(recoveryCodeLength)
		if err != nil {
			return nil, nil, err
		}
		plaintext = append(plaintext, raw[:5]+"-"+raw[5:])
		hashes = append(hashes, hashRecoveryCode(raw))
	}
	return plaintext, hashes, nil
}

// randomRecoveryCode picks length characters uniformly from the Base32 alphabet.
// Rejection sampling removes modulo bias: the alphabet size 32 divides 256 exactly, so the check here is defensive.
func randomRecoveryCode(length int) (string, error) {
	buf := make([]byte, length)
	out := make([]byte, 0, length)
	for len(out) < length {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("generate recovery code: %w", err)
		}
		for _, b := range buf {
			idx := int(b) % len(recoveryCodeAlphabet)
			if idx < len(recoveryCodeAlphabet) {
				out = append(out, recoveryCodeAlphabet[idx])
				if len(out) == length {
					break
				}
			}
		}
	}
	return string(out), nil
}
