package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"testing"
)

// MCP/CLI 的 base64 归档入口在解码前先按「归档上限对应的展开上界」拒绝超限输入，
// 绝不对超限串做整体解码。

// TestPackageReaderRejectsOversizedBase64BeforeDecoding 断言超限的 base64 串在解码前被拒。
// 用一个既超限、又含非法 base64 字符（'!'）的串：若实现先解码，必然得到解码错误；
// 只有「先判长度」才会得到 package_too_large。
func TestPackageReaderRejectsOversizedBase64BeforeDecoding(t *testing.T) {
	// MaxTotalBytes=30 时展开上界 EncodedLen(30)=40 字节；64 > 40，必被长度校验拦下。
	limits := PackageLimits{MaxEntries: 8, MaxFileBytes: 1 << 20, MaxTotalBytes: 30}
	oversize := strings.Repeat("!", 64)
	_, err := packageReader(oversize, limits)
	if err == nil {
		t.Fatal("oversized invalid base64 was accepted")
	}
	var pe *PackageError
	if !errors.As(err, &pe) || pe.Code != CodePackageTooLarge {
		t.Fatalf("error = %v, want PackageError code %s (length check must run before decode)", err, CodePackageTooLarge)
	}
}

// TestPackageReaderBase64Boundary 断言展开上界本身不被误拒：恰好等于上界的串被接受，
// 超过上界即拒。
func TestPackageReaderBase64Boundary(t *testing.T) {
	limits := PackageLimits{MaxEntries: 8, MaxFileBytes: 1 << 20, MaxTotalBytes: 3}
	// EncodedLen(3)=4：'YWJj' 解码为 3 字节，恰好在上界内，放行。
	exact := base64.StdEncoding.EncodeToString([]byte("abc"))
	if len(exact) != 4 {
		t.Fatalf("test setup: encoded len = %d, want 4", len(exact))
	}
	r, err := packageReader(exact, limits)
	if err != nil {
		t.Fatalf("base64 at the expansion bound was rejected: %v", err)
	}
	got, err := io.ReadAll(r)
	if err != nil || !bytes.Equal(got, []byte("abc")) {
		t.Fatalf("decoded = %q err %v, want %q", got, err, "abc")
	}
	// 比上界多一个字符：即使内容巧合合法，也必须被拒（不上界外溢）。
	if _, err := packageReader(strings.Repeat("A", 5), limits); err == nil {
		t.Fatal("base64 one byte over the expansion bound was accepted")
	}
}

// TestImportViaBase64PackageReader 是正向对照：一个合法的 base64 归档经 PackageReader
// 仍能完整导入（真实 SQLite 往返，复用 store 层已有的包构造工具）。
func TestImportViaBase64PackageReader(t *testing.T) {
	srcs := packageDatabases(t)
	dsts := packageDatabases(t)
	for driver, src := range srcs {
		dst := dsts[driver]
		t.Run(driver, func(t *testing.T) {
			owner := seedUsers(t, src, "pkg_b64_owner")[0]
			deckID, _ := seedPackageDeck(t, src, owner)
			raw := exportZip(t, src, owner, deckID, PackageOptions{IncludeMedia: false})

			r, err := PackageReader(base64.StdEncoding.EncodeToString(raw))
			if err != nil {
				t.Fatalf("PackageReader: %v", err)
			}
			importer := seedUsers(t, dst, "pkg_b64_importer")[0]
			report, err := NewDeckStore(dst).ImportPackage(context.Background(), importer, r, PackageImportOptions{Target: PackageTargetNewDeck})
			if err != nil {
				t.Fatalf("ImportPackage: %v", err)
			}
			if report.NotesCreated != 4 {
				t.Fatalf("notes_created = %d, want 4", report.NotesCreated)
			}
		})
	}
}
