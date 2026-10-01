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

// 敏感配置的加密通道（DESIGN.md §8.4、§11；AGENTS.md §5 M6-10）。
//
// 约定：
//   - 主密钥来自环境变量 ENCRYPTION_KEY（internal/config 的 KeyEncryptionKey），
//     按 .env.example 的说明是 base64 编码的 32 字节（生成：openssl rand -base64 32）。
//   - 密文带算法版本前缀，形如 `v1:` + base64(nonce || AES-256-GCM 密文与认证标签)。
//     版本前缀是日后迁移/轮换唯一的挂钩：解密端先读前缀再选算法，将来上 v2
//     （换算法或加 KDF 参数）时调用方无需改动。
//   - 解密失败（换错主密钥、密文损坏）必须大声报错，绝不返回空值：
//     静默返回空串会让调用方把「解不开」误读成「没配置」。
const (
	// secretVersionPrefixV1 是当前密文格式的版本前缀。
	secretVersionPrefixV1 = "v1"
	// secretKeyBytes 是 AES-256 要求的密钥长度。
	secretKeyBytes = 32
	// secretKeyEnv 是主密钥的环境变量名，报错信息里点名便于运维定位。
	secretKeyEnv = "ENCRYPTION_KEY"
)

var (
	// ErrSecretKeyInvalid 表示主密钥不是合法的 base64(32 字节)。
	ErrSecretKeyInvalid = errors.New("invalid secret master key")
	// ErrSecretFormat 表示密文缺少版本前缀或 base64 载荷无法解析。
	ErrSecretFormat = errors.New("malformed secret ciphertext")
	// ErrSecretVersion 表示密文的算法版本前缀不被当前代码支持。
	ErrSecretVersion = errors.New("unsupported secret ciphertext version")
	// ErrSecretDecrypt 表示 GCM 认证失败（主密钥不匹配或密文被篡改）。
	ErrSecretDecrypt = errors.New("secret decryption failed")
)

// SecretCodec 用 AES-256-GCM 加解密敏感设置值。
type SecretCodec struct {
	aead cipher.AEAD
}

// NewSecretCodec 从 ENCRYPTION_KEY 的字符串值构造编解码器。
// 密钥格式非法时返回 ErrSecretKeyInvalid（启动期大声失败，而不是用弱密钥静默加密）。
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

// decodeMasterKey 把配置值解码成 32 字节密钥：优先标准 base64，兼容无填充的 raw base64。
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

// Encrypt 加密明文，返回带版本前缀的密文。
func (c *SecretCodec) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	sealed := c.aead.Seal(nil, nonce, []byte(plaintext), nil)
	payload := append(nonce, sealed...)
	return secretVersionPrefixV1 + ":" + base64.StdEncoding.EncodeToString(payload), nil
}

// Decrypt 解密带版本前缀的密文。
// 主密钥不对时 GCM 认证失败 → 返回 ErrSecretDecrypt，绝不返回空值（M6-10 验收）。
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

// sensitiveKeySuffixes 判定哪些 settings 键必须走加密通道：OIDC client secret、未来 LLM key 等。
// 用后缀约定而不是硬编码键名列表，新增服务商只需按约定命名（例：`llm_api_key`）。
var sensitiveKeySuffixes = []string{"_secret", "_key", "_password", "_token"}

// IsSensitiveSettingKey 报告一个 settings 键是否属于敏感值，管理面板据此决定是否加密存储、
// 以及是否只显示「已配置/未配置」（DESIGN.md §8.4）。
func IsSensitiveSettingKey(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	for _, suffix := range sensitiveKeySuffixes {
		if strings.HasSuffix(k, suffix) {
			return true
		}
	}
	return false
}

// PutSecret 把敏感明文加密后写入 settings 表（upsert，含 updated_by/updated_at）。
// 密文以 JSON 字符串落库，与 LoadSettings 的解码约定一致（models.go 的 Setting.Value 注释）。
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

// GetSecret 读取并解密敏感设置值，供服务端内部使用（例如 OIDC 换取 token）。
// 未配置返回 ("", false, nil)；解密失败返回错误——绝不让「解不开」伪装成「未配置」。
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
	// 值可能是 JSON 字符串（PutSecret 的写法），也可能被手工写成裸密文。
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

// SecretConfigured 只报告「已配置/未配置」，不接触明文，供管理面板展示（DESIGN.md §8.4）。
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
