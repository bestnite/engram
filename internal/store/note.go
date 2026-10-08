package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/cardtype"
)

var (
	// ErrNoteDeckRequired 表示 note 未指定所属卡组。
	ErrNoteDeckRequired = errors.New("note deck is required")
	// ErrNoteKindRequired 表示 note 未指定题型。
	ErrNoteKindRequired = errors.New("note kind is required")
	// ErrNoteTagRequired 表示批量加标签时未给出标签名。
	ErrNoteTagRequired = errors.New("note tag is required")
)

// 列表状态取值：active 是默认（排除软删除），deleted 只看已软删除，
// all 两者都看。用稳定英文常量，避免在 handler 里散落裸字符串。
const (
	NoteStatusActive  = "active"
	NoteStatusDeleted = "deleted"
	NoteStatusAll     = "all"
)

// DefaultNotePageSize 是卡片列表的每页条数；设计只要求分页可用，不暴露给用户配置。
const DefaultNotePageSize = 50

// NoteListOptions 是卡片列表的查询条件（分页、搜索、标签与题型筛选）。
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

// NormalizeNoteListOptions 把分页默认值与下界收敛成一处：Page 小于 1 按 1，
// PerPage 非正按 DefaultNotePageSize。REST handler、MCP 工具、service 与 store.List
// 四条入口都先过这里，避免同一规则在多处各写一遍（写多了必然漂移）。
func NormalizeNoteListOptions(opts NoteListOptions) NoteListOptions {
	if opts.Page < 1 {
		opts.Page = 1
	}
	if opts.PerPage <= 0 {
		opts.PerPage = DefaultNotePageSize
	}
	return opts
}

// NoteStore 封装 notes 表，并实现\"由题型生成 cards\"的管线。
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

// actorKey 是 note 写入校验用的 context 键：本次写入的执行者用户 id。
type actorKey struct{}

// WithActor 标注本次 note 写入的执行者，供写前校验判定「新引入的引用是否我可读」。
//
// 为什么不把 actor 做成写入方法的显式参数：note 写入被 web / REST / MCP / 包导入 / 克隆 / CLI
// 以多种形态调用，actor 走 context 能保持写入方法签名不变，同时让「写前校验」与「映射重建」这对
// 规则只有一处实现。**所有对外写入入口都必须先 WithActor**——否则校验会被静默跳过（逐入口的
// 越权注入用例正是守着这一点）。actor 为 0（未标注）表示服务端自身写入，跳过校验。
func WithActor(ctx context.Context, userID uint64) context.Context {
	return context.WithValue(ctx, actorKey{}, userID)
}

// actorFromContext 取本次写入的执行者；未标注时返回 0（服务端自身写入）。
func actorFromContext(ctx context.Context) uint64 {
	v, _ := ctx.Value(actorKey{}).(uint64)
	return v
}

// Save 是 note 写入的唯一入口：在同一事务内完成写前校验、写入（新建或更新）与映射重建。
//
// n.ID == 0 表示新建，否则表示更新。写前校验与 media_notes 重建都收敛在这里，因此
// web 编辑器 / REST / MCP / 包导入 / 克隆 / CLI 只要调用它（或它的 InTx 形态），规则就不会漂移。
func (s *NoteStore) Save(ctx context.Context, n *Note, fields map[string]any) ([]Card, error) {
	var cards []Card
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		created, err := s.SaveInTx(ctx, tx, n, fields)
		cards = created
		return err
	})
	if err != nil {
		return nil, err
	}
	return cards, nil
}

// SaveInTx 在调用方给定的事务里执行 note 写入（新建或更新），复用外部事务。
// 与 Save 的差别只是事务归属：批量导入把多条 note 放进同一事务，由调用方决定何时提交/回滚。
func (s *NoteStore) SaveInTx(ctx context.Context, tx *gorm.DB, n *Note, fields map[string]any) ([]Card, error) {
	if n == nil {
		return nil, errors.New("save note: note is required")
	}
	if n.ID == 0 {
		return s.createInTx(ctx, tx, n, fields)
	}
	return s.updateInTx(ctx, tx, n, fields)
}

// Create 新建一条 note；等价于 Save（n.ID 为 0）。保留它是为了让「建 note」的调用点读起来明确。
func (s *NoteStore) Create(ctx context.Context, n *Note, fields map[string]any) ([]Card, error) {
	return s.Save(ctx, n, fields)
}

// CreateInTx 在调用方给定的事务里新建一条 note（批量导入按批提交用）；等价于 SaveInTx。
func (s *NoteStore) CreateInTx(ctx context.Context, tx *gorm.DB, n *Note, fields map[string]any) ([]Card, error) {
	return s.SaveInTx(ctx, tx, n, fields)
}

// createInTx 在一个事务里写入一条新 note：先做写前校验（本次新引入的引用必须可读），
// 写入 note 与 cards，最后重建它的 media_notes 映射。
// 任一步失败则整体回滚，不会留下「有 note 无 card」或「映射与字段不一致」的半截状态。
func (s *NoteStore) createInTx(ctx context.Context, tx *gorm.DB, n *Note, fields map[string]any) ([]Card, error) {
	if n.DeckID == 0 {
		return nil, ErrNoteDeckRequired
	}
	wanted, err := s.prepareNoteFields(n, fields)
	if err != nil {
		return nil, err
	}
	// 写前校验：对写入之前的状态求值（新建时旧引用集为空）。
	if err := checkNewMediaRefs(ctx, tx, actorFromContext(ctx), nil, fields); err != nil {
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
	if err := rebuildMediaNotes(ctx, tx, n.ID, fields); err != nil {
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
	return s.Save(ctx, n, fields)
}

// UpdateInTx 在调用方给定的事务里更新 note 与它的 cards（批量导入按批提交用）。
// 与 Update 语义一致，只是复用外部事务；现有 card 的 id 与用户进度保持不变。
func (s *NoteStore) UpdateInTx(ctx context.Context, tx *gorm.DB, n *Note, fields map[string]any) ([]Card, error) {
	return s.SaveInTx(ctx, tx, n, fields)
}

// updateInTx 在一个事务里更新 note：先读旧内容算「旧引用集」，对写入之前的状态做写前校验
// （本次新引入的引用必须可读），写入 note 与 cards，最后**重建**它的 media_notes 映射。
func (s *NoteStore) updateInTx(ctx context.Context, tx *gorm.DB, n *Note, fields map[string]any) ([]Card, error) {
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
	// 写前校验：对写入之前的状态求值——旧引用集来自库里现有的字段，校验通过才写库。
	oldFields, perr := ParseFields(existing.FieldsJSON)
	if perr != nil {
		// 坏字段按空集处理：不因此放行新引用，也不因既有脏数据阻断编辑。
		oldFields = nil
	}
	if err := checkNewMediaRefs(ctx, tx, actorFromContext(ctx), oldFields, fields); err != nil {
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
	if err := rebuildMediaNotes(ctx, tx, n.ID, fields); err != nil {
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

// RestoreInTx 在调用方给定的事务里恢复软删除的 note（批量导入按批提交用：
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

// List 按条件分页列出卡组的 notes，返回当页行与符合条件的总数。
//
// 查询约定：分页统一 LIMIT/OFFSET；模糊匹配用 LOWER(col) LIKE，
// 不使用任何 PG 专有的 ILIKE 或表达式索引。搜索覆盖 fields_json 的全部字段文本，
// 因此正面与背面都能命中；标签用带引号边界的 LIKE 精确匹配数组元素。
func (s *NoteStore) List(ctx context.Context, opts NoteListOptions) ([]Note, int64, error) {
	opts = NormalizeNoteListOptions(opts)
	perPage := opts.PerPage
	page := opts.Page
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
// dryRun 只统计「会被删除的行数」，不执行任何写操作。
func (s *NoteStore) DeleteMany(ctx context.Context, ids []uint64, dryRun bool) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	if dryRun {
		var n int64
		if err := s.db.WithContext(ctx).Model(&Note{}).Where("id IN ?", ids).Count(&n).Error; err != nil {
			return 0, fmt.Errorf("count notes to delete: %w", err)
		}
		return n, nil
	}
	var affected int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Where("id IN ?", ids).Delete(&Note{})
		if res.Error != nil {
			return fmt.Errorf("delete notes %v: %w", ids, res.Error)
		}
		affected = res.RowsAffected
		return nil
	})
	if err != nil {
		return 0, err
	}
	return affected, nil
}

// tagOp 选择标签原语的合并方式；三个原语都收敛到 applyTags 一处实现。
type tagOp int

const (
	tagOpAdd tagOp = iota
	tagOpRemove
	tagOpSet
)

// AddTags 给一组 note 追加标签（并集），跳过结果与旧值相同的行（幂等）。
// 返回实际发生变化的行数；只改 tags_json 与 updated_at，不触碰字段与 cards。
func (s *NoteStore) AddTags(ctx context.Context, ids []uint64, tags []string, dryRun bool) (int64, error) {
	return s.applyTags(ctx, ids, tags, tagOpAdd, dryRun)
}

// RemoveTags 从一组 note 移除给定标签（差集，保持原有顺序）。
// 返回实际发生变化的行数；只改 tags_json 与 updated_at，不触碰字段与 cards。
func (s *NoteStore) RemoveTags(ctx context.Context, ids []uint64, tags []string, dryRun bool) (int64, error) {
	return s.applyTags(ctx, ids, tags, tagOpRemove, dryRun)
}

// SetTags 用给定列表整体替换一组 note 的标签（保持列表顺序）。
// 返回实际发生变化的行数；只改 tags_json 与 updated_at，不触碰字段与 cards。
func (s *NoteStore) SetTags(ctx context.Context, ids []uint64, tags []string, dryRun bool) (int64, error) {
	return s.applyTags(ctx, ids, tags, tagOpSet, dryRun)
}

// applyTags 是三个标签原语的唯一实现。先把 tags 规范化（见 NormalizeTags），
// 再逐行算出新值：与旧值相同则跳过（不写库、不计入返回值），不同才写 tags_json + updated_at。
// 所有写操作在同一个事务里，任一行失败整体回滚；dryRun 时只计数、不执行任何 UPDATE。
func (s *NoteStore) applyTags(ctx context.Context, ids []uint64, tags []string, op tagOp, dryRun bool) (int64, error) {
	clean := NormalizeTags(tags)
	if len(clean) == 0 {
		return 0, ErrNoteTagRequired
	}
	if len(ids) == 0 {
		return 0, nil
	}
	var notes []Note
	if err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&notes).Error; err != nil {
		return 0, fmt.Errorf("load notes for tagging: %w", err)
	}
	type tagChange struct {
		id  uint64
		raw string
	}
	changes := make([]tagChange, 0, len(notes))
	for i := range notes {
		old, err := ParseTags(notes[i].TagsJSON)
		if err != nil {
			return 0, fmt.Errorf("note %d: %w", notes[i].ID, err)
		}
		var next []string
		switch op {
		case tagOpAdd:
			next = addTags(old, clean)
		case tagOpRemove:
			next = removeTags(old, clean)
		default:
			next = append([]string{}, clean...)
		}
		if equalTags(old, next) {
			continue
		}
		raw, err := json.Marshal(next)
		if err != nil {
			return 0, fmt.Errorf("encode tags for note %d: %w", notes[i].ID, err)
		}
		changes = append(changes, tagChange{id: notes[i].ID, raw: string(raw)})
	}
	if dryRun {
		return int64(len(changes)), nil
	}
	now := time.Now().UTC()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, ch := range changes {
			if err := tx.Model(&Note{}).Where("id = ?", ch.id).
				Updates(map[string]any{"tags_json": ch.raw, "updated_at": now}).Error; err != nil {
				return fmt.Errorf("update tags for note %d: %w", ch.id, err)
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return int64(len(changes)), nil
}

// NormalizeTags 规范化标签列表：去掉首尾空白、丢弃空串、按首次出现去重并保持顺序。
// store 的三个标签原语与 REST/MCP 的批量服务共用它，保证「加/减/设」与请求校验对同一
// 标签的判定完全一致（同一条规则只改一处）。
func NormalizeTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" || containsString(out, t) {
			continue
		}
		out = append(out, t)
	}
	return out
}

// addTags 返回 old 与 add 的并集：保留 old 的顺序，再按 add 的顺序追加未见过的标签。
func addTags(old, add []string) []string {
	out := append([]string{}, old...)
	for _, t := range add {
		if !containsString(out, t) {
			out = append(out, t)
		}
	}
	return out
}

// removeTags 返回 old 去掉 remove 中标签后的结果，保持 old 的顺序。
func removeTags(old, remove []string) []string {
	out := make([]string, 0, len(old))
	for _, t := range old {
		if !containsString(remove, t) {
			out = append(out, t)
		}
	}
	return out
}

// equalTags 判断两个标签列表是否完全相同（顺序敏感）；nil 与空列表视为相等。
func equalTags(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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

// TagsJSON 把标签切片序列化成 notes.tags_json；nil 落成 "[]"，避免列里出现空串。
// 这是唯一的序列化入口，卡组包导出与 REST 写入共用（输出与逐处手写一致）。
func TagsJSON(tags []string) string {
	if tags == nil {
		tags = []string{}
	}
	raw, err := json.Marshal(tags)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

// FieldsOrEmpty 解码 fields_json；坏数据或 null 收敛成空对象，避免响应里出现 null。
// 列表、到期卡与导出三条读取路径共用，保证同一行在不同出口产出同一形态。
func FieldsOrEmpty(raw string) map[string]any {
	fields, err := ParseFields(raw)
	if err != nil || fields == nil {
		return map[string]any{}
	}
	return fields
}

// TagsOrEmpty 解码 tags_json；坏数据或 null 收敛成空数组。
func TagsOrEmpty(raw string) []string {
	tags, err := ParseTags(raw)
	if err != nil || tags == nil {
		return []string{}
	}
	return tags
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
