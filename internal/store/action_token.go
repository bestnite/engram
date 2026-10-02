package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// 本文件是 M1-19（A 类事务安全邮件）的一次性动作令牌持久化层。
//
// 为什么用一张通用表而不是每种令牌各建一张：密码重置、邮箱验证、改邮箱确认的
// 生命周期完全相同——随机明文只出现在邮件链接里，库里只存摘要，用过一次即作废，
// 且都有过期时间。共用一张表让「只存摘要 + 条件更新 WHERE used_at IS NULL」这条
// 安全约束只有一处实现，不会因新增一种令牌而漂移。
//
// 明文绝不入库、绝不进日志：表里只有 sha256 摘要（token_hash）。生成与投递链接
// 由 internal/auth 的 ActionTokenService 负责。

// 动作令牌用途；取值英文且稳定，落库后不再更改（AGENTS.md §2.1）。
const (
	// ActionTokenPasswordReset 是密码重置令牌。
	ActionTokenPasswordReset = "password_reset"
	// ActionTokenEmailVerify 是邮箱验证令牌（注册后确认邮箱）。
	ActionTokenEmailVerify = "email_verification"
	// ActionTokenEmailChange 是改邮箱确认令牌；Payload 保存待确认的新邮箱。
	ActionTokenEmailChange = "email_change"
)

// 令牌消费的稳定错误值；transport 层据此映射到稳定英文 code。
var (
	// ErrActionTokenNotFound 表示摘要不存在。
	ErrActionTokenNotFound = errors.New("action token not found")
	// ErrActionTokenUsed 表示令牌已被使用过（一次性）。
	ErrActionTokenUsed = errors.New("action token already used")
	// ErrActionTokenExpired 表示令牌已过期。
	ErrActionTokenExpired = errors.New("action token expired")
)

// M1-19 的审计动作；集中在本文件，避免改动其它里程碑的 audit_*.go 热点。
const (
	// ActionUserPasswordResetComplete 是用户凭邮件链接完成密码重置。
	ActionUserPasswordResetComplete = "user.password_reset_complete"
	// ActionUserEmailVerifyRequest 是请求/重发邮箱验证邮件。
	ActionUserEmailVerifyRequest = "user.email_verify_request"
	// ActionUserEmailVerified 是邮箱验证通过。
	ActionUserEmailVerified = "user.email_verified"
	// ActionUserEmailChangeRequest 是请求改邮箱（已向新邮箱发确认信）。
	ActionUserEmailChangeRequest = "user.email_change_request"
	// ActionUserEmailChanged 是改邮箱确认通过、邮箱已更新。
	ActionUserEmailChanged = "user.email_changed"
)

// ActionToken 是一次性动作令牌。库里只保存 token_hash（sha256 摘要），
// 明文仅出现在发给用户的邮件链接中。
type ActionToken struct {
	ID uint64 `gorm:"primaryKey" json:"id"`
	// Purpose 区分令牌用途（password_reset / email_verification / email_change）。
	Purpose string `gorm:"not null;index:idx_action_tokens_lookup,priority:1" json:"purpose"`
	// UserID 是令牌归属的用户。
	UserID uint64 `gorm:"not null;index" json:"user_id"`
	// TokenHash 是明文令牌的 sha256 十六进制摘要；唯一索引防止摘要碰撞。
	TokenHash string `gorm:"column:token_hash;not null;uniqueIndex" json:"-"`
	// Payload 是用途相关的附加数据；改邮箱确认存新邮箱，其余为空。
	Payload string `gorm:"not null" json:"payload,omitempty"`
	// CreatedAt 是签发时间。
	CreatedAt time.Time `gorm:"not null" json:"created_at"`
	// ExpiresAt 是过期时间；到点后即便未使用也不再接受。
	ExpiresAt time.Time `gorm:"not null" json:"expires_at"`
	// UsedAt 非空表示已消费；条件更新据此保证一次性。
	UsedAt *time.Time `json:"used_at,omitempty"`
}

// TableName 固定表名，避免复数化规则在不同 GORM 版本下漂移。
func (ActionToken) TableName() string { return "action_tokens" }

// HashActionToken 返回令牌明文的 sha256 十六进制摘要。这是唯一的哈希实现，
// 签发与校验都必须用它，避免两处算法分歧。
func HashActionToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// ActionTokenStore 封装 action_tokens 表的 GORM 访问。
type ActionTokenStore struct {
	db *gorm.DB
}

// NewActionTokenStore 构造动作令牌存储。
func NewActionTokenStore(db *gorm.DB) *ActionTokenStore { return &ActionTokenStore{db: db} }

// Create 写入一条新令牌；只接受摘要，调用方不得把明文传进来。
func (s *ActionTokenStore) Create(ctx context.Context, tok *ActionToken) error {
	if tok == nil {
		return errors.New("store: action token is required")
	}
	if strings.TrimSpace(tok.TokenHash) == "" {
		return errors.New("store: action token hash is required")
	}
	if err := s.db.WithContext(ctx).Create(tok).Error; err != nil {
		return fmt.Errorf("create action token: %w", err)
	}
	return nil
}

// Consume 校验并消费一枚令牌：找到行后按 used_at / expires_at 判定，
// 再用条件更新 `WHERE id = ? AND used_at IS NULL` 原子地标记已用，
// 从而在并发下也保证「一码一用」。成功时返回被消费的行。
func (s *ActionTokenStore) Consume(ctx context.Context, purpose, plaintext string, now time.Time) (*ActionToken, error) {
	purpose = strings.TrimSpace(purpose)
	plaintext = strings.TrimSpace(plaintext)
	if purpose == "" || plaintext == "" {
		return nil, ErrActionTokenNotFound
	}
	hash := HashActionToken(plaintext)
	var tok ActionToken
	err := s.db.WithContext(ctx).Where("purpose = ? AND token_hash = ?", purpose, hash).First(&tok).Error
	if err != nil {
		if IsNotFound(err) {
			return nil, ErrActionTokenNotFound
		}
		return nil, fmt.Errorf("load action token: %w", err)
	}
	if tok.UsedAt != nil {
		return nil, ErrActionTokenUsed
	}
	now = now.UTC()
	if !tok.ExpiresAt.After(now) {
		return nil, ErrActionTokenExpired
	}
	res := s.db.WithContext(ctx).Model(&ActionToken{}).
		Where("id = ? AND used_at IS NULL", tok.ID).
		Update("used_at", now)
	if res.Error != nil {
		return nil, fmt.Errorf("consume action token: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		// 另一个并发请求抢先消费了它。
		return nil, ErrActionTokenUsed
	}
	tok.UsedAt = &now
	return &tok, nil
}
