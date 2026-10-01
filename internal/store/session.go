package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// SessionStore 封装 sessions 表。会话是服务端状态：登出、改密码、禁用都靠它作废（DESIGN.md §4.3、§11）。
type SessionStore struct {
	db *gorm.DB
}

// NewSessionStore 构造会话存储。
func NewSessionStore(db *gorm.DB) *SessionStore { return &SessionStore{db: db} }

// Create 写入一条会话记录。
func (s *SessionStore) Create(ctx context.Context, sess *Session) error {
	if err := s.db.WithContext(ctx).Create(sess).Error; err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// ByID 取会话；不存在时返回 gorm.ErrRecordNotFound。
func (s *SessionStore) ByID(ctx context.Context, id string) (*Session, error) {
	var sess Session
	if err := s.db.WithContext(ctx).First(&sess, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &sess, nil
}

// Revoke 作废单个会话（登出）。已作废的再次调用幂等。
func (s *SessionStore) Revoke(ctx context.Context, id string, at time.Time) error {
	if err := s.db.WithContext(ctx).Model(&Session{}).
		Where("id = ? AND revoked_at IS NULL", id).
		Update("revoked_at", at).Error; err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

// RevokeAllForUser 作废某用户的全部有效会话（改密码、禁用、强制下线）。
func (s *SessionStore) RevokeAllForUser(ctx context.Context, userID uint64, at time.Time) error {
	if err := s.db.WithContext(ctx).Model(&Session{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", at).Error; err != nil {
		return fmt.Errorf("revoke user sessions: %w", err)
	}
	return nil
}

// Touch 更新会话的最后活跃时间；best-effort，失败不应打断请求。
func (s *SessionStore) Touch(ctx context.Context, id string, at time.Time) error {
	return s.db.WithContext(ctx).Model(&Session{}).Where("id = ?", id).
		Update("last_seen_at", at).Error
}

// DeleteExpired 清理过期会话行；由后台任务调用，测试用它验证过期回收。
func (s *SessionStore) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	res := s.db.WithContext(ctx).Where("expires_at <= ?", before).Delete(&Session{})
	if res.Error != nil {
		return 0, fmt.Errorf("delete expired sessions: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// IsNotFound 是 gorm.ErrRecordNotFound 的窄包装，让上层不必直接依赖 GORM（消费者侧最小接口）。
func IsNotFound(err error) bool { return errors.Is(err, gorm.ErrRecordNotFound) }
