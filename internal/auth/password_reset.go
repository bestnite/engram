package auth

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// 本文件是 M1-19（A 类事务安全邮件）在 auth 层的补充：凭一次性重置令牌设置新密码。
//
// 与 ChangePassword 的区别有两点：一是不需要旧密码，安全性由调用方保证——它必须先消费
// 一枚有效、未过期、用过即废的重置令牌；二是重置属于「口令丢失后由外部证明身份」的路径，
// 因此除会话外还要全数吊销该用户的 API Key。这里仍复用同一条密码策略与
// 同一个哈希器，不定义第二套规则。

// SetPasswordFromResetTx 在调用方给定的事务里为已证明控制邮箱的用户设置新密码，
// 并在同一事务里作废其全部会话与全部 API Key。
//
// 必须使用调用方的事务句柄：本方法内部会复用 s.users/s.sessions/s.keys 三个 store，
// 它们都只是 GORM 句柄的包装，只有传 tx 才能保证三条写入共享同一事务；另开连接在 SQLite
// 单写连接上会锁冲突，失败时也会留下半吊销。
func (s *AccountService) SetPasswordFromResetTx(ctx context.Context, tx *gorm.DB, userID uint64, newPassword string) error {
	if err := ValidatePasswordPolicy(newPassword); err != nil {
		return err
	}
	hash, err := s.hasher.Hash(newPassword)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	if err := s.users.SetPasswordHashTx(ctx, tx, userID, hash); err != nil {
		return err
	}
	if err := s.sessions.RevokeAllForUserTx(ctx, tx, userID, s.now()); err != nil {
		return err
	}
	return s.keys.RevokeAllForUserTx(ctx, tx, userID, s.now())
}
