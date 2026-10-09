package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newSQLite(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "migrate.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	return db
}

// TestApplyRefusesUnnamedMigration 覆盖反面用例：
// 没有注册名字的破坏性变更必须被 schema sync 拒绝。
func TestApplyRefusesUnnamedMigration(t *testing.T) {
	db := newSQLite(t)
	ran := false
	migrations := []Migration{{Name: "   ", Up: func(tx *gorm.DB) error { ran = true; return nil }}}
	applied, err := Apply(context.Background(), db, migrations)
	if err == nil {
		t.Fatal("Apply() accepted an unnamed migration, want a refusal")
	}
	if !strings.Contains(err.Error(), "has no name") {
		t.Errorf("Apply() error = %q, want it to mention a missing name", err.Error())
	}
	if applied != 0 || ran {
		t.Errorf("applied = %d, ran = %v; want 0 and false", applied, ran)
	}
}

func TestApplyRefusesDuplicateMigrationNames(t *testing.T) {
	db := newSQLite(t)
	migrations := []Migration{
		{Name: "0001_dup", Up: func(tx *gorm.DB) error { return nil }},
		{Name: "0001_dup", Up: func(tx *gorm.DB) error { return nil }},
	}
	if _, err := Apply(context.Background(), db, migrations); err == nil {
		t.Fatal("Apply() accepted duplicate names, want a refusal")
	}
}

// TestApplyRunsRegisteredMigrationExactlyOnce 覆盖正面用例：已注册的迁移恰好执行一次。
func TestApplyRunsRegisteredMigrationExactlyOnce(t *testing.T) {
	db := newSQLite(t)
	ctx := context.Background()
	runs := 0
	migrations := []Migration{
		{Name: "0001_create_widgets", Up: func(tx *gorm.DB) error {
			runs++
			return tx.Exec("CREATE TABLE widgets (id INTEGER PRIMARY KEY)").Error
		}},
	}

	applied, err := Apply(ctx, db, migrations)
	if err != nil {
		t.Fatalf("first Apply() error = %v", err)
	}
	if applied != 1 || runs != 1 {
		t.Fatalf("first Apply(): applied = %d, runs = %d; want 1 and 1", applied, runs)
	}
	version, err := CurrentVersion(ctx, db)
	if err != nil {
		t.Fatalf("CurrentVersion() error = %v", err)
	}
	if version != 1 {
		t.Errorf("CurrentVersion() = %d, want 1", version)
	}

	// 第二次执行不得重跑：版本已到位。
	applied, err = Apply(ctx, db, migrations)
	if err != nil {
		t.Fatalf("second Apply() error = %v", err)
	}
	if applied != 0 || runs != 1 {
		t.Errorf("second Apply(): applied = %d, runs = %d; want 0 and 1", applied, runs)
	}
}

func TestSyncAppliesMigrationsAfterAutoMigrate(t *testing.T) {
	db := newSQLite(t)
	ctx := context.Background()
	runs := 0
	migrations := []Migration{{Name: "0001_mark", Up: func(tx *gorm.DB) error { runs++; return nil }}}
	applied, err := Sync(ctx, db, migrations)
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if applied != 1 || runs != 1 {
		t.Errorf("Sync(): applied = %d, runs = %d; want 1 and 1", applied, runs)
	}
	if !db.Migrator().HasTable("schema_version") {
		t.Error("Sync() did not create the schema_version table")
	}
	if !db.Migrator().HasTable("users") {
		t.Error("Sync() did not run AutoMigrate")
	}
}

// TestBuiltinMediaPrimaryKeyMigrationPreservesRows 覆盖破坏性迁移：media 主键从自增 id
// 换成内容 sha256。先在临时库里造**旧 schema**（id 主键 + sha256 唯一索引）并写入一行，
// 跑 Sync 后断言行数保留、id 列消失、sha256 成为主键（重复 sha 被拒），且新代码按 sha 能读到它。
func TestBuiltinMediaPrimaryKeyMigrationPreservesRows(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t)

	const oldSha = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	// 旧 schema 与 GORM 为旧模型（id 自增主键 + sha256 not null uniqueIndex）生成的 DDL 一致：
	// 单行、带反引号、created_at 为 datetime。这正是生产库里的形状。
	oldSchema := []string{
		"CREATE TABLE `media` (`id` integer PRIMARY KEY AUTOINCREMENT,`sha256` text NOT NULL,`rel_path` text NOT NULL,`mime` text NOT NULL,`bytes` integer NOT NULL,`width` integer,`height` integer,`created_by` integer,`created_at` datetime NOT NULL)",
		"CREATE UNIQUE INDEX `idx_media_sha256` ON `media`(`sha256`)",
		`INSERT INTO media (sha256, rel_path, mime, bytes, created_by, created_at)
		 VALUES ('` + oldSha + `', 'cc/` + oldSha + `.png', 'image/png', 42, NULL, '2026-01-01 00:00:00')`,
	}
	for _, stmt := range oldSchema {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("seed old schema: %v", err)
		}
	}
	if !db.Migrator().HasColumn("media", "id") {
		t.Fatal("fixture is not the old schema: media has no id column")
	}

	applied, err := Sync(ctx, db, BuiltinMigrations)
	if err != nil {
		t.Fatalf("Sync(BuiltinMigrations) error = %v", err)
	}
	if applied == 0 {
		t.Fatal("Sync() applied no migration, want the media primary-key migration")
	}

	// 行数保留，id 列消失，sha256 列仍在。
	var rows int64
	if err := db.Table("media").Count(&rows).Error; err != nil {
		t.Fatalf("count media rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("media rows after migration = %d, want 1 (must preserve existing rows)", rows)
	}
	if db.Migrator().HasColumn("media", "id") {
		t.Error("media still has an id column after the migration")
	}
	if !db.Migrator().HasColumn("media", "sha256") {
		t.Error("media lost its sha256 column")
	}

	// 按 sha 读回该行，确认数据本身没有丢。
	var got Media
	if err := db.First(&got, "sha256 = ?", oldSha).Error; err != nil {
		t.Fatalf("read migrated media by sha: %v", err)
	}
	if got.RelPath != "cc/"+oldSha+".png" || got.Bytes != 42 || got.Mime != "image/png" {
		t.Fatalf("migrated row differs: %+v", got)
	}

	// sha256 是新主键：同 sha 再插一行必须被拒；不同 sha 可正常插入（不需要 id）。
	dup := Media{Sha256: oldSha, RelPath: "cc/other.png", Mime: "image/png", Bytes: 1}
	if err := db.Create(&dup).Error; err == nil {
		t.Error("duplicate sha256 was accepted after the migration, want primary-key violation")
	}
	otherSha := "ddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	if err := db.Create(&Media{Sha256: otherSha, RelPath: "dd/" + otherSha + ".png", Mime: "image/png", Bytes: 2}).Error; err != nil {
		t.Fatalf("insert a new sha after the migration: %v", err)
	}

	// 迁移幂等：第二次 Sync 不再是「本次执行」，也不会因为表已是新形状而报错。
	applied, err = Sync(ctx, db, BuiltinMigrations)
	if err != nil {
		t.Fatalf("second Sync(BuiltinMigrations) error = %v", err)
	}
	if applied != 0 {
		t.Errorf("second Sync() applied = %d, want 0", applied)
	}
}

// legacyDeck 只用于迁移测试：它是 decks 还带 visibility 列时的形状。
// 业务模型已删除该列，因此不能拿 store.Deck 造出旧表。
type legacyDeck struct {
	ID            uint64    `gorm:"primaryKey"`
	OwnerUserID   uint64    `gorm:"not null;index"`
	Name          string    `gorm:"not null"`
	Description   string    `gorm:"not null"`
	Visibility    string    `gorm:"not null"`
	NewPerDay     int       `gorm:"not null;default:20"`
	ReviewsPerDay int       `gorm:"not null;default:200"`
	PresetID      uint64    `gorm:"not null;index"`
	CreatedAt     time.Time `gorm:"not null"`
}

func (legacyDeck) TableName() string { return "decks" }

// TestBuiltinDropDeckVisibilityMigration 覆盖破坏性迁移：decks.visibility 被删除。
// 先在临时库里造**旧 schema**（带 visibility 列）并写入一行自有、一行「公开」卡组；
// 跑 Sync 后断言列消失、两行都还在，且旧 public 行不再对别的用户可见——这正是删列的目的。
func TestBuiltinDropDeckVisibilityMigration(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t)
	if err := db.AutoMigrate(&legacyDeck{}); err != nil {
		t.Fatalf("create legacy decks table: %v", err)
	}
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, row := range []legacyDeck{
		{OwnerUserID: 1, Name: "mine", Visibility: "private", PresetID: 1, CreatedAt: ts},
		{OwnerUserID: 2, Name: "theirs", Visibility: "public", PresetID: 1, CreatedAt: ts},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("seed legacy deck %q: %v", row.Name, err)
		}
	}
	if !db.Migrator().HasColumn("decks", "visibility") {
		t.Fatal("fixture is not the old schema: decks has no visibility column")
	}

	applied, err := Sync(ctx, db, BuiltinMigrations)
	if err != nil {
		t.Fatalf("Sync(BuiltinMigrations) error = %v", err)
	}
	if applied == 0 {
		t.Fatal("Sync() applied no migration, want the deck-visibility drop")
	}
	if db.Migrator().HasColumn("decks", "visibility") {
		t.Error("decks still has a visibility column after the migration")
	}

	// 两行都保留（删列不改数据），公开卡组的名字也没被动过。
	var decks []Deck
	if err := db.Order("owner_user_id ASC").Find(&decks).Error; err != nil {
		t.Fatalf("read decks after migration: %v", err)
	}
	if len(decks) != 2 || decks[0].Name != "mine" || decks[1].Name != "theirs" {
		t.Fatalf("decks after migration = %+v, want the two seeded rows intact", decks)
	}

	// 关键行为：原先靠 public 对所有人可见的那张卡组，现在只属于它的属主。
	visible, err := NewDeckStore(db).ListVisible(ctx, 1)
	if err != nil {
		t.Fatalf("ListVisible() error = %v", err)
	}
	if len(visible) != 1 || visible[0].Name != "mine" {
		t.Fatalf("ListVisible(1) = %+v, want only the caller's own deck", visible)
	}

	// 幂等：第二次 Sync 不再是「本次执行」，也不会因为列已删除而报错。
	applied, err = Sync(ctx, db, BuiltinMigrations)
	if err != nil {
		t.Fatalf("second Sync(BuiltinMigrations) error = %v", err)
	}
	if applied != 0 {
		t.Errorf("second Sync() applied = %d, want 0", applied)
	}
}
