package store

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// InstanceStats 是管理面板概览页要用的实例级计数。
// 全部是廉价的 COUNT(*)：概览页是落地页，不该拖慢任何一个分区页。
type InstanceStats struct {
	// Users 是账号总数；ActiveUsers 是其中状态为 active 的（禁用账号不计）。
	Users       int64
	ActiveUsers int64
	// Decks 是卡组总数；Notes/Cards 均不含软删除的行。
	Decks int64
	Notes int64
	Cards int64
	// DueNow 是实例级到期队列量（与健康页同一口径）。
	DueNow int64
	// JobsRunning 含排队中与执行中的作业；JobsFailed 是失败态的作业。
	JobsRunning int64
	JobsFailed  int64
}

// InstanceSummary 汇总概览页需要的计数。任何一项查询失败都整体失败：
// 概览页宁可显示错误，也不该把失败当成 0（那会让管理员以为实例是空的）。
func InstanceSummary(ctx context.Context, db *gorm.DB, now time.Time) (InstanceStats, error) {
	var s InstanceStats
	count := func(model any, query string, args ...any) (int64, error) {
		var n int64
		q := db.WithContext(ctx).Model(model)
		if query != "" {
			q = q.Where(query, args...)
		}
		return n, q.Count(&n).Error
	}

	var err error
	if s.Users, err = count(&User{}, ""); err != nil {
		return InstanceStats{}, fmt.Errorf("instance stats: users: %w", err)
	}
	if s.ActiveUsers, err = count(&User{}, "status = ?", StatusActive); err != nil {
		return InstanceStats{}, fmt.Errorf("instance stats: active users: %w", err)
	}
	if s.Decks, err = count(&Deck{}, ""); err != nil {
		return InstanceStats{}, fmt.Errorf("instance stats: decks: %w", err)
	}
	if s.Notes, err = count(&Note{}, ""); err != nil {
		return InstanceStats{}, fmt.Errorf("instance stats: notes: %w", err)
	}
	if s.Cards, err = count(&Card{}, ""); err != nil {
		return InstanceStats{}, fmt.Errorf("instance stats: cards: %w", err)
	}
	if s.DueNow, err = DueQueueSize(ctx, db, now); err != nil {
		return InstanceStats{}, fmt.Errorf("instance stats: due queue: %w", err)
	}
	// 作业状态取值见 Job 模型（queued | running | succeeded | failed）。
	if s.JobsRunning, err = count(&Job{}, "status IN ?", []string{"queued", "running"}); err != nil {
		return InstanceStats{}, fmt.Errorf("instance stats: running jobs: %w", err)
	}
	if s.JobsFailed, err = count(&Job{}, "status = ?", "failed"); err != nil {
		return InstanceStats{}, fmt.Errorf("instance stats: failed jobs: %w", err)
	}
	return s, nil
}
