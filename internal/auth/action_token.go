package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是 M1-19（A 类事务安全邮件）的一次性令牌服务：签发与消费。
//
// 安全约束（DESIGN.md §4.7、§4.6）：
//   - 明文随机、URL 安全，只出现在邮件链接里，绝不入库、绝不进日志；
//   - 库里只存 sha256 摘要（store.HashActionToken）；
//   - 一次性：消费走条件更新 `WHERE used_at IS NULL`（store.ActionTokenStore.Consume）；
//   - 有明确过期时间。
//
// 令牌的用途常量与过期时长集中在这里，调用方不自己编时长。

const (
	// PasswordResetTTL 是密码重置链接的有效期；窗口短，降低链接泄漏后的滥用面。
	PasswordResetTTL = 1 * time.Hour
	// EmailVerifyTTL 是邮箱验证链接的有效期。
	EmailVerifyTTL = 24 * time.Hour
	// EmailChangeTTL 是改邮箱确认链接的有效期。
	EmailChangeTTL = 24 * time.Hour
	// UnsubscribeTTL 是一键退订链接的有效期（M1-22）。
	// 退订链接会长期留在历史邮件里，用户可能几个月后才想起来点，因此给足有效期；
	// 但它仍是「一次性 + 有有效期」的令牌（复用 action_tokens 的消费语义）。
	UnsubscribeTTL = 90 * 24 * time.Hour
)

// ActionTokenService 签发与消费一次性动作令牌。
type ActionTokenService struct {
	tokens *store.ActionTokenStore
	now    func() time.Time
	rand   func([]byte) error
}

// NewActionTokenService 构造令牌服务；store 必填。
func NewActionTokenService(tokens *store.ActionTokenStore) (*ActionTokenService, error) {
	if tokens == nil {
		return nil, errors.New("auth: action token store is required")
	}
	return &ActionTokenService{
		tokens: tokens,
		now:    func() time.Time { return time.Now().UTC() },
		rand:   func(b []byte) error { _, err := rand.Read(b); return err },
	}, nil
}

// Issue 生成一枚随机令牌，把摘要落库，返回明文（仅供拼进邮件链接）。
// payload 是用途相关的附加数据（改邮箱确认传新邮箱），其余用途传空串。
func (s *ActionTokenService) Issue(ctx context.Context, userID uint64, purpose, payload string, ttl time.Duration) (string, error) {
	buf := make([]byte, 32)
	if err := s.rand(buf); err != nil {
		return "", fmt.Errorf("generate action token: %w", err)
	}
	plaintext := base64.RawURLEncoding.EncodeToString(buf)
	now := s.now()
	row := &store.ActionToken{
		Purpose:   purpose,
		UserID:    userID,
		TokenHash: store.HashActionToken(plaintext),
		Payload:   payload,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	if err := s.tokens.Create(ctx, row); err != nil {
		return "", err
	}
	return plaintext, nil
}

// Consume 校验并一次性消费令牌，返回被消费的行（含 UserID 与 Payload）。
// 错误是 store 的稳定哨兵：ErrActionTokenNotFound / Used / Expired。
func (s *ActionTokenService) Consume(ctx context.Context, purpose, plaintext string) (*store.ActionToken, error) {
	return s.tokens.Consume(ctx, purpose, plaintext, s.now())
}
