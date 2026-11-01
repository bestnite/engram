package store

import (
	"fmt"
	"log/slog"

	"gorm.io/gorm"
)

// repairPresetSchedulerLimits logs the number of preset rows changed, counting each row once
// even when several fields need repair. It leaves all other columns, including updated_at, intact.
func repairPresetSchedulerLimits(tx *gorm.DB) error {
	if !tx.Migrator().HasTable("presets") {
		return nil
	}
	var last uint64
	var changed int64
	for {
		var presets []Preset
		if err := tx.Select("id", "maximum_interval_days", "learning_steps", "relearning_steps").
			Where("id > ?", last).Order("id ASC").Limit(500).Find(&presets).Error; err != nil {
			return fmt.Errorf("repair preset limits: scan after id %d: %w", last, err)
		}
		if len(presets) == 0 {
			slog.Info("repaired preset scheduler limits", "rows_changed", changed)
			return nil
		}
		for _, p := range presets {
			last = p.ID
			updates := map[string]any{}
			if p.MaximumIntervalDays > DefaultMaximumIntervalDays {
				updates["maximum_interval_days"] = DefaultMaximumIntervalDays
			}
			if !validPackageSteps(p.LearningSteps) {
				updates["learning_steps"] = DefaultLearningSteps
			}
			if !validPackageSteps(p.RelearningSteps) {
				updates["relearning_steps"] = DefaultRelearningSteps
			}
			if len(updates) == 0 {
				continue
			}
			result := tx.Model(&Preset{}).Where("id = ?", p.ID).UpdateColumns(updates)
			if result.Error != nil {
				return fmt.Errorf("repair preset limits: preset %d: %w", p.ID, result.Error)
			}
			changed += result.RowsAffected
		}
	}
}
