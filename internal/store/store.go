package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Open 按 DB_DRIVER 打开数据库连接；只支持两种驱动（DESIGN.md §10.1）。
func Open(driver, dsn string) (*gorm.DB, error) {
	var dialector gorm.Dialector
	switch driver {
	case "sqlite":
		dialector = sqlite.Open(dsn)
	case "postgres":
		dialector = postgres.Open(dsn)
	default:
		return nil, fmt.Errorf("unsupported DB_DRIVER %q: use \"sqlite\" or \"postgres\"", driver)
	}
	db, err := gorm.Open(dialector, &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if driver == "sqlite" {
		// SQLite 单写连接即可串行化写入，避免 database is locked（DESIGN.md §3.4 依赖这一点）。
		sqlDB, err := db.DB()
		if err != nil {
			return nil, fmt.Errorf("get sql.DB: %w", err)
		}
		sqlDB.SetMaxOpenConns(1)
	}
	return db, nil
}

// LoadSettings 读出 settings 表覆盖值；value 是 JSON 编码文本，字符串值在这里解码。
func LoadSettings(ctx context.Context, db *gorm.DB) (map[string]string, error) {
	var rows []Setting
	if err := db.WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load settings: %w", err)
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		var decoded string
		if err := json.Unmarshal([]byte(row.Value), &decoded); err == nil {
			out[row.Key] = decoded
			continue
		}
		// 不是 JSON 字符串（例如手工写入的裸值）时按原样使用。
		out[row.Key] = row.Value
	}
	return out, nil
}
