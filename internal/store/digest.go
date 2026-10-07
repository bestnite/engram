package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 学习摘要（C 类）的发送台账（ROADMAP.md M1-23）。
//
// 为什么单开一张表，而不是复用 reminder_log：reminder_log 的主键是 (user_id, day)，
// 其中 day 是「用户本地复习日」——与周粒度会撞键（同一个复习日既可能属于这一周也可能属于
// 下一周），语义无法并存。给既有表加一列 NOT NULL 属于「新非空约束」，要走版本化的破坏性
// 迁移（AGENTS.md §2.3 第 5 条、store.Migration）；新建一张表则是纯增量，AutoMigrate 直接
// 处理，迁移面最小。
//
// 「每人每周恰好一封」必须跨进程重启成立，所以去重落在数据库而不是进程内计数器：主键
// (user_id, week_start) 的唯一约束是最后一道防线，即使两个 worker 并发判定，库也只接受一行。
//
// WeekStart 是该用户**本地周起始日**（周一）的 YYYY-MM-DD，不是 UTC 日期：跨时区用户在
// 本地周一凌晨必须落到同一周，否则会出现「一周两封」或「少发一封」。
//
// M1-23 新增的表：纯增量建表，AutoMigrate 直接处理，不需要一次性迁移函数。
type DigestLog struct {
	UserID uint64 `gorm:"primaryKey;column:user_id" json:"user_id"`
	// WeekStart 是用户本地周起始日（周一），YYYY-MM-DD。
	WeekStart string `gorm:"primaryKey;column:week_start" json:"week_start"`
	// SentAt 是实际入队时刻（UTC）。
	SentAt time.Time `gorm:"column:sent_at;not null" json:"sent_at"`
}

// TableName 固定表名，避免复数化规则在不同 GORM 版本下漂移。
func (DigestLog) TableName() string { return "digest_log" }

// DigestCandidate 是摘要 worker 需要的用户字段。
// 与提醒不同，摘要发给所有可收信的活跃用户，不要求有到期卡——没有复习的人收到的是一份
// 「这段时间没有复习」的回顾，这正是它作为回顾与坚持感（而非催促）的定位。
type DigestCandidate struct {
	ID            uint64 `gorm:"column:id" json:"id"`
	Email         string `gorm:"column:email" json:"email"`
	Locale        string `gorm:"column:locale" json:"locale"`
	Timezone      string `gorm:"column:timezone" json:"timezone"`
	DayCutoffHour *int   `gorm:"column:day_cutoff_hour" json:"day_cutoff_hour"`
	// ReminderHour 是用户选择的本地发送小时；NULL 表示未设置（用全局默认）。周报与复习提醒
	// 共用同一小时，因此读的是同一列。
	ReminderHour *int `gorm:"column:reminder_hour" json:"reminder_hour,omitempty"`
}

// DigestCandidates 返回所有可收信的活跃用户（有邮箱）。按 id 升序，保证每轮顺序稳定。
func DigestCandidates(ctx context.Context, db *gorm.DB) ([]DigestCandidate, error) {
	var out []DigestCandidate
	err := db.WithContext(ctx).Table("users").
		Select("id AS id, email AS email, locale AS locale, timezone AS timezone, day_cutoff_hour AS day_cutoff_hour, reminder_hour AS reminder_hour").
		Where("status = ? AND email <> ''", StatusActive).
		Order("id asc").
		Scan(&out).Error
	if err != nil {
		return nil, fmt.Errorf("load digest candidates: %w", err)
	}
	return out, nil
}

// HasDigestBeenSent 报告该用户在其本地周起始日 weekStart 是否已经发过摘要。
func HasDigestBeenSent(ctx context.Context, db *gorm.DB, userID uint64, weekStart string) (bool, error) {
	var n int64
	if err := db.WithContext(ctx).Model(&DigestLog{}).
		Where("user_id = ? AND week_start = ?", userID, weekStart).
		Count(&n).Error; err != nil {
		return false, fmt.Errorf("check digest log: %w", err)
	}
	return n > 0, nil
}

// LastDigestSentAt 跨周取最近入队时间；空账本不是查询故障。
func LastDigestSentAt(ctx context.Context, db *gorm.DB, userID uint64) (time.Time, bool, error) {
	var row DigestLog
	err := db.WithContext(ctx).Select("sent_at").Where("user_id = ?", userID).Order("sent_at DESC").Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("load last digest sent time: %w", err)
	}
	return row.SentAt, true, nil
}

// RecordDigestSent 记录一封已入队的摘要。主键冲突时静默跳过（DoNothing）：
// 唯一约束是「每周一封」的兜底，重复记录不该让调用方报错，更不该阻塞发信。
func RecordDigestSent(ctx context.Context, db *gorm.DB, userID uint64, weekStart string, at time.Time) error {
	row := DigestLog{UserID: userID, WeekStart: weekStart, SentAt: at.UTC()}
	if err := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return fmt.Errorf("record digest sent: %w", err)
	}
	return nil
}
