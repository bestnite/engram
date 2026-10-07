package store

import (
	"context"
	"fmt"
)

// DeckSummary 是卡组列表页的一行：卡组本体加上卡片数。
// 卡片数在存储层用一条分组聚合一次取回，避免每个卡组各查一次（N+1）。
//
// 「今日可刷多少张」不在这里：它必须先过队列的每日额度（schedule.DeckCounts），
// 由 web 层用同一套取卡路径取得 —— 存储层再算一份原始积压数只会和队列口径漂移
// 要求的是「各卡组到期数/新卡数」两个数，不是原始积压。
type DeckSummary struct {
	Deck Deck
	// CardCount 是卡组内未删除 note 产生的未删除 card 数。
	CardCount int64
}

// countCardsByDeck 统计每个卡组的有效 card 数。
// 直接用 Table/Joins 写显式条件，不走模型自动软删除，条件集中在一处便于核对。
func (s *DeckStore) countCardsByDeck(ctx context.Context, deckIDs []uint64) (map[uint64]int64, error) {
	type row struct {
		DeckID uint64
		N      int64
	}
	var rows []row
	if err := s.db.WithContext(ctx).
		Table("cards AS c").
		Select("n.deck_id AS deck_id, COUNT(c.id) AS n").
		Joins("JOIN notes AS n ON n.id = c.note_id AND n.deleted_at IS NULL").
		Where("c.deleted_at IS NULL AND n.deck_id IN ?", deckIDs).
		Group("n.deck_id").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("count cards by deck: %w", err)
	}
	out := make(map[uint64]int64, len(rows))
	for _, r := range rows {
		out[r.DeckID] = r.N
	}
	return out, nil
}
