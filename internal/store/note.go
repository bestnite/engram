package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"example.com/flashcard/internal/cardtype"
)

var (
	// ErrNoteDeckRequired 表示 note 未指定所属卡组。
	ErrNoteDeckRequired = errors.New("note deck is required")
	// ErrNoteKindRequired 表示 note 未指定题型。
	ErrNoteKindRequired = errors.New("note kind is required")
	// ErrNoteTagRequired 表示批量加标签时未给出标签名。
	ErrNoteTagRequired = errors.New("note tag is required")
)

// 列表状态取值（M2-7）：active 是默认（排除软删除），deleted 只看已软删除，
// all 两者都看。用稳定英文常量，避免在 handler 里散落裸字符串。
const (
	NoteStatusActive  = "active"
	NoteStatusDeleted = "deleted"
	NoteStatusAll     = "all"
)

// DefaultNotePageSize 是卡片列表的每页条数；设计只要求分页可用，不暴露给用户配置。
const DefaultNotePageSize = 50

// NoteListOptions 是卡片列表的查询条件（M2-7 分页、搜索、标签与题型筛选）。
type NoteListOptions struct {
	DeckID uint64
	// Page 从 1 起；小于 1 时按 1 处理。
	Page int
	// PerPage 为 0 时用 DefaultNotePageSize。
	PerPage int
	// Query 搜索 note 的字段内容（正面/背面等所有字段文本），大小写不敏感。
	Query string
	// Tag 按 tags_json 里的标签精确匹配（带引号边界，避免前缀误匹配）。
	Tag string
	// Kind 按题型过滤；空串表示不过滤。
	Kind string
	// Status 取值见上方常量；空串等同 NoteStatusActive。
	Status string
}

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
	var cards []Card
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		created, err := s.CreateInTx(ctx, tx, n, fields)
		cards = created
		return err
	})
	if err != nil {
		return nil, err
	}
	return cards, nil
}

// CreateInTx 在调用方给定的事务里创建 note 并生成它的 cards（M4-4 批量导入按批提交用）。
//
// 与 Create 的唯一区别是复用外部事务：批量导入把 200 条 note 放进同一个事务，
// 由调用方决定何时提交/回滚，从而减少每行一次提交的开销。校验与序列化仍在这里完成，
// 保证\"业务模型只有一份实现\"。调用方必须保证 tx 尚未提交。
func (s *NoteStore) CreateInTx(ctx context.Context, tx *gorm.DB, n *Note, fields map[string]any) ([]Card, error) {
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

	if err := tx.WithContext(ctx).Create(n).Error; err != nil {
		return nil, fmt.Errorf("create note: %w", err)
	}
	created, err := syncCards(tx, n.ID, wanted, now)
	if err != nil {
		return nil, err
	}
	return created, nil
}

// ByID 取一个未软删除的 note；已删除的 note 视为不存在（用 Restore 恢复）。
func (s *NoteStore) ByID(ctx context.Context, id uint64) (*Note, error) {
	return s.byIDTx(ctx, s.db, id)
}

// byIDTx 在给定事务/连接上取一个未软删除的 note；供 UpdateInTx 在同一事务内读取现有内容。
func (s *NoteStore) byIDTx(ctx context.Context, tx *gorm.DB, id uint64) (*Note, error) {
	var n Note
	if err := tx.WithContext(ctx).First(&n, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &n, nil
}

// Update 更新 note 的内容并同步它的 cards。
//
// 验收关键：更新后已存在的 card 与其 id 保持不变。同步只复用旧 card（按 template 匹配，
// id 不变）、补插新出现的 template，绝不删除已有行（见 syncCards）。
func (s *NoteStore) Update(ctx context.Context, n *Note, fields map[string]any) ([]Card, error) {
	var cards []Card
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		updated, err := s.UpdateInTx(ctx, tx, n, fields)
		cards = updated
		return err
	})
	if err != nil {
		return nil, err
	}
	return cards, nil
}

// UpdateInTx 在调用方给定的事务里更新 note 与它的 cards（M4-4 批量导入按批提交用）。
// 与 Update 语义一致，只是复用外部事务；现有 card 的 id 与用户进度保持不变。
func (s *NoteStore) UpdateInTx(ctx context.Context, tx *gorm.DB, n *Note, fields map[string]any) ([]Card, error) {
	if n.ID == 0 {
		return nil, errors.New("update note: id is required")
	}
	existing, err := s.byIDTx(ctx, tx, n.ID)
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

	updates := map[string]any{
		"kind":        n.Kind,
		"fields_json": n.FieldsJSON,
		"tags_json":   n.TagsJSON,
		"updated_at":  now,
	}
	if err := tx.WithContext(ctx).Model(&Note{}).Where("id = ?", n.ID).Updates(updates).Error; err != nil {
		return nil, fmt.Errorf("update note %d: %w", n.ID, err)
	}
	synced, err := syncCards(tx, n.ID, wanted, now)
	if err != nil {
		return nil, err
	}
	return synced, nil
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
	return s.RestoreInTx(ctx, s.db, id)
}

// RestoreInTx 在调用方给定的事务里恢复软删除的 note（M4-4 批量导入按批提交用：
// 命中已软删除的 external_ref 时先恢复，再 Update 才能生效）。
func (s *NoteStore) RestoreInTx(ctx context.Context, tx *gorm.DB, id uint64) error {
	res := tx.WithContext(ctx).Unscoped().Model(&Note{}).Where("id = ?", id).
		Updates(map[string]any{"deleted_at": nil})
	if res.Error != nil {
		return fmt.Errorf("restore note %d: %w", id, res.Error)
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// List 按条件分页列出卡组的 notes，返回当页行与符合条件的总数（M2-7）。
//
// 查询约定（DESIGN.md §2.3）：分页统一 LIMIT/OFFSET；模糊匹配用 LOWER(col) LIKE，
// 不使用任何 PG 专有的 ILIKE 或表达式索引。搜索覆盖 fields_json 的全部字段文本，
// 因此正面与背面都能命中；标签用带引号边界的 LIKE 精确匹配数组元素。
func (s *NoteStore) List(ctx context.Context, opts NoteListOptions) ([]Note, int64, error) {
	perPage := opts.PerPage
	if perPage <= 0 {
		perPage = DefaultNotePageSize
	}
	page := opts.Page
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * perPage

	// 每次调用都重新构造查询，避免复用同一个 *gorm.DB 时把 Count 的语句状态带到 Find。
	build := func() *gorm.DB {
		q := s.db.WithContext(ctx).Model(&Note{})
		switch opts.Status {
		case NoteStatusDeleted:
			q = q.Unscoped().Where("notes.deleted_at IS NOT NULL")
		case NoteStatusAll:
			q = q.Unscoped()
		default:
			// 默认由 GORM 的软删除作用域排除 deleted_at 非空的行。
		}
		q = q.Where("notes.deck_id = ?", opts.DeckID)
		if v := strings.TrimSpace(opts.Kind); v != "" {
			q = q.Where("notes.kind = ?", v)
		}
		if v := strings.TrimSpace(opts.Query); v != "" {
			// LOWER 与 LIKE 两库语义一致；通配符转义后按字面匹配。
			q = q.Where("LOWER(notes.fields_json) LIKE ? ESCAPE '\\'",
				"%"+strings.ToLower(escapeLike(v))+"%")
		}
		if v := strings.TrimSpace(opts.Tag); v != "" {
			q = q.Where("LOWER(notes.tags_json) LIKE ? ESCAPE '\\'",
				"%\""+strings.ToLower(escapeLike(v))+"\"%")
		}
		return q
	}

	var total int64
	if err := build().Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count notes for deck %d: %w", opts.DeckID, err)
	}
	var notes []Note
	if err := build().Order("notes.created_at DESC, notes.id DESC").
		Limit(perPage).Offset(offset).Find(&notes).Error; err != nil {
		return nil, 0, fmt.Errorf("list notes for deck %d: %w", opts.DeckID, err)
	}
	return notes, total, nil
}

// DeleteMany 软删除一组 note（只写 notes.deleted_at，不碰 cards 行）。
// 已删除或不存在的 id 不计入返回的条数；ids 为空时直接返回 (0, nil)。
func (s *NoteStore) DeleteMany(ctx context.Context, ids []uint64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	res := s.db.WithContext(ctx).Where("id IN ?", ids).Delete(&Note{})
	if res.Error != nil {
		return 0, fmt.Errorf("delete notes %v: %w", ids, res.Error)
	}
	return res.RowsAffected, nil
}

// AddTags 给一组 note 追加同一个标签，跳过已含该标签的行（幂等）。
// 返回实际发生变化的行数；只改 tags_json 与 updated_at，不触碰字段与 cards。
func (s *NoteStore) AddTags(ctx context.Context, ids []uint64, tag string) (int64, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return 0, ErrNoteTagRequired
	}
	if len(ids) == 0 {
		return 0, nil
	}
	var notes []Note
	if err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&notes).Error; err != nil {
		return 0, fmt.Errorf("load notes for tagging: %w", err)
	}
	now := time.Now().UTC()
	var changed int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i := range notes {
			tags, err := ParseTags(notes[i].TagsJSON)
			if err != nil {
				return fmt.Errorf("note %d: %w", notes[i].ID, err)
			}
			if containsString(tags, tag) {
				continue
			}
			raw, err := json.Marshal(append(tags, tag))
			if err != nil {
				return fmt.Errorf("encode tags for note %d: %w", notes[i].ID, err)
			}
			if err := tx.Model(&Note{}).Where("id = ?", notes[i].ID).
				Updates(map[string]any{"tags_json": string(raw), "updated_at": now}).Error; err != nil {
				return fmt.Errorf("update tags for note %d: %w", notes[i].ID, err)
			}
			changed++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return changed, nil
}

// ParseTags 解码 tags_json；空串按空数组处理，坏数据返回可读英文错误。
func ParseTags(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{}, nil
	}
	var tags []string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		return nil, fmt.Errorf("decode tags_json: %w", err)
	}
	return tags, nil
}

// ParseFields 解码 fields_json 成字段映射，供列表摘要与编辑页回填使用。
func ParseFields(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}, nil
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return nil, fmt.Errorf("decode fields_json: %w", err)
	}
	return fields, nil
}

// escapeLike 转义 LIKE 模式里的通配符，让用户输入按字面匹配（配合 ESCAPE '\\'）。
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// containsString 判断字符串切片是否含目标值。
func containsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
