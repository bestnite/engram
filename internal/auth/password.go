// Package auth 实现内置账号、密码哈希、会话与 CSRF（DESIGN.md §4、§11）。
// 内部日志与错误文案一律英文；代码注释用中文解释"为什么"（AGENTS.md §2.1、§2.4）。
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2id 参数与哈希一起入库，且格式带版本号，便于将来提升强度而不作废旧哈希（DESIGN.md §4.6）。
const argon2IDVersion = 19

// Params 描述一次 argon2id 计算的强度参数。
type Params struct {
	Memory     uint32
	Time       uint32
	Threads    uint8
	SaltLength uint32
	KeyLength  uint32
}

// DefaultParams 是当前推荐强度：修改它等于"新哈希用新强度"，已入库的哈希因自带参数而继续可验证。
func DefaultParams() Params {
	return Params{Memory: 64 * 1024, Time: 3, Threads: 4, SaltLength: 16, KeyLength: 32}
}

// PasswordHasher 按固定参数计算 argon2id 哈希；测试可注入弱参数以加速。
type PasswordHasher struct {
	params Params
	rand   func([]byte) error
}

// NewPasswordHasher 用给定参数构造哈希器；参数为零值时回落到 DefaultParams。
func NewPasswordHasher(p Params) *PasswordHasher {
	if p.Memory == 0 || p.Time == 0 || p.Threads == 0 || p.SaltLength == 0 || p.KeyLength == 0 {
		p = DefaultParams()
	}
	return &PasswordHasher{params: p, rand: func(b []byte) error { _, err := rand.Read(b); return err }}
}

// DefaultPasswordHasher 返回生产参数下的哈希器。
func DefaultPasswordHasher() *PasswordHasher { return NewPasswordHasher(DefaultParams()) }

// Params 返回哈希器使用的参数，供测试断言。
func (h *PasswordHasher) Params() Params { return h.params }

// Hash 生成形如 $argon2id$v=19$m=65536,t=3,p=4$<salt>$<hash> 的编码串。
// 参数写进编码串是"参数随哈希记录"的落点：验证只需看哈希本身，不依赖当时的配置。
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

// Verify 校验明文密码与已编码哈希是否匹配；参数从哈希本身解析，因此旧强度哈希无需迁移即可验证。
func Verify(encoded, password string) (bool, error) {
	params, salt, want, err := decodeHash(encoded)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(password), salt, params.Time, params.Memory, params.Threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// decodeHash 解析编码串；版本号不识别时明确报错而不是静默算错。
func decodeHash(encoded string) (Params, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	// 形如 ["", "argon2id", "v=19", "m=...,t=...,p=...", salt, hash]，共 6 段。
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

// 密码策略（DESIGN.md §4.3）：最小长度 + 常见弱密码小词表，不引外部服务。
const (
	MinPasswordLength = 8
	MaxPasswordLength = 128
)

// weakPasswords 是内置小词表；只挡最常被撞库的少数口令，不做完整性校验（那是加密强度问题，不是词表问题）。
var weakPasswords = map[string]struct{}{
	"password": {}, "password1": {}, "password123": {}, "12345678": {}, "123456789": {},
	"1234567890": {}, "qwerty123": {}, "qwertyuiop": {}, "letmein": {}, "iloveyou": {},
	"admin123": {}, "welcome1": {}, "changeme": {}, "abc12345": {}, "11111111": {},
}

// ValidatePasswordPolicy 返回英文错误（内部错误，不本地化，AGENTS.md §2.1）。
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
