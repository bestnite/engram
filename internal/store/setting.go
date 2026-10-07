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

// 普通（非敏感）系统设置的写入通道。读取端是 store.go 的 LoadSettings；
// 写入值同样 JSON 编码，保证两端约定一致。敏感值走 secret.go 的 PutSecret，
// 不能经过这里，否则密文写作明文会破坏敏感值的加密存储要求。
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

// SensitiveSettingKeys 返回 settings 表里所有敏感键（后缀约定见 secret.go），
// 按字母序。管理面板据此逐项显示「已配置/未配置」，全程不接触明文。
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
