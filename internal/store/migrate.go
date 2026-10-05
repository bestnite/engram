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
	{
		// 媒体主键从自增 id 换成内容 sha256（DESIGN.md §6.3）：删列不在 AutoMigrate
		// 的加法范围内，必须显式迁移。这里只改主键，不迁移旧引用。
		Name: "0001_media_primary_key_sha256",
		Up:   mediaPrimaryKeySha256,
	},
}

// mediaPrimaryKeySha256 把 media 的主键从自增 id 换成内容 sha256。
//
// 两库写法不同：SQLite 不允许对主键列 DROP COLUMN，只能重建表；PostgreSQL 直接
// DROP COLUMN（会连带删掉旧主键约束）再补主键。已经是目标形状的新库（AutoMigrate
// 直接建出 sha 主键，没有 id 列）在此直接跳过。
func mediaPrimaryKeySha256(tx *gorm.DB) error {
	m := tx.Migrator()
	if !m.HasTable("media") {
		return nil
	}
	// 没有 id 列说明表已经是 sha 主键形状，无需处理。
	if !m.HasColumn("media", "id") {
		return nil
	}
	switch tx.Dialector.Name() {
	case "sqlite":
		return rebuildMediaForShaPrimaryKey(tx)
	case "postgres":
		return alterMediaForShaPrimaryKey(tx)
	default:
		return fmt.Errorf("media primary key migration: unsupported dialect %q", tx.Dialector.Name())
	}
}

// rebuildMediaForShaPrimaryKey 用「建新表 → 搬数据 → 换名」重建 media，绕开 SQLite
// 不能删主键列的限制。列定义与 GORM 为该模型生成的形状对齐（尤其 created_at 是 datetime：
// 写成 TEXT 会让驱动无法把它扫回 time.Time）。列清单与 DESIGN.md §2.2 的 media 表一致。
func rebuildMediaForShaPrimaryKey(tx *gorm.DB) error {
	stmts := []string{
		`CREATE TABLE media_sha_pk (
			sha256     text PRIMARY KEY,
			rel_path   text NOT NULL,
			mime       text NOT NULL,
			bytes      integer NOT NULL,
			width      integer,
			height     integer,
			created_by integer,
			created_at datetime NOT NULL
		)`,
		`INSERT INTO media_sha_pk (sha256, rel_path, mime, bytes, width, height, created_by, created_at)
			SELECT sha256, rel_path, mime, bytes, width, height, created_by, created_at FROM media`,
		`DROP TABLE media`,
		`ALTER TABLE media_sha_pk RENAME TO media`,
	}
	for _, stmt := range stmts {
		if err := tx.Exec(stmt).Error; err != nil {
			return err
		}
	}
	return nil
}

// alterMediaForShaPrimaryKey 在 PostgreSQL 上原地改主键：删 id 列（其上的旧主键约束
// 随列一起被 PG 删除）、删掉 AutoMigrate 建的 sha256 唯一索引、再把 sha256 设为主键。
func alterMediaForShaPrimaryKey(tx *gorm.DB) error {
	stmts := []string{
		`ALTER TABLE media DROP COLUMN IF EXISTS id`,
		`DROP INDEX IF EXISTS idx_media_sha256`,
		`ALTER TABLE media ADD PRIMARY KEY (sha256)`,
	}
	for _, stmt := range stmts {
		if err := tx.Exec(stmt).Error; err != nil {
			return err
		}
	}
	return nil
}

// AutoMigrate 只做增量变更（加表/加列/加索引）。
// 模型清单只有一处：AllModels()。原先这里曾把 TOTP 两表单独并进来，结果两处清单必然分叉，
// 所以并回一处。
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
