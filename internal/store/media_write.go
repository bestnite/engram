package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"git.nite07.com/nite/engram/internal/mediatype"
)

// Content-addressed media writes. Every entry point that adds media bytes (Web upload in internal/media, deck package
// import here) ends in MediaStore.Put, so dedup, the on-disk layout, uploader ownership and the orphan mark follow one rule:
//
//   - files live at <root>/<sha256[:2]>/<sha256>.<ext>, so identical bytes are stored once instance-wide;
//   - a file reaches its final path only by a same-directory rename from <root>/tmp, so a reader never sees a half-written file;
//   - every caller that supplies bytes is recorded in media_uploaders, on a dedup hit as well as on a fresh write;
//   - a dedup hit clears orphaned_at, so bytes that are about to be used again restart their reclamation grace period.

// NewMediaStore builds the media metadata store. Pass a transaction handle to make the metadata row part of that transaction.
func NewMediaStore(db *gorm.DB) *MediaStore { return &MediaStore{db: db} }

// MediaStore wraps metadata access to the media table; the bytes stay on the local filesystem.
type MediaStore struct{ db *gorm.DB }

// BySha256 fetches media metadata by content address; absent returns (nil, nil).
func (s *MediaStore) BySha256(ctx context.Context, sha string) (*Media, error) {
	var m Media
	err := s.db.WithContext(ctx).Where("sha256 = ?", sha).First(&m).Error
	if err == nil {
		return &m, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return nil, err
}

// MediaTempDir is the staging directory for media writes. It sits under the media root so the final rename stays on one
// filesystem and is atomic.
func MediaTempDir(root string) string { return filepath.Join(root, "tmp") }

// MediaPut is one content-addressed write: a staged file holding exactly the bytes whose digest is Sha256.
type MediaPut struct {
	Sha256 string
	// Mime must already be determined from the bytes; it is stored as the truth and picks the file extension.
	Mime  string
	Bytes int64
	// TempPath is the staged file under MediaTempDir(root). Put renames it into place on a fresh write; on a dedup hit it
	// is left alone, so the caller always removes it afterwards (a missing file is not an error).
	TempPath  string
	CreatedBy *uint64
}

// Put stores one piece of media and returns its metadata row. On a dedup hit the returned row is the one that already existed.
//
// written is the absolute path of a file this call moved into place, and empty when the bytes were already stored. It may be
// set together with an error: the file landed but the metadata did not, and a caller whose transaction rolls back must remove
// it (see cleanupMediaWrites).
func (s *MediaStore) Put(ctx context.Context, root string, in MediaPut) (row *Media, written string, err error) {
	if existing, err := s.BySha256(ctx, in.Sha256); err != nil {
		return nil, "", fmt.Errorf("media: lookup sha256: %w", err)
	} else if existing != nil {
		return s.reuse(ctx, existing, in.CreatedBy)
	}

	rel := contentAddressedRelPath(in.Sha256, in.Mime)
	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, "", fmt.Errorf("media: create shard dir: %w", err)
	}
	// A rename onto an existing file replaces it atomically. If the rename fails but the file is there, a concurrent
	// writer already placed the same bytes, so reuse them.
	if err := os.Rename(in.TempPath, abs); err != nil {
		if _, statErr := os.Stat(abs); statErr != nil {
			return nil, "", fmt.Errorf("media: commit file: %w", err)
		}
	} else {
		written = abs
	}

	row = &Media{
		Sha256:    in.Sha256,
		RelPath:   rel,
		Mime:      in.Mime,
		Bytes:     in.Bytes,
		CreatedBy: in.CreatedBy,
		CreatedAt: time.Now().UTC(),
	}
	stored, inserted, err := s.writeRow(ctx, row, in.CreatedBy)
	if err != nil {
		return nil, written, err
	}
	if !inserted {
		// A concurrent writer committed the same sha first: its row owns the file, so nothing here counts as newly written.
		return stored, "", nil
	}
	return stored, written, nil
}

// writeRow inserts one media metadata row and reports whether this call inserted it.
//
// DO NOTHING instead of catching the unique violation: on PostgreSQL a failed INSERT aborts the surrounding transaction,
// which would make the re-read below fail inside a package import. When another writer committed the same sha first, its
// row wins and this caller still becomes an uploader, exactly as a fresh insert does.
func (s *MediaStore) writeRow(ctx context.Context, row *Media, createdBy *uint64) (*Media, bool, error) {
	res := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(row)
	if res.Error != nil {
		return nil, false, fmt.Errorf("media: record metadata: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		existing, err := s.BySha256(ctx, row.Sha256)
		if err != nil {
			return nil, false, fmt.Errorf("media: reload concurrent metadata: %w", err)
		}
		if existing == nil {
			return nil, false, fmt.Errorf("media: metadata for %s conflicted on insert but is not readable", row.Sha256)
		}
		reused, _, err := s.reuse(ctx, existing, createdBy)
		return reused, false, err
	}
	if createdBy != nil {
		if err := RecordMediaUploader(ctx, s.db, row.Sha256, *createdBy); err != nil {
			return nil, true, err
		}
	}
	return row, true, nil
}

// reuse applies the dedup-hit rules to an existing row: record this caller as an uploader, so it can still read the bytes it
// supplied after another user's sharing is revoked, and clear the orphan mark.
func (s *MediaStore) reuse(ctx context.Context, existing *Media, createdBy *uint64) (*Media, string, error) {
	if createdBy != nil {
		if err := RecordMediaUploader(ctx, s.db, existing.Sha256, *createdBy); err != nil {
			return nil, "", err
		}
	}
	if existing.OrphanedAt != nil {
		if err := s.db.WithContext(ctx).Model(&Media{}).Where("sha256 = ?", existing.Sha256).
			Update("orphaned_at", nil).Error; err != nil {
			return nil, "", fmt.Errorf("media: clear orphan mark: %w", err)
		}
		existing.OrphanedAt = nil
	}
	return existing, "", nil
}

// SaveBytes stores in-memory bytes through Put. The mime is trusted as given: callers determine it from the bytes first.
func (s *MediaStore) SaveBytes(ctx context.Context, root, mime string, raw []byte, createdBy *uint64) (*Media, error) {
	m, _, err := s.SaveBytesTracked(ctx, root, mime, raw, createdBy)
	return m, err
}

// SaveBytesTracked is SaveBytes that also returns the path Put moved into place (see Put for when it is set).
func (s *MediaStore) SaveBytesTracked(ctx context.Context, root, mime string, raw []byte, createdBy *uint64) (*Media, string, error) {
	sum := sha256.Sum256(raw)
	tmpName, err := s.stageBytes(root, bytes.NewReader(raw))
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = os.Remove(tmpName) }()
	return s.Put(ctx, root, MediaPut{
		Sha256:    hex.EncodeToString(sum[:]),
		Mime:      mime,
		Bytes:     int64(len(raw)),
		TempPath:  tmpName,
		CreatedBy: createdBy,
	})
}

// mediaFileWrite is one stream this process placed at its content address before a database
// transaction opened.
//
// It exists for imports that may decompress to gigabytes: copying the bytes inside the transaction
// would hold the database write lock for the whole copy (on SQLite every other writer waits or
// fails), so the file is placed first and only the metadata row is written inside the transaction.
type mediaFileWrite struct {
	Sha256 string
	Mime   string
	Bytes  int64
	// AbsPath is the content-addressed path the bytes were placed at.
	AbsPath string
	// Created is true only when this call created the file. An existing path is never reported as
	// created, so a caller that removes the files of a failed run never removes a file that was
	// there before it started.
	Created bool
}

// saveStreamFile places r at its content-addressed path without touching the database.
//
// sha is the digest the caller already computed and must equal the digest of the stream; a mismatch
// is an error rather than a silently misaddressed file. The temporary file lives under root/tmp and
// is renamed into the shard directory, so a reader never sees a half-written file. The metadata row
// is the caller's to write later, inside its transaction, with recordFile.
func (s *MediaStore) saveStreamFile(root, mime, sha string, r io.Reader) (mediaFileWrite, error) {
	if len(sha) < 2 {
		return mediaFileWrite{}, fmt.Errorf("media: content address %q is too short", sha)
	}
	sum := sha256.New()
	tmpName, err := s.stageBytes(root, io.TeeReader(r, sum))
	if err != nil {
		return mediaFileWrite{}, err
	}
	defer func() { _ = os.Remove(tmpName) }()
	if got := hex.EncodeToString(sum.Sum(nil)); got != sha {
		return mediaFileWrite{}, fmt.Errorf("media: stream digest %s does not match %s", got, sha)
	}
	info, err := os.Stat(tmpName)
	if err != nil {
		return mediaFileWrite{}, fmt.Errorf("media: stat temporary file: %w", err)
	}
	abs := contentAddressedPath(root, sha, mime)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return mediaFileWrite{}, fmt.Errorf("media: create shard dir: %w", err)
	}
	_, statErr := os.Stat(abs)
	existed := statErr == nil
	if err := os.Rename(tmpName, abs); err != nil {
		if _, retryErr := os.Stat(abs); retryErr != nil {
			return mediaFileWrite{}, fmt.Errorf("media: commit file: %w", err)
		}
		// A concurrent writer placed the same bytes: the path already held them.
		existed = true
	}
	return mediaFileWrite{Sha256: sha, Mime: mime, Bytes: info.Size(), AbsPath: abs, Created: !existed}, nil
}

// recordFile writes the metadata row of a file placed by saveStreamFile or Put, carrying the same
// dedup rules as Put: a row another writer committed first wins, and this caller still becomes an
// uploader.
func (s *MediaStore) recordFile(ctx context.Context, file mediaFileWrite, createdBy *uint64) (*Media, error) {
	if len(file.Sha256) < 2 {
		return nil, fmt.Errorf("media: content address %q is too short", file.Sha256)
	}
	row := &Media{
		Sha256:    file.Sha256,
		RelPath:   contentAddressedRelPath(file.Sha256, file.Mime),
		Mime:      file.Mime,
		Bytes:     file.Bytes,
		CreatedBy: createdBy,
		CreatedAt: time.Now().UTC(),
	}
	stored, _, err := s.writeRow(ctx, row, createdBy)
	return stored, err
}

// contentAddressedRelPath is the one on-disk layout of media bytes: <sha256[:2]>/<sha256>.<ext>.
func contentAddressedRelPath(sha, mime string) string {
	return filepath.Join(sha[:2], sha+"."+mediatype.Ext(mime))
}

// contentAddressedPath is the absolute form of contentAddressedRelPath under root.
func contentAddressedPath(root, sha, mime string) string {
	return filepath.Join(root, contentAddressedRelPath(sha, mime))
}

// stageBytes copies one stream into a fresh temporary file under root/tmp and returns its path.
// The caller removes the path after Put, because Put renames it into place on a fresh write and
// leaves it untouched on a dedup hit.
func (s *MediaStore) stageBytes(root string, r io.Reader) (string, error) {
	tmpDir := MediaTempDir(root)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return "", fmt.Errorf("media: create temp dir: %w", err)
	}
	tmp, err := os.CreateTemp(tmpDir, "write-*")
	if err != nil {
		return "", fmt.Errorf("media: create temp file: %w", err)
	}
	tmpName := tmp.Name()
	_, werr := io.Copy(tmp, r)
	if werr == nil {
		werr = tmp.Sync()
	}
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("media: write temp file: %w", werr)
	}
	return tmpName, nil
}
