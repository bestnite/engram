package store

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// visibleDecksQuery 是「某用户可见卡组」的唯一谓词来源：自有 ∪ 被 deck_grants 授权 ∪ 其他用户的 public。
//
// 列表页（ListVisible）与队列的全库口径（VisibleIDs）必须看到同一批卡组，所以谓词只写这一处：
// 各写一份会慢慢漂移。之前队列的全库口径完全没有卡组过滤，把别的用户 private 卡组的新卡
// 也算进了当前用户的队列（越权）。
//
// 明确排除其他用户的 unlisted 与 private：
//   - unlisted 的语义是「拿到链接可看」，只能通过直接 id 命中（DeckAccess 负责），
//     出现在任何列表里都会把「不公开」变成「半公开」；
//   - private 只对授权者可见，而授权者已由 deck_grants 分支覆盖。
func (s *DeckStore) visibleDecksQuery(ctx context.Context, userID uint64) *gorm.DB {
	granted := s.db.Model(&DeckGrant{}).Select("deck_id").Where("user_id = ?", userID)
	return s.db.WithContext(ctx).Model(&Deck{}).
		Where("owner_user_id = ? OR visibility = ? OR id IN (?)", userID, DeckVisibilityPublic, granted)
}

// ListVisible 返回某用户“在列表里应该看到”的卡组（M5-5 列表语义），按创建时间倒序。
func (s *DeckStore) ListVisible(ctx context.Context, userID uint64) ([]Deck, error) {
	var decks []Deck
	if err := s.visibleDecksQuery(ctx, userID).
		Order("created_at DESC, id DESC").Find(&decks).Error; err != nil {
		return nil, fmt.Errorf("list visible decks: %w", err)
	}
	return decks, nil
}

// VisibleIDs 返回该用户可见卡组的 id 集合，与 ListVisible 共用同一谓词（见 visibleDecksQuery）。
//
// 队列的「全库」口径（DeckID=0 且 DeckIDs 为空）用它解析范围：只要列表页看得到的卡组就能进队列，
// 别人的 private / unlisted 卡组一张都进不来。空集合表示该用户没有可见卡组，队列据此为空（不报错）。
func (s *DeckStore) VisibleIDs(ctx context.Context, userID uint64) ([]uint64, error) {
	var ids []uint64
	if err := s.visibleDecksQuery(ctx, userID).Order("id ASC").Pluck("id", &ids).Error; err != nil {
		return nil, fmt.Errorf("list visible deck ids: %w", err)
	}
	return ids, nil
}

// SummariesVisible 是列表页用的聚合版：可见卡组各带卡片数。
// 可见性与 ListVisible 同源，计数逻辑集中在 countCardsByDeck，避免两套口径漂移。
//
// 「今日可刷多少张」不在这里算：它必须先过队列的每日额度（schedule.DeckCounts），
// 由调用方按同一套取卡路径取得，列表数字才会等于点进去能刷的张数。
func (s *DeckStore) SummariesVisible(ctx context.Context, userID uint64) ([]DeckSummary, error) {
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
	for i := range decks {
		out = append(out, DeckSummary{
			Deck:      decks[i],
			CardCount: cardCounts[decks[i].ID],
		})
	}
	return out, nil
}
