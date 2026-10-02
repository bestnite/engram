package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Migration 是一次性的显式迁移，用于 AutoMigrate 做不到的破坏性变更
// （改列类型、删列、新增非空约束，DESIGN.md §2.3、AGENTS.md §2.3 第 5 条）。
// Name 是必填的稳定英文标识：没有名字的破坏性变更无法被追踪，必须被拒绝。
type Migration struct {
	Name string
	Up   func(tx *gorm.DB) error
}

// BuiltinMigrations 是有序的迁移列表，顺序即版本号（第 i 项的版本是 i+1）。
// 新增迁移只能追加到末尾，永远不要修改或重排已发布的条目。
var BuiltinMigrations = []Migration{
	// M0 阶段还没有破坏性变更；第一个真实迁移从这里往后追加。
}

// AutoMigrate 只做增量变更（加表/加列/加索引）。
// 模型清单只有一处：AllModels()。原先这里曾把 TOTP 两表单独并进来，结果 AutoMigrate 建了表、
// 而遍历 AllModels 的全库导出漏了表——两处清单必然分叉，所以并回一处。
func AutoMigrate(ctx context.Context, db *gorm.DB) error {
	models := AllModels()
	if err := db.WithContext(ctx).AutoMigrate(models...); err != nil {
		return fmt.Errorf("auto migrate: %w", err)
	}
	return nil
}

// Sync 先跑增量 AutoMigrate，再按 schema 版本执行已注册的破坏性迁移，返回本次执行的迁移数。
func Sync(ctx context.Context, db *gorm.DB, migrations []Migration) (int, error) {
	if err := AutoMigrate(ctx, db); err != nil {
		return 0, err
	}
	return Apply(ctx, db, migrations)
}

// Apply 校验迁移列表并执行尚未应用的条目。每个迁移与其版本号在同一事务里提交，
// 因此迁移成功后不可能出现"执行了但版本没记"的状态。
func Apply(ctx context.Context, db *gorm.DB, migrations []Migration) (int, error) {
	seen := make(map[string]int, len(migrations))
	for i, m := range migrations {
		if strings.TrimSpace(m.Name) == "" {
			return 0, fmt.Errorf("migration at index %d has no name: destructive changes must be named and registered", i)
		}
		if m.Up == nil {
			return 0, fmt.Errorf("migration %q has no Up function", m.Name)
		}
		if prev, dup := seen[m.Name]; dup {
			return 0, fmt.Errorf("duplicate migration name %q at index %d and %d", m.Name, prev, i)
		}
		seen[m.Name] = i
	}
	if err := db.WithContext(ctx).AutoMigrate(&SchemaVersion{}); err != nil {
		return 0, fmt.Errorf("auto migrate schema_version: %w", err)
	}
	current, err := CurrentVersion(ctx, db)
	if err != nil {
		return 0, err
	}
	applied := 0
	for i, m := range migrations {
		version := i + 1
		if version <= current {
			continue
		}
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := m.Up(tx); err != nil {
				return fmt.Errorf("migration %q failed: %w", m.Name, err)
			}
			return setVersion(tx, version)
		})
		if err != nil {
			return applied, err
		}
		current = version
		applied++
	}
	return applied, nil
}

// CurrentVersion 返回已应用的 schema 版本；还没有任何记录时返回 0。
func CurrentVersion(ctx context.Context, db *gorm.DB) (int, error) {
	var row SchemaVersion
	err := db.WithContext(ctx).Where("id = ?", 1).Take(&row).Error
	if err == gorm.ErrRecordNotFound {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return row.Version, nil
}

// setVersion 用 Save 写单行版本表，两库都会生成正确的 upsert（DESIGN.md §2.3）。
func setVersion(tx *gorm.DB, version int) error {
	row := SchemaVersion{ID: 1, Version: version, UpdatedAt: time.Now().UTC()}
	if err := tx.Save(&row).Error; err != nil {
		return fmt.Errorf("write schema version: %w", err)
	}
	return nil
}
