package store

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// DueQueueSize 返回全实例当前到期队列的长度（健康页的「当前到期队列量」）。
//
// 口径（写死，测试用同样的独立 SQL 对拍）：
//   - 计的是 card_states 行，即每个用户各自队列里的一张到期卡；同一张卡被两个用户
//     排到就计两次——这正是「队列长度」而不是「卡数」。
//   - 只算已排期且 due_at <= now 的行；state='new' 且未排期的新卡不算「到期」。
//   - 卡片软删除或暂停、所属 note 软删除时排除，与 schedule 队列的可见性规则一致。
//
// now 由调用方传入，便于测试固定时间；内部统一按 UTC 比较。
func DueQueueSize(ctx context.Context, db *gorm.DB, now time.Time) (int64, error) {
	var n int64
	err := db.WithContext(ctx).Table("card_states AS cs").
		Joins("JOIN cards AS c ON c.id = cs.card_id AND c.deleted_at IS NULL AND cs.suspended_at IS NULL").
		Joins("JOIN notes AS n ON n.id = c.note_id AND n.deleted_at IS NULL").
		Where("cs.due_at IS NOT NULL AND cs.due_at <= ?", now.UTC()).
		Count(&n).Error
	if err != nil {
		return 0, fmt.Errorf("count due queue: %w", err)
	}
	return n, nil
}
