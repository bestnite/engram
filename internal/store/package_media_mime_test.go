package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// 卡组包导入的媒体写入必须与普通上传同规——以真实字节判定类型、白名单放行、与
// media.json 的声明交叉校验。修前这些用例全红：SaveBytesTracked 直接采信包内声明的 mime，
// 于是 mime=text/html 的包会被原样写库，读取时又原样成为 Content-Type（同源存储型 XSS）。

// htmlMediaBytes 是一段会被浏览器当脚本/HTML 执行的内容。
func htmlMediaBytes() []byte {
	return []byte("<html><body><script>alert(document.domain)</script></body></html>")
}

// mediaPackageZip 构造一个结构合法的 .edeck：一条引用媒体的 note + media.json + 媒体字节。
// declaredMime 写进 media.json；body 是媒体条目的真实字节。sha256 由 body 算出。
func mediaPackageZip(t *testing.T, declaredMime string, body []byte) (raw []byte, sha string) {
	t.Helper()
	sum := sha256.Sum256(body)
	sha = hex.EncodeToString(sum[:])
	mediaPath := "media/" + sha + ".png"
	files := map[string]string{
		"manifest.json": fmt.Sprintf(`{"format_version":1,"exported_at":"2026-10-02T00:00:00Z","deck":{"name":"d"},"include_progress":false,"include_media":true,"include_reviews":false,"counts":{"notes":1,"cards":0,"media":1}}`),
		"notes.json":    fmt.Sprintf(`[{"kind":"basic","fields":{"front":"q","back":"%s"}}]`, mediaPath),
		"cards.json":    `[]`,
		"preset.json":   `{"desired_retention":0.9,"learning_steps":"1m","relearning_steps":"10m","maximum_interval_days":100,"enable_fuzz":true,"weights":null,"weights_optimized_at":null,"weights_review_count":null}`,
		"media.json":    fmt.Sprintf(`{"%s":{"path":"%s","mime":"%s"}}`, sha, mediaPath, declaredMime),
		mediaPath:       string(body),
	}
	return buildZip(t, files), sha
}

// TestImportRejectsUnsafeMedia 断言导入链在落盘前用真实字节判定类型：
// 不在白名单内的类型、或与 media.json 声明不符的媒体，整包被拒并逐条列出出错媒体。
func TestImportRejectsUnsafeMedia(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nfake-png-body")
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"></svg>`)
	cases := []struct {
		name     string
		declared string
		body     []byte
	}{
		{"declared text/html, bytes html", "text/html", htmlMediaBytes()},
		{"declared image/svg+xml, bytes svg", "image/svg+xml", svg},
		{"declared image/png, bytes html", "image/png", htmlMediaBytes()},
		{"declared image/gif, bytes png (mismatch)", "image/gif", png},
	}
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					raw, sha := mediaPackageZip(t, tc.declared, tc.body)
					importer := seedUsers(t, db, "pkg_mime_"+sanitizeName(tc.name))[0]
					root := t.TempDir()

					_, err := NewDeckStore(db).ImportPackage(context.Background(), importer, bytes.NewReader(raw), PackageImportOptions{
						Target:    PackageTargetNewDeck,
						MediaRoot: root,
					})
					var pe *PackageError
					if !errors.As(err, &pe) || pe.Code != CodePackageUnsafeMedia {
						t.Fatalf("ImportPackage error = %v, want PackageError code %s", err, CodePackageUnsafeMedia)
					}
					if len(pe.Entries) == 0 || !entryContains(pe.Entries, sha) {
						t.Fatalf("entries = %v, want one naming media/%s", pe.Entries, sha)
					}
					// 拒绝发生在写盘之前：库里没有 media 行，磁盘上没有文件。
					if row, err := NewMediaStore(db).BySha256(context.Background(), sha); err != nil {
						t.Fatalf("BySha256: %v", err)
					} else if row != nil {
						t.Fatalf("rejected import created media metadata %+v", row)
					}
					assertDirEmpty(t, root)
				})
			}
		})
	}
}

// TestImportDryRunRejectsUnsafeMediaWritesNothing 断言 dry_run 下非法媒体包同样被拒且零写入。
func TestImportDryRunRejectsUnsafeMediaWritesNothing(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			raw, _ := mediaPackageZip(t, "text/html", htmlMediaBytes())
			importer := seedUsers(t, db, "pkg_mime_dry")[0]
			root := t.TempDir()

			_, err := NewDeckStore(db).ImportPackage(context.Background(), importer, bytes.NewReader(raw), PackageImportOptions{
				Target:    PackageTargetNewDeck,
				DryRun:    true,
				MediaRoot: root,
			})
			var pe *PackageError
			if !errors.As(err, &pe) || pe.Code != CodePackageUnsafeMedia {
				t.Fatalf("dry-run ImportPackage error = %v, want PackageError code %s", err, CodePackageUnsafeMedia)
			}
			var decks, notes, media int64
			db.Model(&Deck{}).Count(&decks)
			db.Model(&Note{}).Count(&notes)
			db.Model(&Media{}).Count(&media)
			if decks != 0 || notes != 0 || media != 0 {
				t.Fatalf("dry run wrote to the database: decks=%d notes=%d media=%d", decks, notes, media)
			}
			assertDirEmpty(t, root)
		})
	}
}

// TestImportAllowsWhitelistedMediaRoundTrip 是正向用例：合法 PNG 与音频仍能导入并往返，
// 证明新校验没有把正常功能一起拒掉。
func TestImportAllowsWhitelistedMediaRoundTrip(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nlegal-round-trip-payload")
	ogg := []byte("OggS\x00\x02legal-audio-payload")
	cases := []struct {
		name string
		body []byte
		mime string
		ext  string
	}{
		{"png", png, "image/png", "png"},
		{"audio", ogg, "audio/ogg", "ogg"},
	}
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					raw, sha := mediaPackageZip(t, tc.mime, tc.body)
					importer := seedUsers(t, db, "pkg_mime_ok_"+tc.name)[0]
					root := t.TempDir()

					report, err := NewDeckStore(db).ImportPackage(context.Background(), importer, bytes.NewReader(raw), PackageImportOptions{
						Target:    PackageTargetNewDeck,
						MediaRoot: root,
					})
					if err != nil {
						t.Fatalf("ImportPackage: %v", err)
					}
					if report.MediaNew != 1 || report.NotesCreated != 1 {
						t.Fatalf("report media_new=%d notes_created=%d, want 1/1", report.MediaNew, report.NotesCreated)
					}
					row, err := NewMediaStore(db).BySha256(context.Background(), sha)
					if err != nil || row == nil {
						t.Fatalf("load imported media: row=%+v err=%v", row, err)
					}
					if row.Mime != tc.mime {
						t.Fatalf("stored mime = %q, want %q", row.Mime, tc.mime)
					}
					onDisk, err := os.ReadFile(mediaAbsPath(root, row.RelPath))
					if err != nil {
						t.Fatalf("read imported bytes: %v", err)
					}
					if !bytes.Equal(onDisk, tc.body) {
						t.Fatalf("imported bytes differ from source")
					}
				})
			}
		})
	}
}

// sanitizeName 把子测试名里的空格/斜杠换成下划线，用作唯一用户名后缀。
func sanitizeName(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

// mediaAbsPath 把 media 行的相对路径拼成磁盘绝对路径（与 SaveBytesTracked 的拼法一致）。
func mediaAbsPath(root, rel string) string {
	return filepath.Join(root, filepath.FromSlash(rel))
}

// assertDirEmpty 断言目录下没有任何文件，证明失败路径没有落盘。
func assertDirEmpty(t *testing.T, root string) {
	t.Helper()
	var files int
	if err := filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files++
		}
		return nil
	}); err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if files != 0 {
		t.Fatalf("media root %s holds %d file(s), want 0", root, files)
	}
}
