package media

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// pngBytes 是一段带 PNG 签名的字节，足以让 magic bytes 探测通过。
func pngBytes() []byte {
	return append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, []byte("fake-image-body")...)
}

// gifBytes 是一段带 GIF89a 签名的字节。
func gifBytes() []byte { return []byte("GIF89a fake body") }

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "media.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	s, err := New(filepath.Join(t.TempDir(), "root"), db)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return s
}

// TestSaveDeduplicatesBySha256 是 M2-8 的核心验收：同一文件上传两次只存一份。
func TestSaveDeduplicatesBySha256(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	body := pngBytes()

	first, err := s.Save(ctx, bytes.NewReader(body), SaveOptions{DeclaredMime: "image/png"})
	if err != nil {
		t.Fatalf("first Save() error = %v", err)
	}
	second, err := s.Save(ctx, bytes.NewReader(body), SaveOptions{DeclaredMime: "image/png"})
	if err != nil {
		t.Fatalf("second Save() error = %v", err)
	}
	if first.ID != second.ID || first.Sha256 != second.Sha256 {
		t.Fatalf("dedupe failed: first=%d/%s second=%d/%s", first.ID, first.Sha256, second.ID, second.Sha256)
	}

	var rows int64
	if err := s.db.Model(&store.Media{}).Count(&rows).Error; err != nil {
		t.Fatalf("count media rows: %v", err)
	}
	if rows != 1 {
		t.Errorf("media rows = %d, want 1", rows)
	}
	// 磁盘上只应有一个数据文件（tmp 目录为空）。
	entries, err := os.ReadDir(filepath.Join(s.Root(), first.Sha256[:2]))
	if err != nil {
		t.Fatalf("read shard dir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("shard dir entries = %d, want 1", len(entries))
	}
}

// TestSaveRejectsOversized 覆盖超大文件被拒且返回稳定错误码。
func TestSaveRejectsOversized(t *testing.T) {
	s := newTestStore(t)
	body := bytes.Repeat([]byte("x"), 100)
	_, err := s.Save(context.Background(), bytes.NewReader(body), SaveOptions{
		Limit: 10, AllowedMimes: DefaultAllowedMimes(), DeclaredMime: "image/png",
	})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Save() error = %v, want ErrTooLarge", err)
	}
}

// TestSaveRejectsMagicMismatch 覆盖声明类型与魔数不符被拒。
func TestSaveRejectsMagicMismatch(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Save(context.Background(), bytes.NewReader([]byte("this is not an image")), SaveOptions{
		DeclaredMime: "image/png",
	})
	if !errors.Is(err, ErrMagicMismatch) {
		t.Fatalf("Save() error = %v, want ErrMagicMismatch", err)
	}
}

// TestSaveRejectsDisallowedMime 覆盖探测到的类型不在白名单内被拒。
func TestSaveRejectsDisallowedMime(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Save(context.Background(), bytes.NewReader(gifBytes()), SaveOptions{
		AllowedMimes: []string{"image/png"},
		DeclaredMime: "image/gif",
	})
	if !errors.Is(err, ErrMimeNotAllowed) {
		t.Fatalf("Save() error = %v, want ErrMimeNotAllowed", err)
	}
}

// TestOpenReturnsStoredBytes 断言读回的就是写进去的字节。
func TestOpenReturnsStoredBytes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	body := pngBytes()
	saved, err := s.Save(ctx, bytes.NewReader(body), SaveOptions{DeclaredMime: "image/png"})
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	m, f, err := s.Open(ctx, saved.ID)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer f.Close()
	got, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("read stored file: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("stored bytes differ from input")
	}
	if m.Mime != "image/png" {
		t.Errorf("mime = %q, want image/png", m.Mime)
	}
}
