package store

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// 邮件 outbox（AGENTS.md M1-17）。
//
// 投递设计：入站只写这张队列表，后台 worker 带重试与退避投递，绝不在请求路径里同步发信。
// 发信失败不得让触发它的操作失败，因此 Enqueue 只落一行 pending，投递结果由 worker 写回。
//
// 状态机：pending -> sent | failed。
//   - pending：等待投递；next_attempt_at 到了才会被 worker 取走（退避靠它实现）。
//   - sent：投递成功，记录 sent_at。
//   - failed：超过最大尝试次数，保留 last_error 供管理面板显示最后一次错误。
//
// 退避与"最后一次错误可见"都落在这一行上，管理面板不需要另建报表。
const (
	// OutboxStatusPending 表示等待投递。
	OutboxStatusPending = "pending"
	// OutboxStatusSent 表示投递成功。
	OutboxStatusSent = "sent"
	// OutboxStatusFailed 表示重试耗尽后永久失败。
	OutboxStatusFailed = "failed"
)

// OutboxMessage 是一封待投递（或已投递）的邮件。
//
// 表名固定为 mail_outbox，避免复数化规则漂移。正文与自定义头都以 TEXT 存储，
// 双库兼容（不用 jsonb/array）。HeadersJSON 是 map[string]string 的 JSON。
type OutboxMessage struct {
	ID uint64 `gorm:"primaryKey" json:"id"`
	// To 是收件地址（只用登录邮箱）。
	To string `gorm:"not null" json:"to"`
	// Type 是自由字符串邮件类型；目录由 M1-18 定义，这里只原样存。
	Type string `gorm:"not null" json:"type"`
	// Subject 是主题；用户可见文案在入站时已本地化，这里只存结果。
	Subject  string `gorm:"not null" json:"subject"`
	TextBody string `gorm:"column:text_body;not null" json:"text_body"`
	HTMLBody string `gorm:"column:html_body;not null" json:"html_body"`
	// HeadersJSON 是附加邮件头的 JSON 编码（例如 List-Unsubscribe）。
	HeadersJSON string `gorm:"column:headers_json;not null" json:"headers_json"`
	// Status 是 pending | sent | failed；与 idx_outbox_due 组合成"到期待投递"查询。
	Status string `gorm:"not null;index:idx_outbox_due,priority:1" json:"status"`
	// Attempts 是已尝试投递次数。
	Attempts int `gorm:"not null;default:0" json:"attempts"`
	// NextAttemptAt 是下次可投递时间（UTC）；退避把失败的重排到未来。
	NextAttemptAt time.Time `gorm:"not null;index:idx_outbox_due,priority:2" json:"next_attempt_at"`
	// LastError 是最近一次投递失败的服务端错误文本；成功或未失败时为空。
	LastError *string    `gorm:"column:last_error" json:"last_error,omitempty"`
	CreatedAt time.Time  `gorm:"not null" json:"created_at"`
	UpdatedAt time.Time  `gorm:"not null" json:"updated_at"`
	SentAt    *time.Time `json:"sent_at,omitempty"`
}

// TableName 固定表名，避免复数化规则在不同 GORM 版本下漂移。
func (OutboxMessage) TableName() string { return "mail_outbox" }

// EnqueueOutboxMessage 写入一封待投递邮件：强制 pending / attempts=0，并设好创建与首投时间。
// 这是唯一写入口，保证状态字段不会被调用方写歪。
func EnqueueOutboxMessage(ctx context.Context, db *gorm.DB, row *OutboxMessage, now time.Time) error {
	if row == nil {
		return fmt.Errorf("store: outbox message is required")
	}
	row.Status = OutboxStatusPending
	row.Attempts = 0
	row.LastError = nil
	row.SentAt = nil
	row.CreatedAt = now.UTC()
	row.UpdatedAt = now.UTC()
	row.NextAttemptAt = now.UTC()
	if err := db.WithContext(ctx).Create(row).Error; err != nil {
		return fmt.Errorf("enqueue outbox message: %w", err)
	}
	return nil
}

// DueOutboxMessages 返回 next_attempt_at 已到（<= now）的 pending 邮件，按到期时间升序。
// limit <= 0 时不限制（调用方通常传一个批量上限）。
func DueOutboxMessages(ctx context.Context, db *gorm.DB, now time.Time, limit int) ([]OutboxMessage, error) {
	q := db.WithContext(ctx).
		Where("status = ? AND next_attempt_at <= ?", OutboxStatusPending, now.UTC()).
		Order("next_attempt_at asc, id asc")
	if limit > 0 {
		q = q.Limit(limit)
	}
	var rows []OutboxMessage
	if err := q.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load due outbox messages: %w", err)
	}
	return rows, nil
}

// MarkOutboxSent 把一封邮件标记为投递成功。
func MarkOutboxSent(ctx context.Context, db *gorm.DB, id uint64, at time.Time) error {
	if err := db.WithContext(ctx).Model(&OutboxMessage{}).Where("id = ?", id).Updates(map[string]any{
		"status":     OutboxStatusSent,
		"sent_at":    at.UTC(),
		"updated_at": at.UTC(),
	}).Error; err != nil {
		return fmt.Errorf("mark outbox message %d sent: %w", id, err)
	}
	return nil
}

// MarkOutboxRetry 记录一次失败并把邮件重排到 nextAt（退避）；状态保持 pending。
func MarkOutboxRetry(ctx context.Context, db *gorm.DB, id uint64, attempts int, nextAt time.Time, lastErr string, at time.Time) error {
	if err := db.WithContext(ctx).Model(&OutboxMessage{}).Where("id = ?", id).Updates(map[string]any{
		"status":          OutboxStatusPending,
		"attempts":        attempts,
		"next_attempt_at": nextAt.UTC(),
		"last_error":      lastErr,
		"updated_at":      at.UTC(),
	}).Error; err != nil {
		return fmt.Errorf("reschedule outbox message %d: %w", id, err)
	}
	return nil
}

// MarkOutboxFailed 在重试耗尽后把邮件标记为永久失败，保留最后一次错误。
func MarkOutboxFailed(ctx context.Context, db *gorm.DB, id uint64, attempts int, lastErr string, at time.Time) error {
	if err := db.WithContext(ctx).Model(&OutboxMessage{}).Where("id = ?", id).Updates(map[string]any{
		"status":     OutboxStatusFailed,
		"attempts":   attempts,
		"last_error": lastErr,
		"updated_at": at.UTC(),
	}).Error; err != nil {
		return fmt.Errorf("mark outbox message %d failed: %w", id, err)
	}
	return nil
}

// OutboxSummary 是管理面板要读的 outbox 读数：各状态计数与最近一次错误。
// 它只做只读聚合，不改任何行。
type OutboxSummary struct {
	Pending int64
	Sent    int64
	Failed  int64
	// LastError 是最近一次失败的服务端错误文本；没有失败时为空。
	LastError string
	// LastErrorAt 是上述错误的记录时间。
	LastErrorAt *time.Time
	// LastAttempts 是产生上述错误时该邮件的尝试次数。
	LastAttempts int
}

// OutboxSummaryOf 汇总 outbox：逐状态计数，并取最近一条带错误的行作为"最后一次错误"。
// 错误为空的行不参与 LastError 的选取（否则成功投递会把上一次错误覆盖成空）。
func OutboxSummaryOf(ctx context.Context, db *gorm.DB) (OutboxSummary, error) {
	var out OutboxSummary
	for _, tc := range []struct {
		status string
		dst    *int64
	}{
		{OutboxStatusPending, &out.Pending},
		{OutboxStatusSent, &out.Sent},
		{OutboxStatusFailed, &out.Failed},
	} {
		if err := db.WithContext(ctx).Model(&OutboxMessage{}).Where("status = ?", tc.status).Count(tc.dst).Error; err != nil {
			return OutboxSummary{}, fmt.Errorf("count outbox messages (%s): %w", tc.status, err)
		}
	}
	var row OutboxMessage
	err := db.WithContext(ctx).
		Where("last_error IS NOT NULL AND last_error <> ''").
		Order("updated_at desc, id desc").
		First(&row).Error
	if err != nil {
		if IsNotFound(err) {
			return out, nil
		}
		return OutboxSummary{}, fmt.Errorf("load last outbox error: %w", err)
	}
	if row.LastError != nil {
		out.LastError = *row.LastError
	}
	at := row.UpdatedAt
	out.LastErrorAt = &at
	out.LastAttempts = row.Attempts
	return out, nil
}
