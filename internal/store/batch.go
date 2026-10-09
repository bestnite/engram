package store

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// batchLookupSize 是分批 IN 查询的每批 id 上限。
//
// 保守取 200：SQLite 的绑定参数上限由编译时的 SQLITE_MAX_VARIABLE_NUMBER 决定（历史默认 999，
// 新版 32766），而 card_states 的批量查询按 (user_id, card_id IN (...)) 过滤，每个 id 占一个
// 参数、另加一个 user_id。取 200 后单条语句的参数数远低于任何默认上限，也不必依赖具体驱动版本。
const batchLookupSize = 200

// chunkIDs 先去掉重复 id，再把集合切成每批不超过 batchLookupSize 的切片，供分批 IN 查询使用。
//
// 去重必须发生在分批之前：重复 id 既浪费参数额度，也会让「每批 id 数」与实际要取的行数不一致。
// 保持首次出现的顺序（IN 查询本就不关心顺序，这里只是让结果可预期）。
func chunkIDs(ids []uint64) [][]uint64 {
	seen := make(map[uint64]bool, len(ids))
	unique := make([]uint64, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}
	var out [][]uint64
	for start := 0; start < len(unique); start += batchLookupSize {
		end := start + batchLookupSize
		if end > len(unique) {
			end = len(unique)
		}
		out = append(out, unique[start:end])
	}
	return out
}

// ByIDs 按主键批量取未软删除的 note，返回 id→note 的映射；不存在的 id 不出现在结果里。
//
// 为什么需要它：复习队列装配过去对每张卡调用一次 ByID，一次答题的查询数随队列长度线性增长。
// 分批集合查询把装配阶段的查询数降成与队列长度无关的常数级。
func (s *NoteStore) ByIDs(ctx context.Context, ids []uint64) (map[uint64]*Note, error) {
	out := make(map[uint64]*Note, len(ids))
	for _, chunk := range chunkIDs(ids) {
		var notes []Note
		if err := s.db.WithContext(ctx).Where("id IN ?", chunk).Find(&notes).Error; err != nil {
			return nil, fmt.Errorf("list notes by ids: %w", err)
		}
		for i := range notes {
			out[notes[i].ID] = &notes[i]
		}
	}
	return out, nil
}

// ByIDs 按主键批量取可见的 card（所属 note 未软删除），返回 id→card 的映射。
// 可见性口径与 ByID 完全一致（同一 visibleCards 起点），只是把「一张」换成「一批」。
func (s *CardStore) ByIDs(ctx context.Context, ids []uint64) (map[uint64]*Card, error) {
	out := make(map[uint64]*Card, len(ids))
	for _, chunk := range chunkIDs(ids) {
		var cards []Card
		if err := s.visibleCards(ctx).Select("cards.*").Where("cards.id IN ?", chunk).Find(&cards).Error; err != nil {
			return nil, fmt.Errorf("list cards by ids: %w", err)
		}
		for i := range cards {
			out[cards[i].ID] = &cards[i]
		}
	}
	return out, nil
}

// ByIDs 按主键批量取 deck，返回 id→deck 的映射；不存在的 id 不出现在结果里。
// 归档不影响读取（与 ByID 同口径），调用方按 ArchivedAt 决定如何展示。
func (s *DeckStore) ByIDs(ctx context.Context, ids []uint64) (map[uint64]*Deck, error) {
	out := make(map[uint64]*Deck, len(ids))
	for _, chunk := range chunkIDs(ids) {
		var decks []Deck
		if err := s.db.WithContext(ctx).Where("id IN ?", chunk).Find(&decks).Error; err != nil {
			return nil, fmt.Errorf("list decks by ids: %w", err)
		}
		for i := range decks {
			out[decks[i].ID] = &decks[i]
		}
	}
	return out, nil
}

// CardStatesByCardIDs 批量取某用户对一组 card 的进度行，返回 card_id→state 的映射。
//
// card_states 没有独立的 Store 类型（读取散落在 api 与 store 的聚合里），因此与
// StudySettingsFor 一样以包级函数 + 显式 db 的形式提供，供 api 的到期队列装配按 id 集合取回。
// userID 为 0（未登录/系统调用）时直接返回空映射，不发起查询。
func CardStatesByCardIDs(ctx context.Context, db *gorm.DB, userID uint64, cardIDs []uint64) (map[uint64]CardState, error) {
	out := make(map[uint64]CardState, len(cardIDs))
	if userID == 0 {
		return out, nil
	}
	for _, chunk := range chunkIDs(cardIDs) {
		var states []CardState
		if err := db.WithContext(ctx).Where("user_id = ? AND card_id IN ?", userID, chunk).Find(&states).Error; err != nil {
			return nil, fmt.Errorf("list card states by card ids: %w", err)
		}
		for _, st := range states {
			out[st.CardID] = st
		}
	}
	return out, nil
}
