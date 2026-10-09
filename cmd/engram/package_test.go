package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
	"gorm.io/gorm"
)

// CLI 验收：export 写出 .edeck，import 从该文件往返导入并给出与 REST 一致的报告。

// seedCLIDeck 建一个 owner + 卡组 + 两张 basic note（各一张卡）。
func seedCLIDeck(t *testing.T, db *gorm.DB) (userID, deckID uint64) {
	t.Helper()
	ctx := context.Background()
	u := store.User{
		Username: "cli_user", Email: "cli_user@example.com", DisplayName: "cli",
		Role: store.RoleUser, Status: store.StatusActive, Locale: "en",
		Timezone: "UTC", DayCutoffHour: store.Ptr(4), CreatedAt: time.Now().UTC(),
	}
	if err := db.Create(&u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	preset := store.NewPreset(u.ID, "Default")
	if err := store.NewPresetStore(db).Create(ctx, &preset); err != nil {
		t.Fatalf("create preset: %v", err)
	}
	d := store.Deck{OwnerUserID: u.ID, Name: "cli deck", PresetID: preset.ID, CreatedAt: time.Now().UTC()}
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

// TestCLIPackageRoundTrip 是 CLI 验收：export 出的文件能被 import 往返。
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

	pkgPath := filepath.Join(t.TempDir(), "deck.edeck")
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

// cliRun 执行一个 CLI 命令并返回其错误与诊断输出；用于断言拒绝路径（非零退出）。
func cliRun(fn func() error) (error, string) {
	var buf bytes.Buffer
	old := cliOut
	cliOut = &buf
	defer func() { cliOut = old }()
	return fn(), buf.String()
}

// importFixture 建库、播种源卡组与一个导入目标账号，再导出一个可往返的 .edeck。
func importFixture(t *testing.T) (db *gorm.DB, target store.User, pkgPath string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "cli.db")
	cfgEnv(t, dbPath)
	var err error
	db, err = store.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	userID, deckID := seedCLIDeck(t, db)
	target = store.User{
		Username: "importer", Email: "importer@example.com", DisplayName: "importer",
		Role: store.RoleUser, Status: store.StatusActive, Locale: "en",
		Timezone: "UTC", DayCutoffHour: store.Ptr(4), CreatedAt: time.Now().UTC(),
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("create target user: %v", err)
	}
	pkgPath = filepath.Join(t.TempDir(), "deck.edeck")
	captureCLI(t, func() error {
		return runExport([]string{"--deck", strconv.FormatUint(deckID, 10), "--package", pkgPath, "--user", strconv.FormatUint(userID, 10)})
	})
	return db, target, pkgPath
}

// countDecks 统计卡组总数，用来断言拒绝路径确实没有落库。
func countDecks(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&store.Deck{}).Count(&n).Error; err != nil {
		t.Fatalf("count decks: %v", err)
	}
	return n
}

// TestCLIImportRequiresExplicitUser 是命令级验收：
// 缺 --user 非零退出且不建卡组、带 --user 落到指定账号（id 与用户名两种取值）、
// --user 指向不存在的人则拒绝且不落库。
func TestCLIImportRequiresExplicitUser(t *testing.T) {
	db, target, pkgPath := importFixture(t)

	// ① 缺 --user：非零退出，错误要说清怎么传，且不得创建任何卡组。
	before := countDecks(t, db)
	err, _ := cliRun(func() error {
		return run(context.Background(), []string{"import", "--package", pkgPath, "--target", "new_deck"})
	})
	if err == nil {
		t.Fatal("import without --user exited 0, want a non-zero exit")
	}
	if !strings.Contains(err.Error(), "--user") {
		t.Errorf("error does not say how to pass --user: %v", err)
	}
	if after := countDecks(t, db); after != before {
		t.Errorf("import without --user created a deck: %d -> %d", before, after)
	}

	// ③ --user 指向不存在的用户：点名拒绝，且不落库。
	before = countDecks(t, db)
	err, _ = cliRun(func() error {
		return run(context.Background(), []string{"import", "--package", pkgPath, "--user", "987654", "--target", "new_deck"})
	})
	if err == nil {
		t.Fatal("import with an unknown --user exited 0, want a non-zero exit")
	}
	if !strings.Contains(err.Error(), "987654") {
		t.Errorf("error does not name the unknown user: %v", err)
	}
	if after := countDecks(t, db); after != before {
		t.Errorf("import with an unknown --user created a deck: %d -> %d", before, after)
	}

	// ② 带 --user（用户名形式）：内容落到该账号名下。
	report := captureImportReport(t, []string{"--package", pkgPath, "--user", target.Username, "--target", "new_deck"})
	if report["notes_created"] != float64(2) {
		t.Fatalf("import by username notes_created = %v, want 2", report["notes_created"])
	}
	assertDeckOwner(t, db, target.ID, 2)

	// ② 带 --user（id 形式）：同样落到该账号名下。
	if report := captureImportReport(t, []string{"--package", pkgPath, "--user", strconv.FormatUint(target.ID, 10), "--target", "new_deck"}); report["notes_created"] != float64(2) {
		t.Fatalf("import by id notes_created = %v, want 2", report["notes_created"])
	}
	assertDeckOwner(t, db, target.ID, 2)
}

// TestCLIImportRejectsAmbiguousUserRef：数字取值同时命中一个 id 与一个用户名且指向不同人时，
// 必须点名歧义并拒绝，而不是任选一个账号写入。
func TestCLIImportRejectsAmbiguousUserRef(t *testing.T) {
	db, _, pkgPath := importFixture(t)

	// 先取一个已存在的用户 id，再建一个用户名恰好等于该数字的账号，制造歧义取值。
	var existing store.User
	if err := db.Order("id ASC").First(&existing).Error; err != nil {
		t.Fatalf("load first user: %v", err)
	}
	clash := store.User{
		Username: strconv.FormatUint(existing.ID, 10), Email: "clash@example.com",
		Role: store.RoleUser, Status: store.StatusActive, Locale: "en",
		Timezone: "UTC", DayCutoffHour: store.Ptr(4), CreatedAt: time.Now().UTC(),
	}
	if err := db.Create(&clash).Error; err != nil {
		t.Fatalf("create clashing user: %v", err)
	}
	if clash.ID == existing.ID {
		t.Fatal("clashing user unexpectedly shares the id")
	}

	before := countDecks(t, db)
	err, _ := cliRun(func() error {
		return run(context.Background(), []string{"import", "--package", pkgPath, "--user", strconv.FormatUint(existing.ID, 10), "--target", "new_deck"})
	})
	if err == nil {
		t.Fatal("import with an ambiguous --user exited 0, want a non-zero exit")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("error does not flag the ambiguity: %v", err)
	}
	if after := countDecks(t, db); after != before {
		t.Errorf("ambiguous --user still created a deck: %d -> %d", before, after)
	}
}

// assertDeckOwner 断言指定账号下存在一个含 wantNotes 条 note 的卡组。
func assertDeckOwner(t *testing.T, db *gorm.DB, ownerID uint64, wantNotes int64) {
	t.Helper()
	var d store.Deck
	if err := db.Where("owner_user_id = ?", ownerID).Order("id DESC").First(&d).Error; err != nil {
		t.Fatalf("no deck owned by user %d: %v", ownerID, err)
	}
	var n int64
	if err := db.Model(&store.Note{}).Where("deck_id = ?", d.ID).Count(&n).Error; err != nil {
		t.Fatalf("count notes under deck %d: %v", d.ID, err)
	}
	if n != wantNotes {
		t.Errorf("deck %d (owner %d) has %d notes, want %d", d.ID, ownerID, n, wantNotes)
	}
}
