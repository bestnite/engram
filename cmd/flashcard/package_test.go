package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"example.com/flashcard/internal/store"
	"gorm.io/gorm"
)

// M5-8 CLI 验收：export 写出 .fdeck，import 从该文件往返导入并给出与 REST 一致的报告。

// seedCLIDeck 建一个 owner + 卡组 + 两张 basic note（各一张卡）。
func seedCLIDeck(t *testing.T, db *gorm.DB) (userID, deckID uint64) {
	t.Helper()
	ctx := context.Background()
	u := store.User{
		Username: "cli_user", Email: "cli_user@example.com", DisplayName: "cli",
		Role: store.RoleUser, Status: store.StatusActive, Locale: "en",
		Timezone: "UTC", DayCutoffHour: 4, CreatedAt: time.Now().UTC(),
	}
	if err := db.Create(&u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	preset := store.NewPreset(u.ID, "Default")
	if err := store.NewPresetStore(db).Create(ctx, &preset); err != nil {
		t.Fatalf("create preset: %v", err)
	}
	d := store.Deck{OwnerUserID: u.ID, Name: "cli deck", Visibility: "private", PresetID: preset.ID, CreatedAt: time.Now().UTC()}
	if err := store.NewDeckStore(db).Create(ctx, &d); err != nil {
		t.Fatalf("create deck: %v", err)
	}
	ns := store.NewNoteStore(db)
	for i, f := range []map[string]any{
		{"front": "q1", "back": "a1"},
		{"front": "q2", "back": "a2"},
	} {
		n := store.Note{DeckID: d.ID, Kind: "basic", TagsJSON: "[]", CreatedBy: store.Ptr(u.ID)}
		if _, err := ns.Create(ctx, &n, f); err != nil {
			t.Fatalf("create note %d: %v", i, err)
		}
	}
	return u.ID, d.ID
}

// cfgEnv 是 CLI 子命令需要的最小环境（与 config.Load 的必需项一致）。
func cfgEnv(t *testing.T, dbPath string) {
	t.Helper()
	t.Setenv("DB_DRIVER", "sqlite")
	t.Setenv("DB_DSN", dbPath)
	t.Setenv("SESSION_SECRET", testSessionSecret)
	t.Setenv("ENCRYPTION_KEY", testEncryptionKey)
	t.Setenv("MEDIA_DIR", filepath.Join(t.TempDir(), "media"))
}

// TestCLIPackageRoundTrip 是 M5-8 的 CLI 验收：export 出的文件能被 import 往返。
func TestCLIPackageRoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cli.db")
	cfgEnv(t, dbPath)

	db, err := store.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	userID, deckID := seedCLIDeck(t, db)
	uid, did := strconv.FormatUint(userID, 10), strconv.FormatUint(deckID, 10)

	pkgPath := filepath.Join(t.TempDir(), "deck.fdeck")
	if out := captureCLI(t, func() error {
		return runExport([]string{"--deck", did, "--package", pkgPath, "--user", uid})
	}); out == "" {
		t.Fatal("runExport produced no output")
	}
	if fi, err := os.Stat(pkgPath); err != nil || fi.Size() == 0 {
		t.Fatalf("exported package missing or empty: %v", err)
	}

	// dry-run 预演：报告字段与 REST 一致，且不写入任何新卡组。
	report := captureImportReport(t, []string{"--package", pkgPath, "--user", uid, "--dry-run", "--target", "new_deck"})
	if report["dry_run"] != true {
		t.Errorf("dry_run = %v, want true", report["dry_run"])
	}
	if report["notes_created"] != float64(2) {
		t.Errorf("notes_created = %v, want 2", report["notes_created"])
	}
	for _, key := range []string{"notes_updated", "notes_skipped", "cards_created", "errors"} {
		if _, ok := report[key]; !ok {
			t.Errorf("report is missing %q: %v", key, report)
		}
	}

	// 正式导入到新卡组，再确认新卡组的 note 数量。
	real := captureImportReport(t, []string{"--package", pkgPath, "--user", uid, "--target", "new_deck"})
	if real["notes_created"] != float64(2) {
		t.Fatalf("real import notes_created = %v, want 2", real["notes_created"])
	}
	var count int64
	if err := db.Model(&store.Note{}).Where("deck_id <> ?", deckID).Count(&count).Error; err != nil {
		t.Fatalf("count imported notes: %v", err)
	}
	if count != 2 {
		t.Errorf("imported note count = %d, want 2", count)
	}
}

// captureCLI 临时把 CLI 诊断输出重定向到缓冲区，返回输出文本。
func captureCLI(t *testing.T, fn func() error) string {
	t.Helper()
	var buf bytes.Buffer
	old := cliOut
	cliOut = &buf
	defer func() { cliOut = old }()
	if err := fn(); err != nil {
		t.Fatalf("CLI command error: %v (output %s)", err, buf.String())
	}
	t.Logf("CLI output: %s", buf.String())
	return buf.String()
}

// captureImportReport 执行 runImport 并把 stdout 解析回报告对象。
func captureImportReport(t *testing.T, args []string) map[string]any {
	t.Helper()
	out := captureCLI(t, func() error { return runImport(args) })
	var report map[string]any
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("parse import report: %v (stdout %s)", err, out)
	}
	return report
}
