package store

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"

	"gorm.io/gorm"
)

// F22：卡组包 zip 索引校验。
//
//  1. 归档内**重复文件名/重复路径**必须拒绝 —— 解析器原先把同名条目静默覆盖（后者胜出），
//     一个包因此能藏一个与索引不符的覆盖层；
//  2. media.json 声明的 sha256 必须等于该条目**字节的真实 sha256**，且文件名须为
//     `<sha256>.<ext>`；否则一个"假 sha"就能让索引与内容彻底脱钩。
//
// 失败必须在任何写入之前发生：库里没有卡组/卡/媒体行，磁盘上没有残留文件。

// zipWithEntries 按给定顺序打 zip：允许同名条目、允许两份同名条目内容不同 —— 正是攻击面。
func zipWithEntries(t *testing.T, entries [][2]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e[0])
		if err != nil {
			t.Fatalf("zip create %s: %v", e[0], err)
		}
		if _, err := w.Write([]byte(e[1])); err != nil {
			t.Fatalf("zip write %s: %v", e[0], err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// indexPackageEntries 返回一个最小合法包的 JSON 条目（不含媒体字节条目本身）。
// mediaPath 同时用作 note 的引用与 media.json 的路径；sha 是 media.json 声明的 key。
func indexPackageEntries(mediaPath, sha, mime string) [][2]string {
	return [][2]string{
		{"manifest.json", `{"format_version":1,"exported_at":"2026-10-02T00:00:00Z","deck":{"name":"d"},"include_progress":false,"include_media":true,"include_reviews":false,"counts":{"notes":1,"cards":0,"media":1}}`},
		{"notes.json", fmt.Sprintf(`[{"kind":"basic","fields":{"front":"q","back":"%s"}}]`, mediaPath)},
		{"cards.json", `[]`},
		{"preset.json", `{"desired_retention":0.9,"learning_steps":"1m","relearning_steps":"10m","maximum_interval_days":100,"enable_fuzz":true,"weights":null,"weights_optimized_at":null,"weights_review_count":null}`},
		{"media.json", fmt.Sprintf(`{"%s":{"path":"%s","mime":"%s"}}`, sha, mediaPath, mime)},
	}
}

// png64 返回一段 64 字节、带 PNG 魔数的合法媒体字节（种子字节由 fill 决定）。
func png64(fill string) []byte {
	body := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte(fill), 56)...)
	return body
}

// assertNoImportSideEffects 断言被拒的导入没有留下任何卡组/卡/媒体行，也没有磁盘文件。
func assertNoImportSideEffects(t *testing.T, db *gorm.DB, root string) {
	t.Helper()
	var decks, notes, media int64
	db.Model(&Deck{}).Count(&decks)
	db.Model(&Note{}).Count(&notes)
	db.Model(&Media{}).Count(&media)
	if decks != 0 || notes != 0 || media != 0 {
		t.Fatalf("rejected import wrote rows: decks=%d notes=%d media=%d", decks, notes, media)
	}
	assertDirEmpty(t, root)
}

// TestReadPackageArchiveRejectsDuplicateEntry 是"同路径重复"的最小负例：同一名字出现两次，
// ReadPackageArchive 必须拒绝（修前它把后者覆盖进 map，静默通过）。
func TestReadPackageArchiveRejectsDuplicateEntry(t *testing.T) {
	sha := strings.Repeat("a", 64)
	path := "media/" + sha + ".png"
	raw := zipWithEntries(t, [][2]string{
		{"manifest.json", `{}`},
		{path, "first"},
		{path, "second"},
	})
	_, err := ReadPackageArchive(bytes.NewReader(raw), DefaultPackageLimits())
	var pe *PackageError
	if !errors.As(err, &pe) || pe.Code != CodePackageBadFormat {
		t.Fatalf("error = %v, want PackageError code %s", err, CodePackageBadFormat)
	}
	if !entryContains(pe.Entries, path) {
		t.Fatalf("entries = %v, want one naming %s", pe.Entries, path)
	}
}

// TestImportPackageRejectsDuplicateShaNamedEntryDifferentContent 覆盖"不同内容但同 sha 声明"：
// 一个 media/<sha>.png 出现两次，内容不同 —— 它们声称同一个 sha，但两段字节不可能都对。
// 整包被拒，且零副作用。
func TestImportPackageRejectsDuplicateShaNamedEntryDifferentContent(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			body := png64("x")
			other := png64("y")
			sum := sha256.Sum256(body)
			sha := hex.EncodeToString(sum[:])
			path := "media/" + sha + ".png"
			entries := append(indexPackageEntries(path, sha, "image/png"),
				[2]string{path, string(body)},
				[2]string{path, string(other)},
			)
			raw := zipWithEntries(t, entries)

			importer := seedUsers(t, db, "pkg_dup_same_sha")[0]
			root := t.TempDir()
			_, err := NewDeckStore(db).ImportPackage(context.Background(), importer, bytes.NewReader(raw), PackageImportOptions{
				Target:    PackageTargetNewDeck,
				MediaRoot: root,
			})
			var pe *PackageError
			if !errors.As(err, &pe) || pe.Code != CodePackageBadFormat {
				t.Fatalf("error = %v, want PackageError code %s", err, CodePackageBadFormat)
			}
			if !entryContains(pe.Entries, path) {
				t.Fatalf("entries = %v, want one naming %s", pe.Entries, path)
			}
			assertNoImportSideEffects(t, db, root)
		})
	}
}

// TestImportPackageRejectsDeclaredShaMismatch 覆盖"声明 sha ≠ 实际字节 sha"：media.json 用一段
// 合法十六进制 sha 作 key、路径也叫这个 sha，但字节的真实 sha256 是另一个值（假 sha）。
// 修前导入照常成功（字节以真实 sha 落盘），修后整包被拒且零副作用。
func TestImportPackageRejectsDeclaredShaMismatch(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			body := png64("z")
			fakeSha := strings.Repeat("f", 64)
			path := "media/" + fakeSha + ".png"
			entries := append(indexPackageEntries(path, fakeSha, "image/png"), [2]string{path, string(body)})
			raw := zipWithEntries(t, entries)

			importer := seedUsers(t, db, "pkg_fake_sha")[0]
			root := t.TempDir()
			_, err := NewDeckStore(db).ImportPackage(context.Background(), importer, bytes.NewReader(raw), PackageImportOptions{
				Target:    PackageTargetNewDeck,
				MediaRoot: root,
			})
			var pe *PackageError
			if !errors.As(err, &pe) || pe.Code != CodePackageBadFormat {
				t.Fatalf("error = %v, want PackageError code %s", err, CodePackageBadFormat)
			}
			if !entryContains(pe.Entries, path) {
				t.Fatalf("entries = %v, want one naming %s", pe.Entries, path)
			}
			assertNoImportSideEffects(t, db, root)
		})
	}
}

// TestImportPackageRejectsMediaEntryNameNotMatchingSha 是相邻负例：字节的 sha256 与 media.json
// 的 key 一致、note 也按规范路径 media/<sha>.png 引用，但 media.json 把该 sha 指到一个
// 文件名与 sha 无关的条目上（索引与内容对不上）。整包被拒，零副作用。
func TestImportPackageRejectsMediaEntryNameNotMatchingSha(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			body := png64("w")
			sum := sha256.Sum256(body)
			sha := hex.EncodeToString(sum[:])
			// media.json 声明的 sha 指向一个 basename 与 sha 无关的条目。
			path := "media/" + strings.Repeat("b", 64) + ".png"
			entries := [][2]string{
				{"manifest.json", `{"format_version":1,"exported_at":"2026-10-02T00:00:00Z","deck":{"name":"d"},"include_progress":false,"include_media":true,"include_reviews":false,"counts":{"notes":1,"cards":0,"media":1}}`},
				{"notes.json", fmt.Sprintf(`[{"kind":"basic","fields":{"front":"q","back":"media/%s.png"}}]`, sha)},
				{"cards.json", `[]`},
				{"preset.json", `{"desired_retention":0.9,"learning_steps":"1m","relearning_steps":"10m","maximum_interval_days":100,"enable_fuzz":true,"weights":null,"weights_optimized_at":null,"weights_review_count":null}`},
				{"media.json", fmt.Sprintf(`{"%s":{"path":"%s","mime":"image/png"}}`, sha, path)},
				{path, string(body)},
			}
			raw := zipWithEntries(t, entries)

			importer := seedUsers(t, db, "pkg_name_mismatch")[0]
			root := t.TempDir()
			_, err := NewDeckStore(db).ImportPackage(context.Background(), importer, bytes.NewReader(raw), PackageImportOptions{
				Target:    PackageTargetNewDeck,
				MediaRoot: root,
			})
			var pe *PackageError
			if !errors.As(err, &pe) || pe.Code != CodePackageBadFormat {
				t.Fatalf("error = %v, want PackageError code %s", err, CodePackageBadFormat)
			}
			if !entryContains(pe.Entries, path) {
				t.Fatalf("entries = %v, want one naming %s", pe.Entries, path)
			}
			assertNoImportSideEffects(t, db, root)
		})
	}
}
