package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gorm.io/gorm"
)

// 媒体是卡组包里唯一会往磁盘写文件的路径，却是覆盖最少的：M5-10 要求导出→导入新库后
// 文件真的落盘且字节一致，并断言 skip_missing_media 的两种取值（ROADMAP.md M5-10）。

// seedMediaNote 把 raw 按内容寻址写进 srcRoot 与媒体元数据表，并在卡组里放一条引用它的 note；
// 返回 sha256。媒体引用形如 media/<sha256>.<ext>。
func seedMediaNote(t *testing.T, db *gorm.DB, srcRoot string, owner, deckID uint64, raw []byte, mime, ext string) string {
	t.Helper()
	m, err := NewMediaStore(db).SaveBytes(context.Background(), srcRoot, mime, raw, Ptr(owner))
	if err != nil {
		t.Fatalf("SaveBytes: %v", err)
	}
	n := Note{DeckID: deckID, Kind: "basic", TagsJSON: "[]", CreatedBy: Ptr(owner)}
	if _, err := NewNoteStore(db).Create(context.Background(), &n, map[string]any{
		"front": "img?", "back": "media/" + m.Sha256 + "." + ext,
	}); err != nil {
		t.Fatalf("create media note: %v", err)
	}
	return m.Sha256
}

// TestImportPackageMediaRoundTrip 断言带媒体的包导出后导入全新库，媒体文件真的落盘且字节一致。
func TestImportPackageMediaRoundTrip(t *testing.T) {
	srcs := packageDatabases(t)
	dsts := packageDatabases(t)
	for driver, src := range srcs {
		dst := dsts[driver]
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			owner := seedUsers(t, src, "pkg_media_rt_owner")[0]
			deckID := seedPresetDeck(t, src, owner)
			srcRoot := t.TempDir()

			// 一段带 PNG 魔数的字节：SaveBytes 只按内容寻址，不校验 magic，这里刻意保留可辨识头。
			raw := append([]byte("\x89PNG\r\n\x1a\n"), []byte("media-round-trip-payload")...)
			sum := sha256.Sum256(raw)
			sha := hex.EncodeToString(sum[:])
			if got := seedMediaNote(t, src, srcRoot, owner, deckID, raw, "image/png", "png"); got != sha {
				t.Fatalf("seeded media sha = %s, want %s", got, sha)
			}

			pkg, err := NewDeckStore(src).ExportPackage(ctx, owner, deckID, PackageOptions{IncludeMedia: true, MediaRoot: srcRoot})
			if err != nil {
				t.Fatalf("ExportPackage: %v", err)
			}
			if !pkg.Manifest.IncludeMedia || pkg.Manifest.Counts.Media != 1 {
				t.Fatalf("manifest media: include=%v counts=%d, want true/1", pkg.Manifest.IncludeMedia, pkg.Manifest.Counts.Media)
			}
			if !bytes.Equal(pkg.MediaBytes[sha], raw) {
				t.Fatalf("exported media bytes differ from source: %d bytes", len(pkg.MediaBytes[sha]))
			}
			var buf bytes.Buffer
			if err := pkg.WriteZip(&buf); err != nil {
				t.Fatalf("WriteZip: %v", err)
			}

			dstRoot := t.TempDir()
			importer := seedUsers(t, dst, "pkg_media_rt_importer")[0]
			report, err := NewDeckStore(dst).ImportPackage(ctx, importer, bytes.NewReader(buf.Bytes()), PackageImportOptions{MediaRoot: dstRoot})
			if err != nil {
				t.Fatalf("ImportPackage: %v", err)
			}
			if report.MediaNew != 1 || report.MediaMissing != 0 {
				t.Fatalf("report media_new=%d media_missing=%d, want 1/0", report.MediaNew, report.MediaMissing)
			}

			row, err := NewMediaStore(dst).BySha256(ctx, sha)
			if err != nil {
				t.Fatalf("load imported media metadata: %v", err)
			}
			if row == nil || row.Sha256 != sha || row.Mime != "image/png" {
				t.Fatalf("imported media metadata = %+v, want sha %s mime image/png", row, sha)
			}
			onDisk, err := os.ReadFile(filepath.Join(dstRoot, filepath.FromSlash(row.RelPath)))
			if err != nil {
				t.Fatalf("read imported media from disk: %v", err)
			}
			if !bytes.Equal(onDisk, raw) {
				t.Fatalf("imported media bytes differ from source: got %d bytes, disk %v", len(onDisk), row.RelPath)
			}
		})
	}
}

// TestImportMissingMediaRespectsSkipFlag 断言 skip_missing_media 两种取值的行为：
// false（默认）缺媒体即失败并列出清单；true 只计数、继续导入且不落盘任何文件。
func TestImportMissingMediaRespectsSkipFlag(t *testing.T) {
	srcs := packageDatabases(t)
	dsts := packageDatabases(t)
	for driver, src := range srcs {
		dst := dsts[driver]
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			owner := seedUsers(t, src, "pkg_missing_owner")[0]
			deckID := seedPresetDeck(t, src, owner)

			// note 引用一个包里根本没有字节的 sha（导出时 include_media=0，引用仍在字段里）。
			sha := strings.Repeat("a", 64)
			n := Note{DeckID: deckID, Kind: "basic", TagsJSON: "[]", CreatedBy: Ptr(owner)}
			if _, err := NewNoteStore(src).Create(ctx, &n, map[string]any{"front": "x?", "back": "media/" + sha + ".png"}); err != nil {
				t.Fatalf("create note: %v", err)
			}
			raw := exportZip(t, src, owner, deckID, PackageOptions{IncludeMedia: false})

			// 默认（skip_missing_media=false）：失败并指名缺失的媒体条目。
			_, err := NewDeckStore(dst).ImportPackage(ctx, 1, bytes.NewReader(raw), PackageImportOptions{})
			var pe *PackageError
			if !errors.As(err, &pe) || pe.Code != CodePackageBadFormat {
				t.Fatalf("error = %v, want PackageError code %s", err, CodePackageBadFormat)
			}
			if !entryContains(pe.Entries, sha) {
				t.Fatalf("entries = %v, want one naming media/%s", pe.Entries, sha)
			}

			// skip_missing_media=true：只计数、继续导入。
			dstRoot := t.TempDir()
			importer := seedUsers(t, dst, "pkg_missing_importer")[0]
			report, err := NewDeckStore(dst).ImportPackage(ctx, importer, bytes.NewReader(raw), PackageImportOptions{SkipMissingMedia: true, MediaRoot: dstRoot})
			if err != nil {
				t.Fatalf("ImportPackage (skip_missing_media): %v", err)
			}
			if report.MediaMissing != 1 || report.MediaNew != 0 || report.NotesCreated != 1 {
				t.Fatalf("report media_missing=%d media_new=%d notes_created=%d, want 1/0/1", report.MediaMissing, report.MediaNew, report.NotesCreated)
			}
			row, err := NewMediaStore(dst).BySha256(ctx, sha)
			if err != nil {
				t.Fatalf("load media metadata: %v", err)
			}
			if row != nil {
				t.Fatalf("missing media must not create metadata, got %+v", row)
			}
			entries, err := os.ReadDir(dstRoot)
			if err != nil {
				t.Fatalf("read media root: %v", err)
			}
			if len(entries) != 0 {
				t.Fatalf("missing media must not write files, found %d entries", len(entries))
			}
		})
	}
}

// entryContains 判断逐条错误列表里是否有条目提到 needle。
func entryContains(entries []string, needle string) bool {
	for _, e := range entries {
		if strings.Contains(e, needle) {
			return true
		}
	}
	return false
}

// TestImportPackageRollbackLeavesNoOrphanMedia 是 M5-11 的验收测试：媒体写入发生在
// 事务提交之前，若事务随后失败回滚，写下的字节必须一并清理，不能留下无人引用的孤儿文件。
//
// 构造方式：目标卡组里预置一条与包内 note 同 external_ref 的记录，配合 on_conflict=fail
// 让导入在“媒体已落盘、note 已判定冲突”之后整体失败。冲突判定发生在媒体写入之后
// （见 importInTx 的顺序），因此这条路径确实会先写文件再回滚。
//
// 断言两侧：媒体分片目录存在（证明写盘路径真的执行过，测试没有空转）且文件已被清掉，
// 同时元数据行不存在。
func TestImportPackageRollbackLeavesNoOrphanMedia(t *testing.T) {
	srcs := packageDatabases(t)
	dsts := packageDatabases(t)
	for driver, src := range srcs {
		dst := dsts[driver]
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			owner := seedUsers(t, src, "pkg_orphan_owner")[0]
			srcDeck := seedPresetDeck(t, src, owner)
			srcRoot := t.TempDir()

			raw := append([]byte("\x89PNG\r\n\x1a\n"), []byte("orphan-media-payload")...)
			sum := sha256.Sum256(raw)
			sha := hex.EncodeToString(sum[:])
			m, err := NewMediaStore(src).SaveBytes(ctx, srcRoot, "image/png", raw, Ptr(owner))
			if err != nil {
				t.Fatalf("SaveBytes: %v", err)
			}
			if m.Sha256 != sha {
				t.Fatalf("seeded media sha = %s, want %s", m.Sha256, sha)
			}
			srcNote := Note{DeckID: srcDeck, Kind: "basic", TagsJSON: "[]", CreatedBy: Ptr(owner), ExternalRef: Ptr("ext:conflict")}
			if _, err := NewNoteStore(src).Create(ctx, &srcNote, map[string]any{
				"front": "q?", "back": "media/" + sha + ".png",
			}); err != nil {
				t.Fatalf("create source note: %v", err)
			}
			rawZip := exportZip(t, src, owner, srcDeck, PackageOptions{IncludeMedia: true, MediaRoot: srcRoot})

			importer := seedUsers(t, dst, "pkg_orphan_importer")[0]
			dstDeck := seedPresetDeck(t, dst, importer)
			// 预置同 external_ref 的 note，让包内 note 命中冲突（on_conflict=fail → 整包回滚）。
			pre := Note{DeckID: dstDeck, Kind: "basic", TagsJSON: "[]", CreatedBy: Ptr(importer), ExternalRef: Ptr("ext:conflict")}
			if _, err := NewNoteStore(dst).Create(ctx, &pre, map[string]any{"front": "already here", "back": "x"}); err != nil {
				t.Fatalf("create conflicting note: %v", err)
			}

			dstRoot := t.TempDir()
			target := "into_deck:" + strconv.FormatUint(dstDeck, 10)
			_, err = NewDeckStore(dst).ImportPackage(ctx, importer, bytes.NewReader(rawZip), PackageImportOptions{
				Target: target, OnConflict: "fail", MediaRoot: dstRoot,
			})
			if err == nil {
				t.Fatal("ImportPackage succeeded, want a conflict failure after the media write")
			}
			var pe *PackageError
			if !errors.As(err, &pe) {
				t.Fatalf("error = %v, want *PackageError", err)
			}

			// 元数据行必须随事务回滚。
			row, err := NewMediaStore(dst).BySha256(ctx, sha)
			if err != nil {
				t.Fatalf("load media metadata: %v", err)
			}
			if row != nil {
				t.Fatalf("rolled-back import left media metadata %+v", row)
			}
			// 写盘路径确实执行过：分片目录是 MkdirAll 建的，清理只删文件不删目录。
			shardDir := filepath.Join(dstRoot, sha[:2])
			if fi, err := os.Stat(shardDir); err != nil || !fi.IsDir() {
				t.Fatalf("shard dir %s missing (err=%v); the media write path did not run", shardDir, err)
			}
			// 关键断言：没有任何孤儿文件残留。
			orphan := filepath.Join(shardDir, sha+".png")
			if _, err := os.Stat(orphan); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("orphan media file %s survived the rollback (err=%v)", orphan, err)
			}
			var files int
			if err := filepath.WalkDir(dstRoot, func(_ string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !d.IsDir() {
					files++
				}
				return nil
			}); err != nil {
				t.Fatalf("walk media root: %v", err)
			}
			if files != 0 {
				t.Fatalf("media root %s still holds %d orphan file(s)", dstRoot, files)
			}
		})
	}
}
