package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"example.com/flashcard/internal/cardtype"
)

var (
	// ErrNoteDeckRequired 表示 note 未指定所属卡组。
	ErrNoteDeckRequired = errors.New("note deck is required")
	// ErrNoteKindRequired 表示 note 未指定题型。
	ErrNoteKindRequired = errors.New("note kind is required")
)

// NoteStore 封装 notes 表，并实现\"由题型生成 cards\"的管线（DESIGN.md §2.1、§6.2）。
//
// 内容与进度分离是本项目最重要的一条设计原则：note/card 只描述内容，
// 用户的 FSRS 进度挂在 card 上。因此更新内容时绝不能重建（删旧插新）card，
// 否则会连带丢失进度 —— 详见 syncCards 的不变量说明。
type NoteStore struct {
	db       *gorm.DB
	registry *cardtype.Registry
}

// NewNoteStore 构造笔记存储；题型来自内置注册表（cardtype.Default）。
func NewNoteStore(db *gorm.DB) *NoteStore {
	return &NoteStore{db: db, registry: cardtype.Default}
}

// prepareNoteFields 校验题型字段、把 fields 序列化进 n.FieldsJSON，并返回该 note 应产出的 cards。
// 校验在写库之前完成，避免把非法内容落库；返回的 error 是可读英文并点名字段。
func (s *NoteStore) prepareNoteFields(n *Note, fields map[string]any) ([]cardtype.Card, error) {
	if n.Kind == "" {
		return nil, ErrNoteKindRequired
	}
	if err := s.registry.Validate(n.Kind, fields); err != nil {
		return nil, fmt.Errorf("validate note fields: %w", err)
	}
	wanted, err := s.registry.Cards(cardtype.Note{Kind: n.Kind, Fields: fields})
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("encode note fields: %w", err)
	}
	n.FieldsJSON = string(raw)
	return wanted, nil
}

// Create 在同一事务内写入 note 并生成它的 cards。
// 返回的 cards 已填充 NoteID 与自增 ID。任一步失败则整体回滚，不会留下\"有 note 无 card\"的半截状态。
func (s *NoteStore) Create(ctx context.Context, n *Note, fields map[string]any) ([]Card, error) {
	if n.DeckID == 0 {
		return nil, ErrNoteDeckRequired
	}
	wanted, err := s.prepareNoteFields(n, fields)
	if err != nil {
		return nil, err
	}
	if n.TagsJSON == "" {
		// 字符串默认值由 store 层在 Go 侧显式给出（models.go 包注释）。
		n.TagsJSON = "[]"
	}
	now := time.Now().UTC()
	if n.CreatedAt.IsZero() {
		n.CreatedAt = now
	}
	n.UpdatedAt = now

	var cards []Card
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(n).Error; err != nil {
			return fmt.Errorf("create note: %w", err)
		}
		created, err := syncCards(tx, n.ID, wanted, now)
		if err != nil {
			return err
		}
		cards = created
		return nil
	})
	if err != nil {
		return nil, err
	}
	return cards, nil
}

// ByID 取一个未软删除的 note；已删除的 note 视为不存在（用 Restore 恢复）。
func (s *NoteStore) ByID(ctx context.Context, id uint64) (*Note, error) {
	var n Note
	if err := s.db.WithContext(ctx).First(&n, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &n, nil
}

// Update 更新 note 的内容并同步它的 cards。
//
// 验收关键：更新后已存在的 card 与其 id 保持不变。同步只复用旧 card（按 template 匹配，
// id 不变）、补插新出现的 template，绝不删除已有行（见 syncCards）。
func (s *NoteStore) Update(ctx context.Context, n *Note, fields map[string]any) ([]Card, error) {
	if n.ID == 0 {
		return nil, errors.New("update note: id is required")
	}
	existing, err := s.ByID(ctx, n.ID)
	if err != nil {
		return nil, err
	}
	if n.Kind == "" {
		n.Kind = existing.Kind
	}
	wanted, err := s.prepareNoteFields(n, fields)
	if err != nil {
		return nil, err
	}
	if n.TagsJSON == "" {
		n.TagsJSON = existing.TagsJSON
	}
	now := time.Now().UTC()
	n.UpdatedAt = now

	var cards []Card
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		updates := map[string]any{
			"kind":        n.Kind,
			"fields_json": n.FieldsJSON,
			"tags_json":   n.TagsJSON,
			"updated_at":  now,
		}
		if err := tx.Model(&Note{}).Where("id = ?", n.ID).Updates(updates).Error; err != nil {
			return fmt.Errorf("update note %d: %w", n.ID, err)
		}
		synced, err := syncCards(tx, n.ID, wanted, now)
		if err != nil {
			return err
		}
		cards = synced
		return nil
	})
	if err != nil {
		return nil, err
	}
	return cards, nil
}

// Delete 软删除 note：只写 notes.deleted_at，不碰任何 cards 行。
// card 上的进度必须保留，所以 cards 的不可见由查询层（CardStore）过滤实现，而不是级联写 cards。
// 已删除或不存在时返回 gorm.ErrRecordNotFound。
func (s *NoteStore) Delete(ctx context.Context, id uint64) error {
	res := s.db.WithContext(ctx).Where("id = ?", id).Delete(&Note{})
	if res.Error != nil {
		return fmt.Errorf("delete note %d: %w", id, res.Error)
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// Restore 清除 notes.deleted_at；card 行与 id 自始至终未变，故恢复后重新可见。
// note 不存在时返回 gorm.ErrRecordNotFound。
func (s *NoteStore) Restore(ctx context.Context, id uint64) error {
	res := s.db.WithContext(ctx).Unscoped().Model(&Note{}).Where("id = ?", id).
		Updates(map[string]any{"deleted_at": nil})
	if res.Error != nil {
		return fmt.Errorf("restore note %d: %w", id, res.Error)
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
