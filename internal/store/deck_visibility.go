package store

import (
	"context"
	"fmt"
	"time"
)

// ListVisible 返回某用户“在列表里应该看到”的卡组（M5-5 列表语义）：
//
//  1. 自己拥有的（不论可见性）；
//  2. 被显式授权访问的（deck_grants 有行）；
//  3. 其他用户的 public 卡组（DESIGN.md §5：登录用户可见并可自取副本）。
//
// 明确排除其他用户的 unlisted 与 private 卡组：
//   - unlisted 的语义是“拿到链接可看”，它只能通过直接 id 命中（DeckAccess 负责），
//     出现在任何列表里都会把“不公开”变成“半公开”；
//   - private 只对授权者可见，而授权者已由第 2 条覆盖。
func (s *DeckStore) ListVisible(ctx context.Context, userID uint64) ([]Deck, error) {
	granted := s.db.Model(&DeckGrant{}).Select("deck_id").Where("user_id = ?", userID)
	var decks []Deck
	if err := s.db.WithContext(ctx).
		Where("owner_user_id = ? OR visibility = ? OR id IN (?)", userID, DeckVisibilityPublic, granted).
		Order("created_at DESC, id DESC").Find(&decks).Error; err != nil {
		return nil, fmt.Errorf("list visible decks: %w", err)
	}
	return decks, nil
}

// SummariesVisible 是列表页用的聚合版：可见卡组各带卡片数与当前用户的到期数。
// 与 Summaries 的差别只在“哪些卡组入选”——计数逻辑完全复用，避免两套口径漂移。
func (s *DeckStore) SummariesVisible(ctx context.Context, userID uint64, now time.Time) ([]DeckSummary, error) {
	decks, err := s.ListVisible(ctx, userID)
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
