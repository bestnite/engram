package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

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

// TestApplyRefusesUnnamedMigration 覆盖 M0-4 的反面用例：
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
