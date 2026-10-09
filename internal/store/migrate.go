package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Migration 是一次性的显式迁移，用于 AutoMigrate 做不到的破坏性变更
// （改列类型、删列、新增非空约束，AGENTS.md §2.3 第 5 条）。
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
		// 媒体主键从自增 id 换成内容 sha256：删列不在 AutoMigrate
		// 的加法范围内，必须显式迁移。这里只改主键，不迁移旧引用。
		Name: "0001_media_primary_key_sha256",
		Up:   mediaPrimaryKeySha256,
	},
	{
		Name: "0002_nullable_day_cutoff",
		Up:   nullableDayCutoff,
	},
	{
		// 卡组可见性（private / unlisted / public）整体删除：卡组只对属主与被显式授权者可见。
		// 删列不在 AutoMigrate 的加法范围内，必须显式迁移。存量 public/unlisted 行随之失去
		// 那条隐式可见性——这正是本次变更的目的，因此不重写任何数据，只删列。
		Name: "0003_drop_deck_visibility",
		Up:   dropDeckVisibility,
	},
	{
		// 对外 id 从自增主键迁到 public_id：为已有行回填 UUIDv7。
		// 列本身由 AutoMigrate 加出（可空 + 唯一索引），这里只负责填值。
		// SQLite 与 PostgreSQL 都没有生成 UUIDv7 的 SQL 函数，所以逐行在 Go 侧生成。
		Name: "0004_backfill_public_ids",
		Up:   backfillPublicIDs,
	},
}

// dropDeckVisibility 删除 decks.visibility 列。
//
// 两库都直接 DROP COLUMN：该列既不在主键上，也没有索引或约束，SQLite（≥3.35）与 PostgreSQL
// 都支持原地删除，无需像 0001 那样重建表。已经是新形状的库（AutoMigrate 建出的 decks 没有
// 该列）在此直接跳过。
func dropDeckVisibility(tx *gorm.DB) error {
	m := tx.Migrator()
	if !m.HasTable("decks") || !m.HasColumn("decks", "visibility") {
		return nil
	}
	var stmt string
	switch tx.Dialector.Name() {
	case "sqlite":
		stmt = "ALTER TABLE decks DROP COLUMN visibility"
	case "postgres":
		stmt = "ALTER TABLE decks DROP COLUMN IF EXISTS visibility"
	default:
		return fmt.Errorf("drop deck visibility migration: unsupported dialect %q", tx.Dialector.Name())
	}
	return tx.Exec(stmt).Error
}

// publicIDTables 是需要回填 public_id 的表，与对外可见实体一一对应。
// 未对外暴露的表（sessions、outbox 等）不在此列，也不带 public_id 列。
// 表名只来自这份固定清单、不含任何外部输入，因此可以安全地拼进 SQL。
var publicIDTables = []string{
	"users", "identities", "invites", "presets", "decks",
	"notes", "cards", "reviews", "api_keys", "jobs", "audit_log",
}

// backfillPublicIDs 给每个目标表里 public_id 为空的行补一个 UUIDv7。
// 逐行更新是有意为之：两库都无法在 SQL 里生成 v7，行数也不足以让逐行成为瓶颈。
func backfillPublicIDs(tx *gorm.DB) error {
	m := tx.Migrator()
	for _, table := range publicIDTables {
		if !m.HasTable(table) || !m.HasColumn(table, "public_id") {
			continue
		}
		var ids []uint64
		// 用 Table（而不是模型）取行，绕过软删除作用域：软删除的 note/card 同样需要 id。
		if err := tx.Table(table).Where("public_id IS NULL OR public_id = ''").Pluck("id", &ids).Error; err != nil {
			return fmt.Errorf("backfill public ids: scan %s: %w", table, err)
		}
		for _, id := range ids {
			if err := tx.Exec("UPDATE "+table+" SET public_id = ? WHERE id = ?", NewPublicID(), id).Error; err != nil {
				return fmt.Errorf("backfill public ids: update %s id=%d: %w", table, id, err)
			}
		}
	}
	return nil
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
// 写成 TEXT 会让驱动无法把它扫回 time.Time）。列清单与 media 模型一致。
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

// dayCutoffColumn 只供显式 DDL 使用；业务仍使用 User 同一模型。
// 增量 DDL 专用形状屏蔽此列，避免 AutoMigrate 在版本化迁移前移除 NOT NULL/default。
type dayCutoffColumn struct {
	DayCutoffHour *int `gorm:"column:day_cutoff_hour"`
}

func (dayCutoffColumn) TableName() string { return "users" }

// 仅在增量 DDL 使用，不作为业务模型：其余用户字段仍直接嵌入唯一的 User 定义。
type additiveUserSchema struct {
	User
	DayCutoffHour *int `gorm:"column:day_cutoff_hour;-:migration"`
}

func (additiveUserSchema) TableName() string { return "users" }

// nullableDayCutoff 只放宽列约束，不转换既有数值：旧 0 现在明确代表午夜，旧 4 保持不变。
func nullableDayCutoff(tx *gorm.DB) error {
	if !tx.Migrator().HasTable("users") || !tx.Migrator().HasColumn("users", "day_cutoff_hour") {
		return nil
	}
	switch tx.Dialector.Name() {
	case "sqlite":
		// 驱动重建表时不保留外置索引/触发器，先保存并在同一事务内恢复，避免唯一约束丢失。
		var objects []struct{ SQL string }
		if err := tx.Raw("SELECT sql FROM sqlite_master WHERE tbl_name = ? AND type IN ('index', 'trigger') AND sql IS NOT NULL", "users").Scan(&objects).Error; err != nil {
			return err
		}
		if err := tx.Migrator().AlterColumn(&dayCutoffColumn{}, "DayCutoffHour"); err != nil {
			return err
		}
		for _, object := range objects {
			if err := tx.Exec(object.SQL).Error; err != nil {
				return err
			}
		}
		return nil
	case "postgres":
		if err := tx.Exec("ALTER TABLE users ALTER COLUMN day_cutoff_hour DROP NOT NULL").Error; err != nil {
			return err
		}
		return tx.Exec("ALTER TABLE users ALTER COLUMN day_cutoff_hour DROP DEFAULT").Error
	default:
		return fmt.Errorf("day cutoff migration: unsupported dialect %q", tx.Dialector.Name())
	}
}

// AutoMigrate 只做增量变更（加表/加列/加索引）。
// 模型清单只有一处：AllModels()。原先这里曾把 TOTP 两表单独并进来，结果两处清单必然分叉，
// 所以并回一处。
func AutoMigrate(ctx context.Context, db *gorm.DB) error {
	models := AllModels()
	for i, model := range models {
		if _, ok := model.(*User); ok {
			models[i] = &additiveUserSchema{}
		}
	}
	if err := db.WithContext(ctx).AutoMigrate(models...); err != nil {
		return fmt.Errorf("auto migrate: %w", err)
	}
	// 新库补可空列是加法；旧列的约束只能交给 0002 显式迁移。
	if !db.Migrator().HasColumn("users", "day_cutoff_hour") {
		if err := db.WithContext(ctx).Migrator().AddColumn(&dayCutoffColumn{}, "DayCutoffHour"); err != nil {
			return fmt.Errorf("add nullable day cutoff: %w", err)
		}
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

// setVersion 用 Save 写单行版本表，两库都会生成正确的 upsert。
func setVersion(tx *gorm.DB, version int) error {
	row := SchemaVersion{ID: 1, Version: version, UpdatedAt: time.Now().UTC()}
	if err := tx.Save(&row).Error; err != nil {
		return fmt.Errorf("write schema version: %w", err)
	}
	return nil
}
