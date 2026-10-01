package store

import (
	"context"
	"fmt"
	"time"
)

// DeckSummary 是卡组列表页的一行：卡组本体加上卡片数与到期数。
// 计数在存储层用两条分组聚合一次取回，避免每个卡组各查一次（N+1）。
type DeckSummary struct {
	Deck Deck
	// CardCount 是卡组内未删除 note 产生的未删除 card 数。
	CardCount int64
	// DueCount 是当前用户可复习的 card 数：尚未有状态的新卡，或状态到期时间已过的卡；
	// 已暂停（suspended）的卡不计入。
	DueCount int64
}

// Summaries 返回某用户名下卡组的列表，附带卡片数与当前用户的到期数，按创建时间倒序。
// userID 决定“到期”按谁的进度计算；now 由调用方传入，便于测试固定时间。
func (s *DeckStore) Summaries(ctx context.Context, ownerUserID, userID uint64, now time.Time) ([]DeckSummary, error) {
	decks, err := s.ListByOwner(ctx, ownerUserID)
	if err != nil {
		return nil, err
	}
	out := make([]DeckSummary, 0, len(decks))
	if len(decks) == 0 {
		return out, nil
	}
	ids := make([]uint64, 0, len(decks))
	for i := range decks {
		ids = append(ids, decks[i].ID)
	}

	cardCounts, err := s.countCardsByDeck(ctx, ids)
	if err != nil {
		return nil, err
	}
	dueCounts, err := s.countDueByDeck(ctx, ids, userID, now)
	if err != nil {
		return nil, err
	}
	for i := range decks {
		out = append(out, DeckSummary{
			Deck:      decks[i],
			CardCount: cardCounts[decks[i].ID],
			DueCount:  dueCounts[decks[i].ID],
		})
	}
	return out, nil
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

// countDueByDeck 统计每个卡组里当前用户可复习的 card 数。
// 新卡（没有 card_states 行）视为到期；已有状态且 due_at 已过也计入；暂停的卡排除。
func (s *DeckStore) countDueByDeck(ctx context.Context, deckIDs []uint64, userID uint64, now time.Time) (map[uint64]int64, error) {
	type row struct {
		DeckID uint64
		N      int64
	}
	var rows []row
	if err := s.db.WithContext(ctx).
		Table("cards AS c").
		Select("n.deck_id AS deck_id, COUNT(c.id) AS n").
		Joins("JOIN notes AS n ON n.id = c.note_id AND n.deleted_at IS NULL").
		Joins("LEFT JOIN card_states AS cs ON cs.card_id = c.id AND cs.user_id = ?", userID).
		Where("c.deleted_at IS NULL AND c.suspended_at IS NULL AND n.deck_id IN ?", deckIDs).
		Where("cs.card_id IS NULL OR cs.due_at IS NULL OR cs.due_at <= ?", now).
		Group("n.deck_id").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("count due cards by deck: %w", err)
	}
	out := make(map[uint64]int64, len(rows))
	for _, r := range rows {
		out[r.DeckID] = r.N
	}
	return out, nil
}
