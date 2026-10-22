// Package auth implements built-in accounts, password hashing, sessions and CSRF.
// Internal logs and error text are always English; code comments explain "why" in Chinese (AGENTS.md §2.1, §2.4).
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// The argon2id parameters are stored together with the hash, and the format carries a version number, so strength can be raised later without invalidating existing hashes.
const argon2IDVersion = 19

// Params describes the strength parameters of one argon2id computation.
type Params struct {
	Memory     uint32
	Time       uint32
	Threads    uint8
	SaltLength uint32
	KeyLength  uint32
}

// DefaultParams is the currently recommended strength: changing it means "new hashes use the new strength", and hashes already stored keep verifying because they carry their own parameters.
func DefaultParams() Params {
	return Params{Memory: 64 * 1024, Time: 3, Threads: 4, SaltLength: 16, KeyLength: 32}
}

// PasswordHasher computes argon2id hashes with fixed parameters; tests can inject weak parameters to speed things up.
type PasswordHasher struct {
	params Params
	rand   func([]byte) error

	// dummyOnce guarantees the dummy hash is generated exactly once and then frozen. Were it rebuilt on every request,
	// that branch would pay one extra full argon2id cost and would instead become an amplification surface (see VerifyDummy).
	dummyOnce sync.Once
	dummyHash string
	dummyErr  error

	// onVerify fires after every argon2id verification done through this hasher; nil by default.
	// It is the observable proxy for "neither login path skipped the password check": tests assert the call count with it,
	// so they need not write fragile assertions like "the milliseconds must be equal" that invariably flap on shared CI.
	onVerify func()
}

// NewPasswordHasher builds a hasher with the given parameters; zero-valued parameters fall back to DefaultParams.
func NewPasswordHasher(p Params) *PasswordHasher {
	if p.Memory == 0 || p.Time == 0 || p.Threads == 0 || p.SaltLength == 0 || p.KeyLength == 0 {
		p = DefaultParams()
	}
	return &PasswordHasher{params: p, rand: func(b []byte) error { _, err := rand.Read(b); return err }}
}

// DefaultPasswordHasher returns a hasher with the production parameters.
func DefaultPasswordHasher() *PasswordHasher { return NewPasswordHasher(DefaultParams()) }

// Params returns the parameters the hasher uses, for test assertions.
func (h *PasswordHasher) Params() Params { return h.params }

// Hash produces an encoded string of the form $argon2id$v=19$m=65536,t=3,p=4$<salt>$<hash>.
// Writing the parameters into the encoded string is where "the parameters travel with the hash" lands: verification only looks at the hash itself and does not depend on the configuration in force at the time.
func (h *PasswordHasher) Hash(password string) (string, error) {
	salt := make([]byte, h.params.SaltLength)
	if err := h.rand(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, h.params.Time, h.params.Memory, h.params.Threads, h.params.KeyLength)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2IDVersion, h.params.Memory, h.params.Time, h.params.Threads,
		enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

// dummyPlaintext is the plaintext behind the dummy hash; it never compares successfully against a real password,
// and exists only to make the "user does not exist / no local password" branch pay the same argon2id cost as a real verification.
const dummyPlaintext = "engram-dummy-password-for-timing-equalisation"

// verify is the hasher's single entry point for argon2id verification, responsible for firing the onVerify observation hook once the computation is done.
func (h *PasswordHasher) verify(encoded, password string) (bool, error) {
	ok, err := Verify(encoded, password)
	if h.onVerify != nil {
		h.onVerify()
	}
	return ok, err
}

// VerifyDummy runs one discarded argon2id verification with exactly the parameters of a real hash,
// making the "user does not exist / no local password" branch indistinguishable in duration from "existing user + wrong password".
//
// The dummy hash is generated once on first call and then frozen: regenerating it per request would add a second argon2id cost,
// turning that branch into a heavier amplification surface, worse than the original timing gap. The parameters come from this hasher,
// so its cost matches that of real hashes produced by the same hasher.
func (h *PasswordHasher) VerifyDummy(password string) {
	h.dummyOnce.Do(func() {
		h.dummyHash, h.dummyErr = h.Hash(dummyPlaintext)
	})
	if h.dummyErr != nil {
		// On generation failure the original semantics hold: the result is still discarded and the caller returns the uniform error; only the timing leveling is lost.
		return
	}
	_, _ = h.verify(h.dummyHash, password)
}

// Verify checks whether a plaintext password matches an encoded hash; the parameters are parsed from the hash itself, so old-strength hashes verify without migration.
func Verify(encoded, password string) (bool, error) {
	params, salt, want, err := decodeHash(encoded)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(password), salt, params.Time, params.Memory, params.Threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// decodeHash parses the encoded string; an unrecognized version number fails explicitly rather than silently computing a wrong answer.
func decodeHash(encoded string) (Params, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	// Of the form ["", "argon2id", "v=19", "m=...,t=...,p=...", salt, hash], 6 segments in total.
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return Params{}, nil, nil, errors.New("password hash: not an argon2id encoded hash")
	}
	if parts[2] != fmt.Sprintf("v=%d", argon2IDVersion) {
		return Params{}, nil, nil, fmt.Errorf("password hash: unsupported argon2id version %q", parts[2])
	}
	var p Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil {
		return Params{}, nil, nil, fmt.Errorf("password hash: bad parameters %q: %w", parts[3], err)
	}
	enc := base64.RawStdEncoding
	salt, err := enc.DecodeString(parts[4])
	if err != nil {
		return Params{}, nil, nil, fmt.Errorf("password hash: bad salt: %w", err)
	}
	key, err := enc.DecodeString(parts[5])
	if err != nil {
		return Params{}, nil, nil, fmt.Errorf("password hash: bad key: %w", err)
	}
	if p.Memory == 0 || p.Time == 0 || p.Threads == 0 || len(salt) == 0 || len(key) == 0 {
		return Params{}, nil, nil, errors.New("password hash: parameters must be positive")
	}
	return p, salt, key, nil
}

// Password policy: a minimum length plus a small wordlist of common weak passwords, with no external service.
const (
	MinPasswordLength = 8
	MaxPasswordLength = 128
)

// weakPasswords is the built-in small wordlist; it only blocks the few passwords most often used in credential stuffing and makes no completeness claim (that is a question of cryptographic strength, not of the wordlist).
var weakPasswords = map[string]struct{}{
	"password": {}, "password1": {}, "password123": {}, "12345678": {}, "123456789": {},
	"1234567890": {}, "qwerty123": {}, "qwertyuiop": {}, "letmein": {}, "iloveyou": {},
	"admin123": {}, "welcome1": {}, "changeme": {}, "abc12345": {}, "11111111": {},
}

// ValidatePasswordPolicy returns English errors (internal errors, not localized, AGENTS.md §2.1).
func ValidatePasswordPolicy(password string) error {
	if len(password) < MinPasswordLength {
		return fmt.Errorf("password must be at least %d characters", MinPasswordLength)
	}
	if len(password) > MaxPasswordLength {
		return fmt.Errorf("password must be at most %d characters", MaxPasswordLength)
	}
	if _, weak := weakPasswords[strings.ToLower(strings.TrimSpace(password))]; weak {
		return errors.New("password is too common")
	}
	return nil
}
