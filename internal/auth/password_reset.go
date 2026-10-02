package auth

import (
	"context"
	"fmt"
)

// 本文件是 M1-19（A 类事务安全邮件）在 auth 层的补充：凭一次性重置令牌设置新密码。
//
// 与 ChangePassword 的区别只有一点：不需要旧密码。安全性由调用方保证——它必须先消费
// 一枚有效、未过期、用过即废的重置令牌，才允许走到这里。这里仍复用同一条密码策略与
// 同一个哈希器，不定义第二套规则。

// SetPasswordFromReset 为已证明控制邮箱的用户设置新密码，并作废其全部会话（DESIGN.md §11）。
func (s *AccountService) SetPasswordFromReset(ctx context.Context, userID uint64, newPassword string) error {
	if err := ValidatePasswordPolicy(newPassword); err != nil {
		return err
	}
	hash, err := s.hasher.Hash(newPassword)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	if err := s.users.SetPasswordHash(ctx, userID, hash); err != nil {
		return err
	}
	return s.sessions.RevokeAllForUser(ctx, userID, s.now())
}
