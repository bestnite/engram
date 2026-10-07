package store

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"
)

// 卡组包导入新增的媒体字节必须计入**导入者**配额，超限整包失败、零残留。
//
// 修前 ImportPackage 完全不看配额：只要包能解开、媒体在白名单内，字节就落盘。上传链
// （internal/web 的 checkMediaQuota）挡在 /media 与 /decks/:id/media 两个入口，而
// web / REST / MCP / CLI 四条导入路径都绕开它 —— 于是拿一个装满媒体的包就能无限占用空间。

// quotaImportFixture 造一个源库：一份 64 字节 PNG 媒体 + 引用它的 note，导出带媒体的包。
// 返回包字节、媒体 sha256 与媒体字节数。
func quotaImportFixture(t *testing.T, src *gorm.DB) (raw []byte, sha string, size int) {
	t.Helper()
	owner := seedUsers(t, src, "pkg_quota_owner")[0]
	deckID := seedPresetDeck(t, src, owner)
	srcRoot := t.TempDir()
	body := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("q"), 56)...)
	sha = seedMediaNote(t, src, srcRoot, owner, deckID, body, "image/png", "png")
	raw = exportZip(t, src, owner, deckID, PackageOptions{IncludeMedia: true, MediaRoot: srcRoot})
	return raw, sha, len(body)
}

// TestImportPackageMediaQuotaRejectsAndLeavesNoTrace 是核心负例：新增媒体会超出导入者
// 配额时整包被拒，且库里没有任何行、磁盘上没有残留文件。
func TestImportPackageMediaQuotaRejectsAndLeavesNoTrace(t *testing.T) {
	srcs := packageDatabases(t)
	dsts := packageDatabases(t)
	for driver, src := range srcs {
		dst := dsts[driver]
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			raw, sha, size := quotaImportFixture(t, src)
			importer := seedUsers(t, dst, "pkg_quota_importer")[0]
			root := t.TempDir()

			// 配额比包内媒体少 1 字节：0 已用 + size 新增 > size-1。
			_, err := NewDeckStore(dst).ImportPackage(ctx, importer, bytes.NewReader(raw), PackageImportOptions{
				Target:          PackageTargetNewDeck,
				MediaRoot:       root,
				MediaQuotaBytes: int64(size - 1),
			})
			var pe *PackageError
			if !errors.As(err, &pe) || pe.Code != CodePackageQuotaExceeded {
				t.Fatalf("error = %v, want PackageError code %s", err, CodePackageQuotaExceeded)
			}

			var decks, notes, media int64
			dst.Model(&Deck{}).Count(&decks)
			dst.Model(&Note{}).Count(&notes)
			dst.Model(&Media{}).Count(&media)
			if decks != 0 || notes != 0 || media != 0 {
				t.Fatalf("rejected import wrote rows: decks=%d notes=%d media=%d", decks, notes, media)
			}
			if row, err := NewMediaStore(dst).BySha256(ctx, sha); err != nil || row != nil {
				t.Fatalf("media row = %+v err=%v, want none", row, err)
			}
			assertDirEmpty(t, root)
		})
	}
}

// TestImportPackageMediaQuotaAllowsWithinLimit 是正向对照：限额恰好等于新增字节时仍放行，
// 媒体真的落库落盘（证明新检查没有把正常导入一起拒掉）。
func TestImportPackageMediaQuotaAllowsWithinLimit(t *testing.T) {
	srcs := packageDatabases(t)
	dsts := packageDatabases(t)
	for driver, src := range srcs {
		dst := dsts[driver]
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			raw, sha, size := quotaImportFixture(t, src)
			importer := seedUsers(t, dst, "pkg_quota_ok")[0]
			root := t.TempDir()

			report, err := NewDeckStore(dst).ImportPackage(ctx, importer, bytes.NewReader(raw), PackageImportOptions{
				Target:          PackageTargetNewDeck,
				MediaRoot:       root,
				MediaQuotaBytes: int64(size),
			})
			if err != nil {
				t.Fatalf("ImportPackage: %v", err)
			}
			if report.MediaNew != 1 || report.NotesCreated != 1 {
				t.Fatalf("report media_new=%d notes_created=%d, want 1/1", report.MediaNew, report.NotesCreated)
			}
			row, err := NewMediaStore(dst).BySha256(ctx, sha)
			if err != nil || row == nil {
				t.Fatalf("media row = %+v err=%v, want the imported blob", row, err)
			}
		})
	}
}

// TestImportPackageMediaQuotaDoesNotChargeTwice 验收去重口径：同一 blob 已被导入者计费时，
// 再次导入同一 sha 的新增量是 0 —— 第二次导入在配额恰好等于该 blob 大小时仍应放行。
// 若按 size+size 计，这里会因超限被拒，说明去重没生效。
func TestImportPackageMediaQuotaDoesNotChargeTwice(t *testing.T) {
	srcs := packageDatabases(t)
	dsts := packageDatabases(t)
	for driver, src := range srcs {
		dst := dsts[driver]
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			raw, _, size := quotaImportFixture(t, src)
			importer := seedUsers(t, dst, "pkg_quota_dedupe")[0]

			// 第一次导入：媒体计入导入者用量（引用它的 note 属于该用户）。
			if _, err := NewDeckStore(dst).ImportPackage(ctx, importer, bytes.NewReader(raw), PackageImportOptions{
				Target:          PackageTargetNewDeck,
				MediaRoot:       t.TempDir(),
				MediaQuotaBytes: int64(size),
			}); err != nil {
				t.Fatalf("first import: %v", err)
			}

			// 第二次导入同一包到新卡组：同 sha 已计费，新增量 0，配额不变仍放行。
			if _, err := NewDeckStore(dst).ImportPackage(ctx, importer, bytes.NewReader(raw), PackageImportOptions{
				Target:          PackageTargetNewDeck,
				MediaRoot:       t.TempDir(),
				MediaQuotaBytes: int64(size),
			}); err != nil {
				t.Fatalf("second import (same sha) rejected, want dedupe to allow it: %v", err)
			}
		})
	}
}
