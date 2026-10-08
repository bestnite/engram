package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// newGormLogger 让 GORM 的诊断也走标准库 log（英文）。
// IgnoreRecordNotFoundError 是必需的：schema_version 首次读取、按条件查询无结果都属于正常流程，
// 默认配置会把它们打成错误行，掩盖真正的故障。
func newGormLogger() gormlogger.Interface {
	return gormlogger.New(log.New(os.Stderr, "", log.LstdFlags), gormlogger.Config{
		SlowThreshold:             200 * time.Millisecond,
		LogLevel:                  gormlogger.Warn,
		IgnoreRecordNotFoundError: true,
		Colorful:                  false,
	})
}

// Open 按 DB_DRIVER 打开数据库连接；只支持两种驱动。
func Open(driver, dsn string) (*gorm.DB, error) {
	var dialector gorm.Dialector
	switch driver {
	case "sqlite":
		// sqlite 的父目录不存在时先幂等创建，避免 SQLite 给出误导性的 "out of memory (14)"
		// （理由见 dsn.go 顶部注释）。只对 sqlite 生效。
		if err := prepareSQLiteDir(dsn); err != nil {
			return nil, err
		}
		dialector = sqlite.Open(dsn)
	case "postgres":
		dialector = postgres.Open(dsn)
	default:
		return nil, fmt.Errorf("unsupported DB_DRIVER %q: use \"sqlite\" or \"postgres\"", driver)
	}
	db, err := gorm.Open(dialector, &gorm.Config{Logger: newGormLogger()})
	if err != nil {
		if driver == "sqlite" {
			return nil, sqliteOpenError(sqliteDBPath(dsn), err)
		}
		return nil, fmt.Errorf("open database: %w", err)
	}
	if driver == "sqlite" {
		// SQLite 单写连接即可串行化写入，避免 database is locked（依赖这一点）。
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
