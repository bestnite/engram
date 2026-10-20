package store

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DeckDailyExtra 记录某用户在某卡组上、某个复习日里「再来一组」追加的新卡额度。
//
// 每日新卡上限是学习目标而不是硬上限：学完今天的量之后，用户可以只为今天、只为这个卡组
// 多放一批新卡，第二天自动回到原来的上限。额度要落库而不是放在请求参数里，因为队列在每次
// 评分后都会重建，刷新页面或换个入口（REST / MCP）也必须看到同一份追加量。
//
// 主键带 review_day，所以「只对今天生效」不需要任何清理任务：别的复习日查不到这一行。
// 写入时顺手删掉同一 (卡组, 用户) 更早复习日的行，表里每对 (卡组, 用户) 最多留一行。
// 属主与共享成员一视同仁地写这张表：追加量是本人的学习节奏，不会影响卡组上的其他成员。
type DeckDailyExtra struct {
	DeckID uint64 `gorm:"primaryKey" json:"-"`
	UserID uint64 `gorm:"primaryKey;index" json:"-"`
	// ReviewDay 是用户自己的复习日（YYYY-MM-DD），与 reviews.review_day 同一口径。
	ReviewDay string    `gorm:"primaryKey;size:10" json:"review_day"`
	ExtraNew  int       `gorm:"not null" json:"extra_new"`
	UpdatedAt time.Time `gorm:"not null" json:"updated_at"`
}

func (DeckDailyExtra) TableName() string { return "deck_daily_extras" }

// AddDailyExtraNew 把 (卡组, 用户, 复习日) 的追加新卡额度增加 n，并返回累加后的值。
// 同一 (卡组, 用户) 更早复习日的行在同一事务里删除：它们已不再生效。n 必须为正。
func AddDailyExtraNew(ctx context.Context, db *gorm.DB, deckID, userID uint64, day string, n int) (int, error) {
	if n <= 0 {
		return 0, fmt.Errorf("add daily extra new: n must be positive, got %d", n)
	}
	var total int
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("deck_id = ? AND user_id = ? AND review_day < ?", deckID, userID, day).
			Delete(&DeckDailyExtra{}).Error; err != nil {
			return fmt.Errorf("delete stale daily extras: %w", err)
		}
		now := time.Now().UTC()
		row := DeckDailyExtra{DeckID: deckID, UserID: userID, ReviewDay: day, ExtraNew: n, UpdatedAt: now}
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "deck_id"}, {Name: "user_id"}, {Name: "review_day"}},
			DoUpdates: clause.Assignments(map[string]any{
				"extra_new":  gorm.Expr("deck_daily_extras.extra_new + ?", n),
				"updated_at": now,
			}),
		}).Create(&row).Error; err != nil {
			return fmt.Errorf("upsert daily extra: %w", err)
		}
		var saved DeckDailyExtra
		if err := tx.Where("deck_id = ? AND user_id = ? AND review_day = ?", deckID, userID, day).
			Take(&saved).Error; err != nil {
			return fmt.Errorf("read daily extra: %w", err)
		}
		total = saved.ExtraNew
		return nil
	})
	return total, err
}

// DailyExtraNew 返回 userID 在给定复习日里各卡组的追加新卡额度（键是卡组主键）；
// 没有追加的卡组不出现在结果里，调用方按 0 处理。
func DailyExtraNew(ctx context.Context, db *gorm.DB, userID uint64, day string, deckIDs []uint64) (map[uint64]int, error) {
	out := make(map[uint64]int, len(deckIDs))
	if len(deckIDs) == 0 {
		return out, nil
	}
	var rows []DeckDailyExtra
	if err := db.WithContext(ctx).Where("user_id = ? AND review_day = ? AND deck_id IN ?", userID, day, deckIDs).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load daily extras: %w", err)
	}
	for _, r := range rows {
		out[r.DeckID] = r.ExtraNew
	}
	return out, nil
}
