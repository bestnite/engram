package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The encryption channel for sensitive configuration.
//
// Conventions:
//   - the master key comes from the ENCRYPTION_KEY environment variable (KeyEncryptionKey in internal/config),
//     which per .env.example is 32 bytes base64-encoded (generate with: openssl rand -base64 32).
//   - the ciphertext carries an algorithm version prefix, of the form `v1:` + base64(nonce || AES-256-GCM ciphertext and auth tag).
//     The version prefix is the only hook for a future migration/rotation: the decryptor reads the prefix first and then picks the algorithm, so when v2 arrives
//     (a different algorithm or added KDF parameters) callers need no change.
//   - a decryption failure (wrong master key, corrupt ciphertext) must error loudly and never return an empty value:
//     silently returning an empty string would let the caller misread "cannot decrypt" as "not configured".
const (
	// secretVersionPrefixV1 is the version prefix of the current ciphertext format.
	secretVersionPrefixV1 = "v1"
	// secretKeyBytes is the key length AES-256 requires.
	secretKeyBytes = 32
	// secretKeyEnv is the master key's environment variable name, spelled out in error messages so operators can find it.
	secretKeyEnv = "ENCRYPTION_KEY"
)

var (
	// ErrSecretKeyInvalid means the master key is not valid base64(32 bytes).
	ErrSecretKeyInvalid = errors.New("invalid secret master key")
	// ErrSecretFormat means the ciphertext lacks its version prefix or the base64 payload does not parse.
	ErrSecretFormat = errors.New("malformed secret ciphertext")
	// ErrSecretVersion means the ciphertext's algorithm version prefix is not supported by this code.
	ErrSecretVersion = errors.New("unsupported secret ciphertext version")
	// ErrSecretDecrypt means GCM authentication failed (master key mismatch or tampered ciphertext).
	ErrSecretDecrypt = errors.New("secret decryption failed")
)

// SecretCodec encrypts and decrypts sensitive setting values with AES-256-GCM.
type SecretCodec struct {
	aead cipher.AEAD
}

// NewSecretCodec builds the codec from the string value of ENCRYPTION_KEY.
// An illegal key format returns ErrSecretKeyInvalid (fail loudly at startup instead of silently encrypting with a weak key).
func NewSecretCodec(masterKey string) (*SecretCodec, error) {
	raw, err := decodeMasterKey(masterKey)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, fmt.Errorf("init cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("init gcm: %w", err)
	}
	return &SecretCodec{aead: aead}, nil
}

// decodeMasterKey decodes the configured value into a 32-byte key: standard base64 first, unpadded raw base64 accepted too.
func decodeMasterKey(masterKey string) ([]byte, error) {
	trimmed := strings.TrimSpace(masterKey)
	if trimmed == "" {
		return nil, fmt.Errorf("%w: %s is empty", ErrSecretKeyInvalid, secretKeyEnv)
	}
	raw, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(trimmed)
	}
	if err != nil || len(raw) != secretKeyBytes {
		return nil, fmt.Errorf("%w: %s must be base64 of %d bytes (openssl rand -base64 %d)",
			ErrSecretKeyInvalid, secretKeyEnv, secretKeyBytes, secretKeyBytes)
	}
	return raw, nil
}

// Encrypt encrypts the plaintext and returns ciphertext carrying the version prefix.
func (c *SecretCodec) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	sealed := c.aead.Seal(nil, nonce, []byte(plaintext), nil)
	payload := append(nonce, sealed...)
	return secretVersionPrefixV1 + ":" + base64.StdEncoding.EncodeToString(payload), nil
}

// Decrypt decrypts ciphertext carrying the version prefix.
// With a wrong master key GCM authentication fails -> ErrSecretDecrypt is returned, never an empty value
func (c *SecretCodec) Decrypt(ciphertext string) (string, error) {
	version, payload, ok := strings.Cut(ciphertext, ":")
	if !ok {
		return "", fmt.Errorf("%w: missing %q version prefix", ErrSecretFormat, secretVersionPrefixV1)
	}
	if version != secretVersionPrefixV1 {
		return "", fmt.Errorf("%w: %q", ErrSecretVersion, version)
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrSecretFormat, err)
	}
	if len(raw) < c.aead.NonceSize() {
		return "", fmt.Errorf("%w: payload shorter than a nonce", ErrSecretFormat)
	}
	nonce, body := raw[:c.aead.NonceSize()], raw[c.aead.NonceSize():]
	plaintext, err := c.aead.Open(nil, nonce, body, nil)
	if err != nil {
		return "", fmt.Errorf("%w: authentication failed (wrong %s or corrupted ciphertext)", ErrSecretDecrypt, secretKeyEnv)
	}
	return string(plaintext), nil
}

// sensitiveKeySuffixes decides which settings keys must go through the encrypted channel: the OIDC client secret, future LLM keys, etc.
// A suffix convention rather than a hard-coded list of key names, so a new provider only has to name its key by the convention (e.g. `llm_api_key`).
var sensitiveKeySuffixes = []string{"_secret", "_key", "_password", "_token"}

// IsSensitiveSettingKey reports whether a settings key holds a sensitive value; the admin panel uses it to decide whether to store the value encrypted
// and whether to show only "configured/not configured".
func IsSensitiveSettingKey(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	for _, suffix := range sensitiveKeySuffixes {
		if strings.HasSuffix(k, suffix) {
			return true
		}
	}
	return false
}

// PutSecret encrypts the sensitive plaintext and writes it into the settings table (upsert, including updated_by/updated_at).
// The ciphertext is stored as a JSON string, matching LoadSettings' decoding convention (the Setting.Value comment in models.go).
func PutSecret(ctx context.Context, db *gorm.DB, codec *SecretCodec, key, plaintext string, updatedBy *uint64, at time.Time) error {
	if codec == nil {
		return errors.New("store: SecretCodec is required")
	}
	if strings.TrimSpace(key) == "" {
		return errors.New("store: setting key is required")
	}
	encrypted, err := codec.Encrypt(plaintext)
	if err != nil {
		return fmt.Errorf("encrypt setting %q: %w", key, err)
	}
	encoded, err := json.Marshal(encrypted)
	if err != nil {
		return fmt.Errorf("encode setting %q: %w", key, err)
	}
	row := Setting{Key: key, Value: string(encoded), UpdatedBy: updatedBy, UpdatedAt: at.UTC()}
	if err := db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updated_by", "updated_at"}),
	}).Create(&row).Error; err != nil {
		return fmt.Errorf("store setting %q: %w", key, err)
	}
	return nil
}

// GetSecret reads and decrypts a sensitive setting value for internal server use (exchanging an OIDC token, for example).
// Unconfigured returns ("", false, nil); a decryption failure returns an error -- "cannot decrypt" must never masquerade as "not configured".
func GetSecret(ctx context.Context, db *gorm.DB, codec *SecretCodec, key string) (string, bool, error) {
	if codec == nil {
		return "", false, errors.New("store: SecretCodec is required")
	}
	var row Setting
	if err := db.WithContext(ctx).First(&row, "key = ?", key).Error; err != nil {
		if IsNotFound(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("load setting %q: %w", key, err)
	}
	// The value may be a JSON string (as PutSecret writes it) or hand-written as bare ciphertext.
	ciphertext := row.Value
	var decoded string
	if err := json.Unmarshal([]byte(row.Value), &decoded); err == nil {
		ciphertext = decoded
	}
	if strings.TrimSpace(ciphertext) == "" {
		return "", false, nil
	}
	plaintext, err := codec.Decrypt(ciphertext)
	if err != nil {
		return "", false, fmt.Errorf("decrypt setting %q: %w", key, err)
	}
	return plaintext, true, nil
}

// SecretConfigured only reports "configured/not configured" and never touches the plaintext, for the admin panel to display.
func SecretConfigured(ctx context.Context, db *gorm.DB, key string) (bool, error) {
	var row Setting
	if err := db.WithContext(ctx).First(&row, "key = ?", key).Error; err != nil {
		if IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("load setting %q: %w", key, err)
	}
	return strings.TrimSpace(row.Value) != "", nil
}
