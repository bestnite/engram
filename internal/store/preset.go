package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/cardtype"
)

// DefaultPresetName 是默认预设的稳定标识与创建名。
//
// 它是**存储数据而不是 UI 文案**：把它本地化正是「同一用户攒出两个默认预设」这一缺陷的根源
// web 建组用本地化名、REST 建组用字面量时，两条入口对同一条逻辑预设给出不同名字，
// 谁都找不到对方建的那条。这里定义唯一字面量，所有入口共用，语言包不再参与预设名。
const DefaultPresetName = "Default"

// ErrNoDefaultPreset 表示 EnsureDefaultPreset 之后仍未找到默认预设；出现即 store 契约被破坏。
var ErrNoDefaultPreset = errors.New("default preset missing after ensure")

// 调度参数的文档化默认值。
// 这些数值同时是 decks 新建时的预填值；learning_steps 允许为空串表示关闭学习步骤，
// 因此默认值只在构造时给出，Create 不再把它改写回来。
const (
	DefaultDesiredRetention    = 0.90
	DefaultLearningSteps       = "1m,10m"
	DefaultRelearningSteps     = "10m"
	DefaultMaximumIntervalDays = 36500
	DefaultEnableFuzz          = true
)

var (
	// ErrPresetNameRequired 表示预设名为空。
	ErrPresetNameRequired = errors.New("preset name is required")
	// ErrInvalidDesiredRetention 表示目标保留率不在 (0, 1] 内。
	ErrInvalidDesiredRetention = errors.New("desired retention must be in (0, 1]")
	// ErrInvalidMaximumInterval 表示最大间隔天数非正。
	ErrInvalidMaximumInterval = errors.New("maximum interval days must be positive")
	// ErrPresetInUse 表示预设正被一个或多个卡组使用，不可删除。
	ErrPresetInUse = errors.New("preset is in use by one or more decks")
	// ErrDefaultPresetCannotDelete 表示默认预设不可删除。
	ErrDefaultPresetCannotDelete = errors.New("default preset cannot be deleted")
	// ErrDefaultPresetCannotRename 表示默认预设不可改名。
	//
	// 默认预设的身份就是 DefaultPresetName 这个字面量（DefaultPreset 按名字查找、删除保护也按名字判），
	// 改名会让身份失配：此后 EnsureDefaultPreset 找不到它，会再补一条同名的默认预设，
	// 于是同一用户名下出现两条「默认」。
	ErrDefaultPresetCannotRename = errors.New("default preset cannot be renamed")
)

// NewPreset 返回一个带文档化默认值的调度预设；归属与名字由调用方给出。
// 这是获得默认值的唯一入口：直接构造 Preset 会得到零值，Create 会以校验失败拒绝，
// 避免把 desired_retention=0 这类非法值静默写库。
func NewPreset(ownerUserID uint64, name string) Preset {
	return Preset{
		OwnerUserID:         ownerUserID,
		Name:                name,
		DesiredRetention:    DefaultDesiredRetention,
		LearningSteps:       DefaultLearningSteps,
		RelearningSteps:     DefaultRelearningSteps,
		MaximumIntervalDays: DefaultMaximumIntervalDays,
		EnableFuzz:          Ptr(DefaultEnableFuzz),
	}
}

// FuzzEnabled 返回 enable_fuzz 的有效值：NULL 视为默认 true（AGENTS.md §2.3 第 9 条）。
// 所有读取方都走这里，避免各处重复处理 nil。
func (p *Preset) FuzzEnabled() bool { return p.EnableFuzz == nil || *p.EnableFuzz }

// requirePresetOwner 与卡组共用 ErrNotOwner：预设仍属创建者，授权表不涉及 preset。
func requirePresetOwner(p *Preset, actorUserID uint64) error {
	if p.OwnerUserID != actorUserID {
		return fmt.Errorf("%w: preset %d is owned by user %d", ErrNotOwner, p.ID, p.OwnerUserID)
	}
	return nil
}

// validatePresetForWrite 校验可写字段。
func validatePresetForWrite(p *Preset, create bool) error {
	if p.Name == "" {
		return ErrPresetNameRequired
	}
	if p.DesiredRetention <= 0 || p.DesiredRetention > 1 {
		return fmt.Errorf("%w: %v", ErrInvalidDesiredRetention, p.DesiredRetention)
	}
	if p.MaximumIntervalDays <= 0 {
		return fmt.Errorf("%w: %d", ErrInvalidMaximumInterval, p.MaximumIntervalDays)
	}
	if create && p.OwnerUserID == 0 {
		return errors.New("preset owner is required")
	}
	return nil
}

// PresetStore 封装 presets 表的 GORM 访问。
type PresetStore struct {
	db *gorm.DB
}

// NewPresetStore 构造预设存储。
func NewPresetStore(db *gorm.DB) *PresetStore { return &PresetStore{db: db} }

// ensurePresetMu 串行化进程内的默认预设补齐。
//
// presets 表只有 owner_user_id 索引，没有 (owner_user_id, name) 唯一约束（已核实 AutoMigrate
// 的模型标签）。补唯一索引属于结构变更，要先按 AGENTS.md §2.3.5 走显式、版本化迁移并先给历史
// 数据去重，因此这里不退而求其次地用 clause.OnConflict。进程内用互斥串行化：两个请求同时为
// 同一用户补齐时，后到的那个在锁内再查，命中第一条即返回，不会建出两条，也不会报错。
// 跨进程并发（多实例部署）仍可能各建一条，属已知残余；真正根治需要上面的唯一索引迁移。
var ensurePresetMu sync.Mutex

// EnsureDefaultPreset 是该用户默认预设的唯一保障入口：保证 ownerUserID 名下至少有一条名为
// DefaultPresetName 的预设，并返回其全部预设（按创建时间倒序）。
//
// web 与 REST/MCP 都经这里补齐默认预设，预设名不再本地化，因此同一用户不会再因入口不同而攒出
// 两条「默认」预设。
func EnsureDefaultPreset(ctx context.Context, db *gorm.DB, ownerUserID uint64) ([]Preset, error) {
	presets, err := NewPresetStore(db).ListByOwner(ctx, ownerUserID)
	if err != nil {
		return nil, err
	}
	if DefaultPreset(presets) != nil {
		return presets, nil
	}

	ensurePresetMu.Lock()
	defer ensurePresetMu.Unlock()

	// 事务内再查：拿到锁之前可能已有其它请求补上，避免重复创建。
	var out []Preset
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		s := NewPresetStore(tx)
		list, err := s.ListByOwner(ctx, ownerUserID)
		if err != nil {
			return err
		}
		if DefaultPreset(list) != nil {
			out = list
			return nil
		}
		p := NewPreset(ownerUserID, DefaultPresetName)
		if err := s.Create(ctx, &p); err != nil {
			return err
		}
		out = append([]Preset{p}, list...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DefaultPreset 返回列表中名为 DefaultPresetName 的预设；没有则返回 nil。
func DefaultPreset(presets []Preset) *Preset {
	for i := range presets {
		if presets[i].Name == DefaultPresetName {
			return &presets[i]
		}
	}
	return nil
}

// Create 写入一个预设。
//
// EnableFuzz 是 *bool：nil 时 GORM 省略该列、由数据库默认值 true 补齐，非 nil 时显式写入
// （含 false）。补偿写入已按 AGENTS.md §2.3 第 9 条删除——语义由字段类型本身保证。
func (s *PresetStore) Create(ctx context.Context, p *Preset) error {
	if err := validatePresetForWrite(p, true); err != nil {
		return err
	}
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = now
	}
	if err := s.db.WithContext(ctx).Create(p).Error; err != nil {
		return fmt.Errorf("create preset: %w", err)
	}
	return nil
}

// ByID 按主键取预设。
func (s *PresetStore) ByID(ctx context.Context, id uint64) (*Preset, error) {
	var p Preset
	if err := s.db.WithContext(ctx).First(&p, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

// ListByOwner 列出某用户名下的预设，按创建时间倒序。
func (s *PresetStore) ListByOwner(ctx context.Context, ownerUserID uint64) ([]Preset, error) {
	var presets []Preset
	if err := s.db.WithContext(ctx).Where("owner_user_id = ?", ownerUserID).
		Order("created_at DESC, id DESC").Find(&presets).Error; err != nil {
		return nil, fmt.Errorf("list presets by owner: %w", err)
	}
	return presets, nil
}

// Update 修改预设的调度参数；只有 owner 能改。默认预设可以改参数，但不可改名——
// 它的身份就是 DefaultPresetName 这个字面量，改名会让后续补齐再建一条同名预设。
func (s *PresetStore) Update(ctx context.Context, actorUserID uint64, p *Preset) error {
	if p.ID == 0 {
		return errors.New("update preset: id is required")
	}
	existing, err := s.ByID(ctx, p.ID)
	if err != nil {
		return err
	}
	if err := requirePresetOwner(existing, actorUserID); err != nil {
		return err
	}
	if err := validatePresetForWrite(p, false); err != nil {
		return err
	}
	if existing.Name == DefaultPresetName && p.Name != DefaultPresetName {
		return ErrDefaultPresetCannotRename
	}
	updates := map[string]any{
		"name":                  p.Name,
		"desired_retention":     p.DesiredRetention,
		"learning_steps":        p.LearningSteps,
		"relearning_steps":      p.RelearningSteps,
		"maximum_interval_days": p.MaximumIntervalDays,
		"enable_fuzz":           p.FuzzEnabled(),
		"weights_json":          p.WeightsJSON,
		"weights_optimized_at":  p.WeightsOptimizedAt,
		"weights_review_count":  p.WeightsReviewCount,
		"grade_mapping_json":    p.GradeMappingJSON,
		"updated_at":            time.Now().UTC(),
	}
	if err := s.db.WithContext(ctx).Model(&Preset{}).Where("id = ?", p.ID).Updates(updates).Error; err != nil {
		return fmt.Errorf("update preset: %w", err)
	}
	return nil
}

// GradeMapping 解析该预设的「分数→评分档位」映射。
// GradeMappingJSON 为 NULL 时回退到 cardtype.DefaultGradeMapping（全对 Good / 部分对 Hard /
// 全错 Again），因此未配置映射的 preset 也能直接判分。
func (p *Preset) GradeMapping() (cardtype.GradeMapping, error) {
	raw := ""
	if p.GradeMappingJSON != nil {
		raw = *p.GradeMappingJSON
	}
	return cardtype.ParseGradeMapping(raw)
}

// SetGradeMapping 校验并写入映射 JSON，供上层在保存 preset 时调用。
func (p *Preset) SetGradeMapping(m cardtype.GradeMapping) error {
	raw, err := cardtype.MarshalGradeMapping(m)
	if err != nil {
		return err
	}
	p.GradeMappingJSON = &raw
	return nil
}

// Delete 删除预设；只有 owner 能删。默认预设不可删除，正被卡组引用的预设不可删除。
func (s *PresetStore) Delete(ctx context.Context, actorUserID, presetID uint64) error {
	existing, err := s.ByID(ctx, presetID)
	if err != nil {
		return err
	}
	if err := requirePresetOwner(existing, actorUserID); err != nil {
		return err
	}
	if existing.Name == DefaultPresetName {
		return ErrDefaultPresetCannotDelete
	}
	var count int64
	if err := s.db.WithContext(ctx).Model(&Deck{}).Where("preset_id = ?", presetID).Count(&count).Error; err != nil {
		return fmt.Errorf("check preset usage: %w", err)
	}
	if count > 0 {
		return ErrPresetInUse
	}
	if err := s.db.WithContext(ctx).Delete(&Preset{}, "id = ?", presetID).Error; err != nil {
		return fmt.Errorf("delete preset %d: %w", presetID, err)
	}
	return nil
}
