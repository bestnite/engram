package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/answer"
)

// DefaultPresetName is the stable identity and creation name of the default preset.
//
// It is **stored data, not UI copy**: localizing it is exactly the root of the "one user accumulates two default presets" defect --
// when web creates a deck with the localized name and REST with the literal, the two entry points give the same logical preset different names,
// and neither can find the row the other created. One literal is defined here, shared by all entry points, and the language catalogue no longer takes part in the preset name.
const DefaultPresetName = "Default"

// ErrNoDefaultPreset means no default preset was found even after EnsureDefaultPreset; seeing it means the store contract is broken.
var ErrNoDefaultPreset = errors.New("default preset missing after ensure")

// Documented defaults for the scheduling parameters.
// These values are also the prefill for a newly created deck; learning_steps may be an empty string to disable learning steps,
// so the defaults are only supplied at construction and Create no longer writes it back.
const (
	DefaultDesiredRetention    = 0.90
	DefaultLearningSteps       = "1m,10m"
	DefaultRelearningSteps     = "10m"
	DefaultMaximumIntervalDays = 36500
	DefaultEnableFuzz          = true
)

var (
	// ErrPresetNameRequired means the preset name is empty.
	ErrPresetNameRequired = errors.New("preset name is required")
	// ErrInvalidDesiredRetention means the desired retention is outside (0, 1].
	ErrInvalidDesiredRetention = errors.New("desired retention must be in (0, 1]")
	// ErrInvalidMaximumInterval means the maximum interval in days is not positive.
	ErrInvalidMaximumInterval = errors.New("maximum interval days must be positive")
	// ErrPresetInUse means the preset is used by one or more decks and cannot be deleted.
	ErrPresetInUse = errors.New("preset is in use by one or more decks")
	// ErrDefaultPresetCannotDelete means the default preset cannot be deleted.
	ErrDefaultPresetCannotDelete = errors.New("default preset cannot be deleted")
	// ErrDefaultPresetCannotRename means the default preset cannot be renamed.
	//
	// The default preset's identity is the DefaultPresetName literal (DefaultPreset looks it up by name and the delete protection checks by name too),
	// so renaming breaks that identity: afterwards EnsureDefaultPreset cannot find it and adds another default preset under the old name,
	// leaving two "default" presets under one user.
	ErrDefaultPresetCannotRename = errors.New("default preset cannot be renamed")
)

// NewPreset returns a scheduling preset carrying the documented defaults; the caller supplies ownership and name.
// This is the only way to get the defaults: constructing Preset directly yields zero values, which Create rejects as a validation failure,
// so illegal values such as desired_retention=0 are not silently persisted.
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

// FuzzEnabled returns the effective value of enable_fuzz: NULL counts as the default true (AGENTS.md §2.3 item 9).
// Every reader goes through here, so nil is not handled over and over in each place.
func (p *Preset) FuzzEnabled() bool { return p.EnableFuzz == nil || *p.EnableFuzz }

// requirePresetOwner shares ErrNotOwner with decks: a preset still belongs to its creator, and the authorization table does not involve presets.
func requirePresetOwner(p *Preset, actorUserID uint64) error {
	if p.OwnerUserID != actorUserID {
		return fmt.Errorf("%w: preset %d is owned by user %d", ErrNotOwner, p.ID, p.OwnerUserID)
	}
	return nil
}

// validatePresetForWrite validates the writable fields.
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

// PresetStore wraps GORM access to the presets table.
type PresetStore struct {
	db *gorm.DB
}

// NewPresetStore builds the preset store.
func NewPresetStore(db *gorm.DB) *PresetStore { return &PresetStore{db: db} }

// ensurePresetMu serializes the default-preset backfill within the process.
//
// The presets table only has an owner_user_id index, not a (owner_user_id, name) unique constraint (AutoMigrate's
// model tags were checked). Adding a unique index is a structural change: per AGENTS.md §2.3.5 it needs an explicit, versioned migration and the historical data
// de-duplicated first, so this does not fall back to clause.OnConflict. Within a process a mutex serializes it: when two requests backfill for the
// same user at the same time, the later one queries again inside the lock, returns on the first hit, and neither creates two rows nor errors.
// Cross-process concurrency (multi-instance deployments) may still create one each; that is a known residual, and the real fix needs the unique-index migration above.
var ensurePresetMu sync.Mutex

// EnsureDefaultPreset is the only entry point guaranteeing a user's default preset: it ensures ownerUserID has at least one preset named
// DefaultPresetName and returns all of that user's presets (newest first).
//
// web and REST/MCP both backfill the default preset through here, and the preset name is no longer localized, so one user no longer accumulates
// two "default" presets because of different entry points.
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

	// Query again inside the transaction: another request may have backfilled before the lock was acquired, so no duplicate is created.
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

// DefaultPreset returns the preset named DefaultPresetName from the list; nil if there is none.
func DefaultPreset(presets []Preset) *Preset {
	for i := range presets {
		if presets[i].Name == DefaultPresetName {
			return &presets[i]
		}
	}
	return nil
}

// Create writes a preset.
//
// EnableFuzz is a *bool: when nil, GORM omits the column and the database default true fills it in; when not nil it is written explicitly
// (false included). The compensatory write is gone per AGENTS.md §2.3 item 9 -- the field type itself guarantees the semantics.
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

// ByID fetches a preset by primary key.
func (s *PresetStore) ByID(ctx context.Context, id uint64) (*Preset, error) {
	var p Preset
	if err := s.db.WithContext(ctx).First(&p, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

// ListByOwner lists a user's presets, newest first.
func (s *PresetStore) ListByOwner(ctx context.Context, ownerUserID uint64) ([]Preset, error) {
	var presets []Preset
	if err := s.db.WithContext(ctx).Where("owner_user_id = ?", ownerUserID).
		Order("created_at DESC, id DESC").Find(&presets).Error; err != nil {
		return nil, fmt.Errorf("list presets by owner: %w", err)
	}
	return presets, nil
}

// Update changes a preset's scheduling parameters; only the owner can. The default preset's parameters may change but its name may not --
// its identity is the DefaultPresetName literal, and renaming would make a later backfill create another preset under that name.
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

// GradeMapping parses this preset's "score → rating" mapping.
// When GradeMappingJSON is NULL it falls back to answer.DefaultGradeMapping (all correct Good / partial Hard /
// all wrong Again), so a preset without a configured mapping can still grade.
func (p *Preset) GradeMapping() (answer.GradeMapping, error) {
	raw := ""
	if p.GradeMappingJSON != nil {
		raw = *p.GradeMappingJSON
	}
	return answer.ParseGradeMapping(raw)
}

// SetGradeMapping validates and writes the mapping JSON, for the upper layers to call when saving a preset.
func (p *Preset) SetGradeMapping(m answer.GradeMapping) error {
	raw, err := answer.MarshalGradeMapping(m)
	if err != nil {
		return err
	}
	p.GradeMappingJSON = &raw
	return nil
}

// Delete removes a preset; only the owner can. The default preset cannot be deleted, and neither can a preset a deck references.
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
	// There are two kinds of reference: decks.preset_id of one's own decks, and one's member settings on shared decks.
	var count int64
	if err := s.db.WithContext(ctx).Model(&Deck{}).Where("preset_id = ?", presetID).Count(&count).Error; err != nil {
		return fmt.Errorf("check preset usage: %w", err)
	}
	var memberCount int64
	if err := s.db.WithContext(ctx).Model(&DeckMemberSetting{}).Where("preset_id = ?", presetID).Count(&memberCount).Error; err != nil {
		return fmt.Errorf("check preset usage by member settings: %w", err)
	}
	if count > 0 || memberCount > 0 {
		return ErrPresetInUse
	}
	if err := s.db.WithContext(ctx).Delete(&Preset{}, "id = ?", presetID).Error; err != nil {
		return fmt.Errorf("delete preset %d: %w", presetID, err)
	}
	return nil
}
