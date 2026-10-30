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

// pngBytes is a byte sequence with a PNG signature, enough to pass magic byte detection.
func pngBytes() []byte {
	return append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, []byte("fake-image-body")...)
}

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

// TestSaveDeduplicatesBySha256 is the core acceptance: uploading the same file twice stores only one copy.
func TestSaveDeduplicatesBySha256(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	body := pngBytes()

	first, err := s.Save(ctx, bytes.NewReader(body), SaveOptions{Name: "a.png"})
	if err != nil {
		t.Fatalf("first Save() error = %v", err)
	}
	second, err := s.Save(ctx, bytes.NewReader(body), SaveOptions{Name: "a.png"})
	if err != nil {
		t.Fatalf("second Save() error = %v", err)
	}
	if first.Sha256 != second.Sha256 {
		t.Fatalf("dedupe failed: first=%s second=%s", first.Sha256, second.Sha256)
	}

	var rows int64
	if err := s.db.Model(&store.Media{}).Count(&rows).Error; err != nil {
		t.Fatalf("count media rows: %v", err)
	}
	if rows != 1 {
		t.Errorf("media rows = %d, want 1", rows)
	}
	// There should be only one data file on disk (the tmp directory is empty).
	entries, err := os.ReadDir(filepath.Join(s.Root(), first.Sha256[:2]))
	if err != nil {
		t.Fatalf("read shard dir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("shard dir entries = %d, want 1", len(entries))
	}
}

// TestSaveRejectsOversized covers an oversized file being rejected with a stable error code.
func TestSaveRejectsOversized(t *testing.T) {
	s := newTestStore(t)
	body := bytes.Repeat([]byte("x"), 100)
	_, err := s.Save(context.Background(), bytes.NewReader(body), SaveOptions{
		Limit: 10, Name: "a.png",
	})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Save() error = %v, want ErrTooLarge", err)
	}
}

// TestSaveRejectsEmptyFile covers the one rejection left: a file with no detectable content.
func TestSaveRejectsEmptyFile(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Save(context.Background(), bytes.NewReader(nil), SaveOptions{Name: "empty.png"})
	if !errors.Is(err, ErrMagicMismatch) {
		t.Fatalf("Save() error = %v, want ErrMagicMismatch", err)
	}
}

// TestSaveAcceptsUnknownBytes covers the classification rule: unrecognised bytes are stored as
// application/octet-stream, and a text-class extension (here .js) decides the type instead.
func TestSaveAcceptsUnknownBytes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	unknown, err := s.Save(ctx, bytes.NewReader([]byte("this is not an image")), SaveOptions{Name: "note.txt"})
	if err != nil {
		t.Fatalf("Save() unknown bytes error = %v", err)
	}
	if unknown.Mime != "application/octet-stream" {
		t.Fatalf("unknown mime = %q, want application/octet-stream", unknown.Mime)
	}
	script, err := s.Save(ctx, bytes.NewReader([]byte("console.log(1)")), SaveOptions{Name: "tool.js"})
	if err != nil {
		t.Fatalf("Save() script error = %v", err)
	}
	if script.Mime != "text/javascript" {
		t.Fatalf("script mime = %q, want text/javascript", script.Mime)
	}
}

// TestOpenReturnsStoredBytes asserts that what is read back is exactly the bytes that were written.
func TestOpenReturnsStoredBytes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	body := pngBytes()
	saved, err := s.Save(ctx, bytes.NewReader(body), SaveOptions{Name: "a.png"})
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	m, f, err := s.Open(ctx, saved.Sha256)
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
