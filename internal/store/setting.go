package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The write channel for ordinary (non-sensitive) system settings. The read side is LoadSettings in store.go;
// written values are JSON-encoded too, so both sides share one convention. Sensitive values go through PutSecret in secret.go
// and must not pass through here, otherwise writing ciphertext as plaintext breaks the encrypted-storage requirement for sensitive values.
func PutSetting(ctx context.Context, db *gorm.DB, key, value string, updatedBy *uint64, at time.Time) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("store: setting key is required")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode setting %q: %w", key, err)
	}
	row := Setting{Key: key, Value: string(encoded), UpdatedBy: updatedBy, UpdatedAt: at.UTC()}
	if err := db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updated_by", "updated_at"}),
	}).Create(&row).Error; err != nil {
		return fmt.Errorf("store setting %q: %w", key, err)
	}
	return nil
}

// SensitiveSettingKeys returns every sensitive key in the settings table (the suffix convention is in secret.go),
// alphabetically. The admin panel uses it to show "configured/not configured" per item, never touching the plaintext.
func SensitiveSettingKeys(ctx context.Context, db *gorm.DB) ([]string, error) {
	var rows []Setting
	if err := db.WithContext(ctx).Model(&Setting{}).Select("key").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list setting keys: %w", err)
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if IsSensitiveSettingKey(row.Key) {
			out = append(out, row.Key)
		}
	}
	sort.Strings(out)
	return out, nil
}
