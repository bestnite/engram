package store

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/cardtype"
)

// CardStore 封装 cards 表的 GORM 访问。
//
// 可见性规则：note 被软删除时，它的 cards 一并不可见 —— 这一过滤在查询层用与 notes 的
// join 实现，而不是在删除时改写 cards 行。这样进度数据（挂在 card 上）永不因删除内容而丢失。
type CardStore struct {
	db *gorm.DB
}

// NewCardStore 构造卡片存储。
func NewCardStore(db *gorm.DB) *CardStore { return &CardStore{db: db} }

// visibleCards 是\"card 可见\"的统一查询起点：GORM 自动加上 cards.deleted_at IS NULL，
// join 条件再要求所属 note 未软删除。
func (s *CardStore) visibleCards(ctx context.Context) *gorm.DB {
	return s.db.WithContext(ctx).Model(&Card{}).
		Joins("JOIN notes ON notes.id = cards.note_id AND notes.deleted_at IS NULL")
}

// ByID 取一张可见的 card；所属 note 已软删除时返回 gorm.ErrRecordNotFound。
func (s *CardStore) ByID(ctx context.Context, id uint64) (*Card, error) {
	var c Card
	if err := s.visibleCards(ctx).Select("cards.*").Where("cards.id = ?", id).First(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

// ByNote 按 note 列出可见的 cards，按 ordinal、id 升序；note 已软删除时返回空切片。
// 返回的卡片标识（template）与 ordinal 与 cardtype 产出保持一致。
func (s *CardStore) ByNote(ctx context.Context, noteID uint64) ([]Card, error) {
	var cards []Card
	if err := s.visibleCards(ctx).Select("cards.*").
		Where("cards.note_id = ?", noteID).
		Order("cards.ordinal ASC, cards.id ASC").Find(&cards).Error; err != nil {
		return nil, fmt.Errorf("list cards by note: %w", err)
	}
	return cards, nil
}

// syncCards 让 note 的 card 集合与 cardtype 产出的 wanted 对齐。
//
// 不变量（AGENTS.md §2.3 第 1 条，内容与进度分离）：
//   - 已存在的 card 与其 id 永远保留：进度数据挂在 card 上，删行会连带丢进度；
//   - 已存在的 template 复用原行（软删除的在此恢复、ordinal 对齐），id 保持不变；
//   - 只有新出现的 template 会被插入；
//   - 已有但不再出现在 wanted 里的 template 被**软删除**：删掉 {{c2::}} 之后 cloze:2 不能再
//     进复习队列（它渲染出来没有任何挖空）。软删除只写 cards.deleted_at，card_states 与 reviews
//     原样保留，template 重新出现时上面的复用分支会恢复同一行，进度随之回来。
//
// 同一次同步内若出现重复 template，直接报错 —— 这是 (note_id, template) 唯一约束的
// Go 侧防线，数据库唯一索引是最终兜底。
func syncCards(tx *gorm.DB, noteID uint64, wanted []cardtype.Card, now time.Time) ([]Card, error) {
	// 必须包含软删除行：唯一索引不排除它们，按 template 复用才能避免撞唯一约束。
	var existing []Card
	if err := tx.Unscoped().Where("note_id = ?", noteID).Find(&existing).Error; err != nil {
		return nil, fmt.Errorf("load cards for note %d: %w", noteID, err)
	}
	byTemplate := make(map[string]*Card, len(existing))
	for i := range existing {
		byTemplate[existing[i].Template] = &existing[i]
	}

	out := make([]Card, 0, len(wanted))
	seen := make(map[string]bool, len(wanted))
	for _, want := range wanted {
		if want.Template == "" {
			return nil, fmt.Errorf("note %d produced a card with an empty template", noteID)
		}
		if seen[want.Template] {
			return nil, fmt.Errorf("note %d produced duplicate card template %q", noteID, want.Template)
		}
		seen[want.Template] = true

		if row, ok := byTemplate[want.Template]; ok {
			// 复用原行：仅在需要恢复软删除或对齐 ordinal 时写库，其余情况完全不写，id 保持不变。
			if row.DeletedAt.Valid || row.Ordinal != want.Ordinal {
				updates := map[string]any{"ordinal": want.Ordinal, "deleted_at": nil}
				if err := tx.Unscoped().Model(&Card{}).Where("id = ?", row.ID).Updates(updates).Error; err != nil {
					return nil, fmt.Errorf("update card %d: %w", row.ID, err)
				}
				row.Ordinal = want.Ordinal
				row.DeletedAt = gorm.DeletedAt{}
			}
			out = append(out, *row)
			continue
		}

		card := Card{NoteID: noteID, Template: want.Template, Ordinal: want.Ordinal, CreatedAt: now}
		if err := tx.Create(&card).Error; err != nil {
			return nil, fmt.Errorf("create card %q for note %d: %w", want.Template, noteID, err)
		}
		out = append(out, card)
	}

	// 收尾：仍然存活但已不在 wanted 里的卡软删除。按主键逐个写，范围与上面加载的集合一致。
	var stale []uint64
	for i := range existing {
		if !existing[i].DeletedAt.Valid && !seen[existing[i].Template] {
			stale = append(stale, existing[i].ID)
		}
	}
	if len(stale) > 0 {
		if err := tx.Model(&Card{}).Where("id IN ?", stale).Update("deleted_at", now).Error; err != nil {
			return nil, fmt.Errorf("soft-delete stale cards of note %d: %w", noteID, err)
		}
	}
	return out, nil
}
