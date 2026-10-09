package store

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// visibleDeckIDsQuery 返回「某用户可见卡组 id」的子查询，是可见性谓词的唯一来源。
//
// 卡组只有两种归属：自己建的（decks.owner_user_id），以及别人显式授权给自己的（deck_grants）。
// 没有第三种：不存在「默认对所有人可见」的卡组。让别人的卡组自动出现在我的列表、我的复习队列
// 和我的统计里，等于替我做了一次我从未同意的接收——这正是「公开卡组」被删除的原因。
//
// 列表页（ListVisible）、队列的全库口径（VisibleIDs）与统计页的聚合查询（StatsStore）
// 都拿它当范围条件：各写一份必然漂移。之前队列的全库口径完全没有卡组过滤，把别的用户
// private 卡组的新卡也算进了当前用户的队列（越权）；统计页的卡组维度与到期预测犯过同一个错
// （以 cards 为起点、只按 user_id 左连接 card_states，别人卡组里的卡没有状态行，于是被整体
// 算成「到期」并按其 deck_id 分组，卡组维度就冒出了别人的私有卡组）。
func visibleDeckIDsQuery(db *gorm.DB, userID uint64) *gorm.DB {
	granted := db.Model(&DeckGrant{}).Select("deck_id").Where("user_id = ?", userID)
	return db.Model(&Deck{}).Select("id").
		Where("owner_user_id = ? OR id IN (?)", userID, granted)
}

func (s *DeckStore) visibleDecksQuery(ctx context.Context, userID uint64) *gorm.DB {
	return s.db.WithContext(ctx).Model(&Deck{}).Where("id IN (?)", visibleDeckIDsQuery(s.db, userID))
}

// ListVisible 返回某用户“在列表里应该看到”的卡组（列表语义），按创建时间倒序。
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
// 别人的卡组一张都进不来（除非自己被显式授权）。空集合表示该用户没有可见卡组，队列据此为空（不报错）。
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
