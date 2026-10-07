package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// API Key 的五档 scope；本项目的权限模型就是这五档，不再往细里做。
// 取值用英文常量：scope 字符串会写进 api_keys.scopes 并被 REST/MCP 复用，必须稳定。
const (
	ScopeRead   = "read"   // 读卡组、卡片、统计
	ScopeWrite  = "write"  // 建/改/删卡片与卡组
	ScopeReview = "review" // 取到期卡、提交评分
	// ScopeKeys 管理自己的 API Key（列出/新建/撤销）；网页端自管理不受它约束。
	// 它只覆盖“自己的”key——管理他人 key 属于 admin。
	ScopeKeys  = "keys"
	ScopeAdmin = "admin" // 管理全部 API Key、系统设置、用户；蕴含其余四档
)

const (
	// APIKeyPlaintextPrefix 是明文 key 的前缀（fcard_<base64url>）。
	APIKeyPlaintextPrefix = "fcard_"
	// apiKeyDisplayChars 是展示前缀在 "fcard_" 之后保留的明文字符数，如 fcard_ab12cd34。
	apiKeyDisplayChars = 8
	// apiKeyRandomBytes 是生成明文用的随机字节数（32 字节）。
	apiKeyRandomBytes = 32
	// defaultAPIKeyScope 是新 key 不给 scopes 时的默认值（默认只给 read）。
	defaultAPIKeyScope = ScopeRead
)

// apiKeyScopeOrder 是 scope 的规范顺序；归一化时按它排序，让同一组 scope 只有一种存储形式。
var apiKeyScopeOrder = []string{ScopeRead, ScopeWrite, ScopeReview, ScopeKeys, ScopeAdmin}

var (
	// ErrAPIKeyNameRequired 表示创建 key 时 name 为空。
	ErrAPIKeyNameRequired = errors.New("api key name is required")
	// ErrAPIKeyUserRequired 表示创建 key 时未给出 user_id。
	ErrAPIKeyUserRequired = errors.New("api key user is required")
	// ErrAPIKeyInvalidScope 表示 scopes 里出现了四档之外的取值。
	ErrAPIKeyInvalidScope = errors.New("invalid api key scope")
	// ErrAPIKeyNotFound 表示 key 不存在（按明文找不到，或不属于调用者）。
	ErrAPIKeyNotFound = errors.New("api key not found")
	// ErrAPIKeyRevoked 表示 key 已被撤销（revoked_at 非空）。
	ErrAPIKeyRevoked = errors.New("api key revoked")
	// ErrAPIKeyExpired 表示 key 已过 expires_at。
	ErrAPIKeyExpired = errors.New("api key expired")
	// ErrAPIKeyPlaintextRequired 表示校验时明文字符串为空。
	ErrAPIKeyPlaintextRequired = errors.New("api key plaintext is required")
)

// IsValidScope 判断取值是否是五档之一。
func IsValidScope(scope string) bool {
	for _, s := range apiKeyScopeOrder {
		if s == scope {
			return true
		}
	}
	return false
}

// ParseScopes 把逗号分隔的 scopes 列拆成切片，忽略空白项。
func ParseScopes(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// NormalizeScopes 校验并归一化一组 scope：去重、按规范顺序排序、逗号连接。
// 空输入落到默认的 read；出现五档之外的取值返回 ErrAPIKeyInvalidScope。
func NormalizeScopes(scopes []string) (string, error) {
	if len(scopes) == 0 {
		return defaultAPIKeyScope, nil
	}
	seen := make(map[string]bool, len(scopes))
	for _, s := range scopes {
		s = strings.TrimSpace(s)
		if !IsValidScope(s) {
			return "", fmt.Errorf("%w: %q", ErrAPIKeyInvalidScope, s)
		}
		seen[s] = true
	}
	ordered := make([]string, 0, len(seen))
	for _, s := range apiKeyScopeOrder {
		if seen[s] {
			ordered = append(ordered, s)
		}
	}
	return strings.Join(ordered, ","), nil
}

// ScopesIncludeAdmin 判断一组 scope 是否试图授予 admin（按请求原值匹配，未归一化）。
// 取值合法性由 NormalizeScopes 负责，这里只回答「有没有 admin」。Web 表单与 REST
// 建 key 两条入口共用同一判定（admin scope 只能发给管理员账号）。
func ScopesIncludeAdmin(scopes []string) bool {
	for _, s := range scopes {
		if strings.TrimSpace(s) == ScopeAdmin {
			return true
		}
	}
	return false
}

// HasScope 判断 scopes 字符串是否覆盖 want。
// admin 等价于管理员权限，因此它蕴含其余四档（含 keys）；反向不成立，
// 其余各档之间互不蕴含。
func HasScope(scopes, want string) bool {
	for _, s := range ParseScopes(scopes) {
		if s == want || s == ScopeAdmin {
			return true
		}
	}
	return false
}

// HasScope 是 HasScope(scopes, want) 的方法形式，便于直接从 *APIKey 判断。
func (k *APIKey) HasScope(want string) bool {
	if k == nil {
		return false
	}
	return HasScope(k.Scopes, want)
}

// HashAPIKey 返回明文的 sha256 十六进制摘要；库里只存这个值。
func HashAPIKey(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// displayPrefix 截出展示用前缀（如 fcard_ab12cd34）；明文不足时原样返回。
func displayPrefix(plaintext string) string {
	keep := len(APIKeyPlaintextPrefix) + apiKeyDisplayChars
	if len(plaintext) <= keep {
		return plaintext
	}
	return plaintext[:keep]
}

// newAPIKeyPlaintext 生成 fcard_<base64url(32 随机字节)> 的明文。
func newAPIKeyPlaintext() (string, error) {
	buf := make([]byte, apiKeyRandomBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate api key: %w", err)
	}
	return APIKeyPlaintextPrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

// ValidateAPIKey 是不依赖数据库的可用性判定，便于表驱动测试直接覆盖。
func ValidateAPIKey(k *APIKey, now time.Time) error {
	if k == nil {
		return ErrAPIKeyNotFound
	}
	if k.RevokedAt != nil {
		return fmt.Errorf("%w: api key %d", ErrAPIKeyRevoked, k.ID)
	}
	if k.ExpiresAt != nil && !k.ExpiresAt.After(now) {
		return fmt.Errorf("%w: api key %d", ErrAPIKeyExpired, k.ID)
	}
	return nil
}

// APIKeyStore 封装 api_keys 表；明文只在 Create 的返回值里出现一次，之后库里只剩 sha256。
type APIKeyStore struct {
	db *gorm.DB
}

// NewAPIKeyStore 构造 API Key 存储。
func NewAPIKeyStore(db *gorm.DB) *APIKeyStore { return &APIKeyStore{db: db} }

// CreateAPIKeyParams 是创建 key 的入参；Scopes 为空时落到默认 read，ExpiresAt 为 nil 表示不过期。
type CreateAPIKeyParams struct {
	UserID    uint64
	Name      string
	Scopes    []string
	ExpiresAt *time.Time
}

// CreatedAPIKey 是创建结果：Plaintext 只在这里返回一次，调用方必须立即展示给用户、绝不落库。
type CreatedAPIKey struct {
	Key       *APIKey
	Plaintext string
}

// Create 生成一个新 key 并落库。库里只写 sha256 与展示前缀，明文通过返回值交给调用方。
func (s *APIKeyStore) Create(ctx context.Context, p CreateAPIKeyParams) (*CreatedAPIKey, error) {
	if p.UserID == 0 {
		return nil, ErrAPIKeyUserRequired
	}
	if strings.TrimSpace(p.Name) == "" {
		return nil, ErrAPIKeyNameRequired
	}
	scopes, err := NormalizeScopes(p.Scopes)
	if err != nil {
		return nil, err
	}
	plaintext, err := newAPIKeyPlaintext()
	if err != nil {
		return nil, err
	}
	key := APIKey{
		UserID:    p.UserID,
		Name:      p.Name,
		Prefix:    displayPrefix(plaintext),
		KeyHash:   HashAPIKey(plaintext),
		Scopes:    scopes,
		ExpiresAt: p.ExpiresAt,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.db.WithContext(ctx).Create(&key).Error; err != nil {
		return nil, fmt.Errorf("create api key: %w", err)
	}
	return &CreatedAPIKey{Key: &key, Plaintext: plaintext}, nil
}

// ByID 按主键取 key；不存在时返回 ErrAPIKeyNotFound。
func (s *APIKeyStore) ByID(ctx context.Context, id uint64) (*APIKey, error) {
	var k APIKey
	if err := s.db.WithContext(ctx).First(&k, "id = ?", id).Error; err != nil {
		if IsNotFound(err) {
			return nil, ErrAPIKeyNotFound
		}
		return nil, fmt.Errorf("load api key: %w", err)
	}
	return &k, nil
}

// ListByUser 列出某用户的全部 key（含已撤销），按创建时间倒序；供个人设置页与管理面板展示。
func (s *APIKeyStore) ListByUser(ctx context.Context, userID uint64) ([]APIKey, error) {
	var keys []APIKey
	if err := s.db.WithContext(ctx).Where("user_id = ?", userID).
		Order("created_at DESC, id DESC").Find(&keys).Error; err != nil {
		return nil, fmt.Errorf("list api keys: %w", err)
	}
	return keys, nil
}

// Authenticate 用明文验证一个 key：命中、未过期、未撤销时返回该行并刷新 last_used_at。
// 已撤销 / 已过期的 key 立即返回 ErrAPIKeyRevoked / ErrAPIKeyExpired，绝不放行。
func (s *APIKeyStore) Authenticate(ctx context.Context, plaintext string, now time.Time) (*APIKey, error) {
	plaintext = strings.TrimSpace(plaintext)
	if plaintext == "" {
		return nil, ErrAPIKeyPlaintextRequired
	}
	var k APIKey
	if err := s.db.WithContext(ctx).First(&k, "key_hash = ?", HashAPIKey(plaintext)).Error; err != nil {
		if IsNotFound(err) {
			return nil, ErrAPIKeyNotFound
		}
		return nil, fmt.Errorf("load api key by hash: %w", err)
	}
	if err := ValidateAPIKey(&k, now); err != nil {
		return nil, err
	}
	// last_used_at 的刷新是尽力而为：写失败不影响本次鉴权结论，但不能掩盖它。
	if err := s.TouchLastUsed(ctx, k.ID, now); err == nil {
		at := now.UTC()
		k.LastUsedAt = &at
	}
	return &k, nil
}

// TouchLastUsed 更新 last_used_at；用于每次成功的 key 调用。
func (s *APIKeyStore) TouchLastUsed(ctx context.Context, id uint64, at time.Time) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if err := s.db.WithContext(ctx).Model(&APIKey{}).Where("id = ?", id).
		Update("last_used_at", at.UTC()).Error; err != nil {
		return fmt.Errorf("touch api key %d: %w", id, err)
	}
	return nil
}

// Revoke 撤销一个属于 actorUserID 的 key（写 revoked_at），撤销即时生效。
// key 不存在或不属于该用户都返回 ErrAPIKeyNotFound，避免泄露他人 key 的存在性。
// 重复撤销是幂等的（仍命中同一行）。
func (s *APIKeyStore) Revoke(ctx context.Context, actorUserID, keyID uint64, at time.Time) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	res := s.db.WithContext(ctx).Model(&APIKey{}).
		Where("id = ? AND user_id = ?", keyID, actorUserID).
		Update("revoked_at", at.UTC())
	if res.Error != nil {
		return fmt.Errorf("revoke api key %d: %w", keyID, res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrAPIKeyNotFound
	}
	return nil
}

// API Key 的展示状态（M6-9）。取值英文、稳定：页面文案由语言包按状态映射。
const (
	APIKeyStateActive  = "active"
	APIKeyStateRevoked = "revoked"
	APIKeyStateExpired = "expired"
)

// APIKeyState 返回一把 key 的可用状态，供管理面板总览展示。
// 判定顺序先撤销后过期：已撤销的 key 不再关心是否过期，状态必须唯一。
func APIKeyState(k *APIKey, now time.Time) string {
	switch {
	case k == nil:
		return ""
	case k.RevokedAt != nil:
		return APIKeyStateRevoked
	case k.ExpiresAt != nil && !k.ExpiresAt.After(now):
		return APIKeyStateExpired
	default:
		return APIKeyStateActive
	}
}

// ListAll 分页列出全部用户的 key（含已撤销 / 已过期），按创建时间倒序，返回总行数。
// 仅供管理面板总览使用；返回值是元信息，库里没有明文，也没有可回显的明文列。
func (s *APIKeyStore) ListAll(ctx context.Context, limit, offset int) ([]APIKey, int64, error) {
	var total int64
	if err := s.db.WithContext(ctx).Model(&APIKey{}).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count api keys: %w", err)
	}
	var keys []APIKey
	if err := s.db.WithContext(ctx).Order("created_at DESC, id DESC").
		Limit(limit).Offset(offset).Find(&keys).Error; err != nil {
		return nil, 0, fmt.Errorf("list api keys: %w", err)
	}
	return keys, total, nil
}

// RevokeByID 按主键撤销任意用户的 key，供管理面板总览使用（撤销他人 key 是管理员动作）。
// 个人设置页用 Revoke（限定本人）；这里不校验归属，调用方负责鉴权。
func (s *APIKeyStore) RevokeByID(ctx context.Context, keyID uint64, at time.Time) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	res := s.db.WithContext(ctx).Model(&APIKey{}).Where("id = ?", keyID).
		Update("revoked_at", at.UTC())
	if res.Error != nil {
		return fmt.Errorf("revoke api key %d: %w", keyID, res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrAPIKeyNotFound
	}
	return nil
}

// RevokeAllForUser 作废某用户的全部有效 key（口令重置时调用）。
// 用户主动改密不走这里：key 有独立于口令的生命周期。
func (s *APIKeyStore) RevokeAllForUser(ctx context.Context, userID uint64, at time.Time) error {
	return s.RevokeAllForUserTx(ctx, s.db, userID, at)
}

// RevokeAllForUserTx 在调用方给定的事务里批量吊销某用户的全部有效 key。
// 只命中 revoked_at IS NULL 的行，重复调用幂等（与 sessions 的批量作废同构）；
// 必须与密码写入共享同一事务句柄，否则失败会半吊销。
func (s *APIKeyStore) RevokeAllForUserTx(ctx context.Context, tx *gorm.DB, userID uint64, at time.Time) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if err := tx.WithContext(ctx).Model(&APIKey{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", at.UTC()).Error; err != nil {
		return fmt.Errorf("revoke user api keys: %w", err)
	}
	return nil
}
