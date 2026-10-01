package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// 审计动作常量集中定义（AGENTS.md §5 M1-10）。值一律英文点分、保持稳定：
// action 是审计检索的键，调用点写裸字符串会让历史数据无法按动作聚合。
// 新增写操作时在这里加常量，不要另起一套命名。
const (
	ActionUserCreate         = "user.create"
	ActionUserLoginSucceeded = "user.login_succeeded"
	ActionUserLoginFailed    = "user.login_failed"
	ActionUserLogout         = "user.logout"
)

// AuditEntry 是一次审计写入的入参。
// UserID / APIKeyID / TargetID 可空（nil 落库为 NULL）：未登录的失败登录没有 user_id，
// 系统自动动作没有 api_key_id —— 用 0 冒充会污染“按用户检索”的结果。
type AuditEntry struct {
	UserID   *uint64
	APIKeyID *uint64
	Action   string
	// TargetType 为空表示该动作没有目标对象；此时 TargetID 被忽略。
	TargetType string
	TargetID   *uint64
	// Detail 会被序列化成 JSON 存进 detail_json；nil 时该列留空。
	Detail any
}

// AuditStore 封装 audit_log 表；Write 是全部写操作留痕的唯一入口（M1-10）。
type AuditStore struct {
	db *gorm.DB
}

// NewAuditStore 构造审计存储。
func NewAuditStore(db *gorm.DB) *AuditStore { return &AuditStore{db: db} }

// Write 写一行审计。
// action 为空直接拒绝：没有动作名的行无法被检索，属于调用方 bug，不能静默落库。
func (s *AuditStore) Write(ctx context.Context, e AuditEntry) error {
	if e.Action == "" {
		return errors.New("audit action must not be empty")
	}
	row := AuditLog{
		UserID:    e.UserID,
		APIKeyID:  e.APIKeyID,
		Action:    e.Action,
		CreatedAt: time.Now().UTC(),
	}
	if e.TargetType != "" {
		targetType := e.TargetType
		row.TargetType = &targetType
		row.TargetID = e.TargetID
	}
	if e.Detail != nil {
		raw, err := json.Marshal(e.Detail)
		if err != nil {
			return fmt.Errorf("marshal audit detail: %w", err)
		}
		detail := string(raw)
		row.DetailJSON = &detail
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("write audit log: %w", err)
	}
	return nil
}

// List 按写入顺序倒序返回最近 limit 条审计；供管理面板（M6）与测试查看。
func (s *AuditStore) List(ctx context.Context, limit int) ([]AuditLog, error) {
	if limit <= 0 {
		limit = 50
	}
	var rows []AuditLog
	if err := s.db.WithContext(ctx).Order("id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list audit log: %w", err)
	}
	return rows, nil
}

// CountByAction 返回某动作的审计行数；测试用它断言“每次变更恰好一行”。
func (s *AuditStore) CountByAction(ctx context.Context, action string) (int64, error) {
	var n int64
	if err := s.db.WithContext(ctx).Model(&AuditLog{}).
		Where("action = ?", action).Count(&n).Error; err != nil {
		return 0, fmt.Errorf("count audit log: %w", err)
	}
	return n, nil
}

// Ptr 返回 v 的地址，用于填充可空字段（user_id / api_key_id / target_id）。
func Ptr[T any](v T) *T { return &v }
