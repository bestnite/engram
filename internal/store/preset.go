package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// 调度参数的文档化默认值（DESIGN.md §2.2、§3.2、§3.5）。
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
		EnableFuzz:          DefaultEnableFuzz,
	}
}

// requirePresetOwner 与卡组共用 ErrNotOwner：预设仍属创建者，M5-1 的授权表不涉及 preset。
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

// Create 写入一个预设。
//
// EnableFuzz 带 `default:true` 标签：GORM 会在字段为零值时省略该列、由数据库填默认值，
// 于是\"关闭 fuzz\"（false）会被静默改回 true。显式补一次列更新来修掉这个零值陷阱。
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
	if !p.EnableFuzz {
		if err := s.db.WithContext(ctx).Model(&Preset{}).Where("id = ?", p.ID).
			Update("enable_fuzz", false).Error; err != nil {
			return fmt.Errorf("create preset: disable fuzz: %w", err)
		}
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

// Update 修改预设的调度参数；只有 owner 能改。
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
	updates := map[string]any{
		"name":                  p.Name,
		"desired_retention":     p.DesiredRetention,
		"learning_steps":        p.LearningSteps,
		"relearning_steps":      p.RelearningSteps,
		"maximum_interval_days": p.MaximumIntervalDays,
		"enable_fuzz":           p.EnableFuzz,
		"weights_json":          p.WeightsJSON,
		"weights_optimized_at":  p.WeightsOptimizedAt,
		"weights_review_count":  p.WeightsReviewCount,
		"updated_at":            time.Now().UTC(),
	}
	if err := s.db.WithContext(ctx).Model(&Preset{}).Where("id = ?", p.ID).Updates(updates).Error; err != nil {
		return fmt.Errorf("update preset: %w", err)
	}
	return nil
}
