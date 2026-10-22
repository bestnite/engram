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

// newGormLogger makes GORM's diagnostics go through the standard library log too (English).
// IgnoreRecordNotFoundError is required: the first read of schema_version and a filtered query with no rows are both normal flow,
// and the default configuration logs them as error lines, masking real faults.
func newGormLogger() gormlogger.Interface {
	return gormlogger.New(log.New(os.Stderr, "", log.LstdFlags), gormlogger.Config{
		SlowThreshold:             200 * time.Millisecond,
		LogLevel:                  gormlogger.Warn,
		IgnoreRecordNotFoundError: true,
		Colorful:                  false,
	})
}

// Open opens the database connection per DB_DRIVER; only two drivers are supported.
func Open(driver, dsn string) (*gorm.DB, error) {
	var dialector gorm.Dialector
	switch driver {
	case "sqlite":
		// Idempotently create SQLite's parent directory when missing, so SQLite does not report a misleading "out of memory (14)"
		// (the reasoning is in the comment at the top of dsn.go). This only applies to sqlite.
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
		// For SQLite a single write connection is enough to serialize writes and avoid "database is locked" (this is relied upon).
		sqlDB, err := db.DB()
		if err != nil {
			return nil, fmt.Errorf("get sql.DB: %w", err)
		}
		sqlDB.SetMaxOpenConns(1)
	}
	return db, nil
}

// LoadSettings reads the override values out of the settings table; value is JSON-encoded text and string values are decoded here.
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
		// When it is not a JSON string (a hand-written raw value, say) it is used as is.
		out[row.Key] = row.Value
	}
	return out, nil
}
