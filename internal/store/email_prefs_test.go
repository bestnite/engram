package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// openEmailPrefFile 打开一个基于临时文件的 SQLite 库并迁移全部模型。
// 用文件库（不是内存库）是为了能关掉再打开，真实模拟一次「重启」。
func openEmailPrefFile(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite %s: %v", path, err)
	}
	if err := db.AutoMigrate(AllModels()...); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	return db
}

// closeEmailPrefDB 关闭底层 sql.DB，让后续的重新打开拿到真正的磁盘状态。
func closeEmailPrefDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close sql.DB: %v", err)
	}
}

// TestEmailPrefsPersistAcrossReopen 是验收项「用户对可选类型的选择重启后仍生效」的直接证据：
// 写入选择 → 关闭数据库 → 重新打开同一文件 → 读回的选择完全一致。
func TestEmailPrefsPersistAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.db")
	ctx := context.Background()
	want := map[string]bool{
		"deck_shared":     false, // B：默认开，用户关掉
		"review_reminder": true,  // C：默认关，用户打开
		"invite":          false,
	}

	db := openEmailPrefFile(t, path)
	if err := NewEmailPrefStore(db).SetChoices(ctx, 42, want, time.Now().UTC()); err != nil {
		t.Fatalf("SetChoices() error = %v", err)
	}
	closeEmailPrefDB(t, db)

	reopened := openEmailPrefFile(t, path)
	got, err := NewEmailPrefStore(reopened).Choices(ctx, 42)
	if err != nil {
		t.Fatalf("Choices() after reopen error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("choices after reopen = %v, want %v", got, want)
	}
	closeEmailPrefDB(t, reopened)
}

// TestEmailPrefsChoicesEmptyWhenUnset 断言从未保存过偏好的用户读到空 map（而非报错）。
func TestEmailPrefsChoicesEmptyWhenUnset(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			got, err := NewEmailPrefStore(db).Choices(context.Background(), 999999)
			if err != nil {
				t.Fatalf("Choices() error = %v", err)
			}
			if len(got) != 0 {
				t.Errorf("Choices() = %v, want an empty map", got)
			}
		})
	}
}

// TestEmailPrefsSetChoicesReplaces 断言保存是整体覆盖而不是合并：
// 一次提交就是用户在该时刻的完整选择，旧值必须被顶掉，否则关掉某个类型后它会残留。
func TestEmailPrefsSetChoicesReplaces(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			s := NewEmailPrefStore(db)
			if err := s.SetChoices(ctx, 7, map[string]bool{"deck_shared": false, "invite": false}, time.Now().UTC()); err != nil {
				t.Fatalf("first SetChoices() error = %v", err)
			}
			want := map[string]bool{"review_reminder": true}
			if err := s.SetChoices(ctx, 7, want, time.Now().UTC()); err != nil {
				t.Fatalf("second SetChoices() error = %v", err)
			}
			got, err := s.Choices(ctx, 7)
			if err != nil {
				t.Fatalf("Choices() error = %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("choices after replace = %v, want %v", got, want)
			}
		})
	}
}

// TestEmailPrefsTableRegisteredForMigration 断言 email_prefs 进了 AllModels()：
// AutoMigrate 从 AllModels 取，漏登记会让建表静默少一张（AGENTS.md §6.4、M1-18 守卫测试）。
func TestEmailPrefsTableRegisteredForMigration(t *testing.T) {
	found := false
	for _, model := range AllModels() {
		if _, ok := model.(*EmailPref); ok {
			found = true
		}
	}
	if !found {
		t.Fatal("EmailPref is not registered in AllModels(); AutoMigrate would silently skip email_prefs")
	}
}
