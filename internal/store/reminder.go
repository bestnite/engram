package store

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 复习到期提醒（C 类）的发送台账（DESIGN.md §4.7；AGENTS.md M1-21）。
//
// 为什么用一张小表而不是别的方案：
//   - 「每用户每天最多一封」必须跨进程重启仍然成立。进程内计数器一重启就归零，
//     于是重启后当天会再发一封——这正是硬上限要防的事。
//   - 主键 (user_id, day) 让唯一约束成为最后一道防线：即使两个 worker 并发判定，
//     数据库也只接受一行，不依赖调用方的时序正确。
//   - 放独立表而不是给 users 加列：models.go 是单写者热点（AGENTS.md §6.4），
//     给 User 加列要改那份热点文件；独立表只需在 AllModels() 登记一行，且台账与
//     账号生命周期解耦——删账号时随行一起消失，不需要额外清理。
//
// Day 用「用户本地复习日」（YYYY-MM-DD，按 day_cutoff_hour 切分），与 reviews.review_day
// 是同一套时区/切点口径；不用 UTC 日期，否则跨时区用户在本地凌晨会被判成两天。
type ReminderLog struct {
	UserID uint64 `gorm:"primaryKey;column:user_id" json:"user_id"`
	// Day 是用户本地复习日，YYYY-MM-DD（与 reviews.review_day 同一口径）。
	Day string `gorm:"primaryKey;column:day" json:"day"`
	// SentAt 是实际入队时刻（UTC）。
	SentAt time.Time `gorm:"column:sent_at;not null" json:"sent_at"`
}

// TableName 固定表名，避免复数化规则在不同 GORM 版本下漂移。
func (ReminderLog) TableName() string { return "reminder_log" }

// ReminderCandidate 是提醒 worker 需要的用户字段与其当前到期卡数。
// 只有「有到期卡」的活跃用户会出现在结果里——没有到期卡的账号不该收到催促。
type ReminderCandidate struct {
	ID            uint64 `gorm:"column:id" json:"id"`
	Email         string `gorm:"column:email" json:"email"`
	Locale        string `gorm:"column:locale" json:"locale"`
	Timezone      string `gorm:"column:timezone" json:"timezone"`
	DayCutoffHour int    `gorm:"column:day_cutoff_hour" json:"day_cutoff_hour"`
	// DueCount 是该用户当前到期的 card_states 行数（同一张卡对同一用户只计一次）。
	DueCount int64 `gorm:"column:due_count" json:"due_count"`
}

// ReminderCandidates 返回有到期卡、可收信的活跃用户。
//
// 到期口径与 schedule 队列、DueQueueSize 一致：只算已排期且 due_at <= now 的 card_states，
// 排除软删除的 card/note 与暂停的卡（DESIGN.md §3.3）。同一用户可能有多行，用 GROUP BY
// 归并并顺带给出到期卡数。
func ReminderCandidates(ctx context.Context, db *gorm.DB, now time.Time) ([]ReminderCandidate, error) {
	var out []ReminderCandidate
	err := db.WithContext(ctx).Table("users AS u").
		Select("u.id AS id, u.email AS email, u.locale AS locale, u.timezone AS timezone, "+
			"u.day_cutoff_hour AS day_cutoff_hour, COUNT(cs.card_id) AS due_count").
		Joins("JOIN card_states AS cs ON cs.user_id = u.id AND cs.due_at IS NOT NULL AND cs.due_at <= ?", now.UTC()).
		Joins("JOIN cards AS c ON c.id = cs.card_id AND c.deleted_at IS NULL AND c.suspended_at IS NULL").
		Joins("JOIN notes AS n ON n.id = c.note_id AND n.deleted_at IS NULL").
		Where("u.status = ? AND u.email <> ''", StatusActive).
		Group("u.id, u.email, u.locale, u.timezone, u.day_cutoff_hour").
		Order("u.id asc").
		Scan(&out).Error
	if err != nil {
		return nil, fmt.Errorf("load reminder candidates: %w", err)
	}
	return out, nil
}

// HasReminderBeenSent 报告该用户在其本地复习日 day 是否已经发过提醒。
func HasReminderBeenSent(ctx context.Context, db *gorm.DB, userID uint64, day string) (bool, error) {
	var n int64
	if err := db.WithContext(ctx).Model(&ReminderLog{}).
		Where("user_id = ? AND day = ?", userID, day).
		Count(&n).Error; err != nil {
		return false, fmt.Errorf("check reminder log: %w", err)
	}
	return n > 0, nil
}

// RecordReminderSent 记录一封已入队的提醒。主键冲突时静默跳过（DoNothing）：
// 唯一约束是「每天一封」的兜底，重复记录不该让调用方报错，更不该阻塞发信。
func RecordReminderSent(ctx context.Context, db *gorm.DB, userID uint64, day string, at time.Time) error {
	row := ReminderLog{UserID: userID, Day: day, SentAt: at.UTC()}
	if err := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return fmt.Errorf("record reminder sent: %w", err)
	}
	return nil
}
