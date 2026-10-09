package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// Clone 把一个卡组的全部内容（note 与 card）复制到 targetUserID 名下（「克隆/fork」）。
//
// 语义要点（内容与进度分离）：
//   - 只复制内容列（kind / fields_json / tags_json / external_ref / source / reference_refs）；
//   - 新 note 经题型重新生成 card，得到一组**全新 id、全新行**的 card；
//   - 绝不复制任何进度：新 card 没有任何 card_states 行，调用方账号下进度从零开始；
//   - 授权关系属于实例内状态，不随内容复制 —— 克隆出的卡组只有调用者一个 owner。
//
// 整个复制在一个事务内完成：任一条失败则回滚，不会留下半截副本。
// 源卡组只读，不会被本操作修改。
func (s *DeckStore) Clone(ctx context.Context, src *Deck, targetUserID uint64, newName string, presetID uint64) (*Deck, error) {
	if src == nil || src.ID == 0 {
		return nil, errors.New("clone deck: source deck is required")
	}
	if targetUserID == 0 {
		return nil, errors.New("clone deck: target user is required")
	}
	if newName == "" {
		return nil, ErrDeckNameRequired
	}
	if presetID == 0 {
		return nil, ErrDeckPresetRequired
	}
	// 与卡组建组、改名走同一套名称/描述校验：克隆曾只检查非空就 tx.Create，于是能落库一条
	// 超过 200 字符、或含控制字符的卡组，后续任何改名/导入入口都会拒绝它。
	dst := Deck{
		OwnerUserID: targetUserID,
		Name:        newName,
		Description: src.Description,
		PresetID:    presetID,
		CreatedAt:   time.Now().UTC(),
	}
	if err := validateDeckForWrite(&dst, true); err != nil {
		return nil, err
	}
	// 克隆者 = 目标用户：副本里的 note 引用必须对它可读（写前校验）。
	// 克隆者能看见源卡组时，源 note 的映射即构成可读性，因此合法克隆不会被误挡；
	// 克隆后副本映射指向新 note，读者不再依赖源卡组仍可见。
	ctx = WithActor(ctx, targetUserID)

	var out Deck
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&dst).Error; err != nil {
			return fmt.Errorf("create cloned deck: %w", err)
		}

		// 只取未软删除的 note（GORM 默认作用域）；已删除内容不应被复制。
		var notes []Note
		if err := tx.Where("deck_id = ?", src.ID).Order("id ASC").Find(&notes).Error; err != nil {
			return fmt.Errorf("list source notes for clone: %w", err)
		}

		// 复用 NoteStore 的题型校验与 card 生成管线，克隆与手工建卡走同一条路径。
		notesStore := NewNoteStore(s.db)
		for i := range notes {
			original := notes[i]
			fields, err := ParseFields(original.FieldsJSON)
			if err != nil {
				return fmt.Errorf("clone note %d: decode fields: %w", original.ID, err)
			}
			note := &Note{
				DeckID:        dst.ID,
				Kind:          original.Kind,
				TagsJSON:      original.TagsJSON,
				ExternalRef:   original.ExternalRef,
				Source:        original.Source,
				ReferenceRefs: original.ReferenceRefs,
				CreatedBy:     Ptr(targetUserID),
			}
			if _, err := notesStore.SaveInTx(ctx, tx, note, fields); err != nil {
				return fmt.Errorf("clone note %d: %w", original.ID, err)
			}
		}
		out = dst
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}
