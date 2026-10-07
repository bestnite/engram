package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// 管理面板的用户管理所需的账号操作（ROADMAP.md M6-2）。
// 放在独立文件，避免改动 account.go（其它里程碑的单写者热点）。

// tempPasswordAlphabet 是临时口令的字符集：去掉了易混淆的 0/O/1/l/I。
const tempPasswordAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789"

// tempPasswordLength 是临时口令长度；大于 MinPasswordLength，随机字符不会命中弱密码词表。
const tempPasswordLength = 16

// GenerateTempPassword 生成一个满足密码策略的随机临时口令。
func GenerateTempPassword() (string, error) {
	buf := make([]byte, tempPasswordLength)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate temp password: %w", err)
	}
	out := make([]byte, tempPasswordLength)
	for i, b := range buf {
		out[i] = tempPasswordAlphabet[int(b)%len(tempPasswordAlphabet)]
	}
	return string(out), nil
}

// ResetPasswordTx 由管理员为指定用户生成临时口令：在调用方给定的事务里写入新哈希，
// 并在同一事务里作废其全部会话与全部 API Key。
// 返回的明文只在此刻交给调用方展示一次，绝不写日志或落库。
//
// 必须使用调用方的事务句柄：存在性校验与三条写入都要走 tx，否则在 SQLite 上另开连接会锁冲突，
// 且任一写入失败时会留下半作废。
func (s *AccountService) ResetPasswordTx(ctx context.Context, tx *gorm.DB, userID uint64) (string, error) {
	if _, err := s.users.ByIDTx(ctx, tx, userID); err != nil {
		return "", fmt.Errorf("load user: %w", err)
	}
	temp, err := GenerateTempPassword()
	if err != nil {
		return "", err
	}
	if err := ValidatePasswordPolicy(temp); err != nil {
		// 理论上不会发生；真发生说明字符集/长度被改坏了，必须显式报错而不是写出弱口令。
		return "", fmt.Errorf("temp password failed policy: %w", err)
	}
	hash, err := s.hasher.Hash(temp)
	if err != nil {
		return "", err
	}
	if err := s.users.SetPasswordHashTx(ctx, tx, userID, hash); err != nil {
		return "", err
	}
	if err := s.sessions.RevokeAllForUserTx(ctx, tx, userID, s.now()); err != nil {
		return temp, err
	}
	if err := s.keys.RevokeAllForUserTx(ctx, tx, userID, s.now()); err != nil {
		return temp, err
	}
	return temp, nil
}

// ErrLastAdmin 表示该操作会让系统一个可登录管理员都不剩；handler 据此拒绝并给稳定错误码。
var ErrLastAdmin = errors.New("cannot remove the last active admin")

// ForceLogout 作废指定用户当前全部会话（强制下线），不改密码、不改状态。
func (s *AccountService) ForceLogout(ctx context.Context, userID uint64) error {
	if _, err := s.users.ByID(ctx, userID); err != nil {
		return fmt.Errorf("load user: %w", err)
	}
	return s.sessions.RevokeAllForUser(ctx, userID, s.now())
}
