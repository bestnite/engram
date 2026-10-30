// Package media implements media storage on the local filesystem.
//
// Content addressing, the temporary-file + rename write and the dedup rules live in store.MediaStore.Put, shared with deck
// package import; this package streams and validates uploads, serves files and reclaims orphans. No object storage is
// involved and no server-side compression is done. This package only owns bytes and media metadata; the HTTP proxy and upload endpoints belong to internal/web.
package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/mediatype"
	"git.nite07.com/nite/engram/internal/store"
)

// Stable error codes (AGENTS.md §2.1: identifiers in English). The upload endpoint maps them to HTTP status codes;
// callers decide by code, never by matching message.
const (
	CodeTooLarge       = "media_too_large"
	CodeMimeNotAllowed = "media_mime_not_allowed"
	CodeMagicMismatch  = "media_magic_mismatch"
	// CodeQuotaExceeded means this upload would exceed that user's total media quota.
	// Usage is the deduplicated sum of "media referenced by that user's notes ∪ that user's uploads not yet referenced by anyone",
	// computed by the routing layer before writing to disk; see UserMediaUsage in internal/store.
	CodeQuotaExceeded = "media_quota_exceeded"
)

// Error is a media error carrying a stable code.
type Error struct {
	Code string
	Msg  string
}

func (e *Error) Error() string { return e.Code + ": " + e.Msg }

var (
	// ErrTooLarge means the file exceeds the administrator-configured upload limit.
	ErrTooLarge = &Error{Code: CodeTooLarge, Msg: "file exceeds the configured upload limit"}
	// ErrMimeNotAllowed means the detected type is not on the allowlist.
	ErrMimeNotAllowed = &Error{Code: CodeMimeNotAllowed, Msg: "detected media type is not allowed"}
	// ErrMagicMismatch means the declared type does not match the magic bytes, or the magic number is unrecognizable.
	ErrMagicMismatch = &Error{Code: CodeMagicMismatch, Msg: "declared media type does not match file magic bytes"}
	// ErrNotFound means the media does not exist.
	ErrNotFound = &Error{Code: "media_not_found", Msg: "media not found"}
)

// defaultMaxBytes is the default upload limit (10 MiB) and is meant to be configured by the administrator;
// the routing layer overrides it with the system settings first.
const defaultMaxBytes = 10 * 1024 * 1024

// DefaultMaxBytes exposes the default limit so the routing layer can fall back when the system setting is missing.
func DefaultMaxBytes() int64 { return defaultMaxBytes }

// The settings key and environment variable override name for the upload limit. Web uploads and REST deck-package import
// share the single resolution below, so the two entry points do not each define their own constants and drift apart.
const (
	SettingKeyMediaMaxBytes = "media_max_bytes"
	EnvMediaMaxBytes        = "MEDIA_MAX_BYTES"
)

// ResolveMaxBytes resolves the effective upload byte limit: environment variable > settings table > default 10 MiB.
// The precedence and environment variable semantics match internal/config; the settings table is read per request, so changes take effect immediately.
// Both Web upload and REST import must go through here, keeping "the size limit and the upload limit configured in one place".
func ResolveMaxBytes(ctx context.Context, db *gorm.DB) int64 {
	if raw := strings.TrimSpace(os.Getenv(EnvMediaMaxBytes)); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	if db != nil {
		if settings, err := store.LoadSettings(ctx, db); err == nil {
			if raw, ok := settings[SettingKeyMediaMaxBytes]; ok {
				if n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil && n > 0 {
					return n
				}
			}
		}
	}
	return defaultMaxBytes
}

// The settings key and environment variable override name for the per-user total media quota.
// Web upload, REST/MCP/CLI import and Web import share the single resolution below, so all four entry points use one definition.
const (
	SettingKeyMediaUserQuotaBytes = "media_user_quota_bytes"
	EnvMediaUserQuotaBytes        = "MEDIA_USER_QUOTA_BYTES"
)

// ResolveUserQuotaBytes resolves the effective per-user total media quota (bytes): environment variable > settings table > 0.
// 0 (including unconfigured) means unlimited — defaulting to off is deliberate: it does not pick a number on the administrator's behalf that nobody agreed to.
//
// The upload path (checkMediaQuota in internal/web) and the deck-package import path (MediaQuotaBytes in
// store.ImportPackage) both take the value from here, so "newly added media bytes count against the importer's quota" is consistent across all entry points.
func ResolveUserQuotaBytes(ctx context.Context, db *gorm.DB) int64 {
	if raw := strings.TrimSpace(os.Getenv(EnvMediaUserQuotaBytes)); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	if db != nil {
		if settings, err := store.LoadSettings(ctx, db); err == nil {
			if raw, ok := settings[SettingKeyMediaUserQuotaBytes]; ok {
				if n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil && n > 0 {
					return n
				}
			}
		}
	}
	return 0
}

// Store is the local media store: root is the directory root, db records metadata.
type Store struct {
	root string
	db   *gorm.DB
}

// New constructs the store and ensures the root directory and temporary directory exist.
func New(root string, db *gorm.DB) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("media: root directory is required")
	}
	s := &Store{root: root, db: db}
	for _, dir := range []string{s.root, s.tempDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("media: create %s: %w", dir, err)
		}
	}
	return s, nil
}

// Root returns the storage root directory.
func (s *Store) Root() string { return s.root }

func (s *Store) tempDir() string { return store.MediaTempDir(s.root) }

// absPath joins a relative path into an absolute one and blocks paths escaping the root directory (defensive).
func (s *Store) absPath(rel string) (string, error) {
	clean := filepath.Clean(rel)
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("media: unsafe relative path %q", rel)
	}
	return filepath.Join(s.root, clean), nil
}

// SaveOptions holds the inputs of one save.
type SaveOptions struct {
	// Limit is the per-file byte limit; the default is used when <=0.
	Limit int64
	// AllowedMimes is the set of allowed mimes; the default allowlist is used when empty.
	AllowedMimes []string
	// DeclaredMime is the client-declared Content-Type, cross-checked against the magic bytes (may be empty).
	DeclaredMime string
	// CreatedBy is the uploading user id (nullable).
	CreatedBy *uint64
}

// Save writes one piece of media: reads into a temporary file up to the limit, validates the magic bytes, then stores it
// through store.MediaStore.Put, which owns dedup, placement and metadata. On a dedup hit the returned Media is the row
// that already existed.
func (s *Store) Save(ctx context.Context, r io.Reader, opts SaveOptions) (*store.Media, error) {
	if s.db == nil {
		return nil, errors.New("media: save requires a database")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultMaxBytes
	}
	allowed := opts.AllowedMimes
	if len(allowed) == 0 {
		allowed = mediatype.DefaultAllowedMimes()
	}

	tmp, err := os.CreateTemp(s.tempDir(), "upload-*")
	if err != nil {
		return nil, fmt.Errorf("media: create temp file: %w", err)
	}
	tmpName := tmp.Name()
	// Put renames the temporary file into place on a fresh write; every other outcome leaves it here to be removed.
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()

	hasher := sha256.New()
	// Read 1 extra byte to detect "over the limit" instead of reading an oversized file to the end.
	written, err := io.Copy(io.MultiWriter(tmp, hasher), io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("media: write temp file: %w", err)
	}
	if written > limit {
		return nil, ErrTooLarge
	}
	if written == 0 {
		return nil, &Error{Code: CodeMagicMismatch, Msg: "empty file"}
	}
	if err := tmp.Sync(); err != nil {
		return nil, fmt.Errorf("media: sync temp file: %w", err)
	}

	// Read the file header back for magic byte validation: trust only the bytes, never the extension.
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("media: seek temp file: %w", err)
	}
	head := make([]byte, mediatype.HeadBytes)
	n, err := io.ReadFull(tmp, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("media: read temp head: %w", err)
	}
	detected, _, ok := mediatype.Detect(head[:n])
	if !ok {
		return nil, ErrMagicMismatch
	}
	if !mediatype.Allowed(detected, allowed) {
		return nil, ErrMimeNotAllowed
	}
	// When the client declared a type it must match the magic number, otherwise the file is disguised.
	if declared := mediatype.Normalize(opts.DeclaredMime); declared != "" && declared != detected {
		return nil, ErrMagicMismatch
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("media: close temp file: %w", err)
	}

	row, _, err := store.NewMediaStore(s.db).Put(ctx, s.root, store.MediaPut{
		Sha256:    hex.EncodeToString(hasher.Sum(nil)),
		Mime:      detected,
		Bytes:     written,
		TempPath:  tmpName,
		CreatedBy: opts.CreatedBy,
	})
	return row, err
}

// Open fetches the metadata by sha256 and opens the file; returns ErrNotFound when it does not exist.
func (s *Store) Open(ctx context.Context, sha string) (*store.Media, *os.File, error) {
	if s.db == nil {
		return nil, nil, ErrNotFound
	}
	var m store.Media
	if err := s.db.WithContext(ctx).First(&m, "sha256 = ?", sha).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, fmt.Errorf("media: load metadata: %w", err)
	}
	abs, err := s.absPath(m.RelPath)
	if err != nil {
		return nil, nil, err
	}
	f, err := os.Open(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, fmt.Errorf("media: open file: %w", err)
	}
	return &m, f, nil
}

// bySha256 looks up metadata by sha256; returns (nil, nil) when absent.
func (s *Store) bySha256(ctx context.Context, sum string) (*store.Media, error) {
	if s.db == nil {
		return nil, nil
	}
	var m store.Media
	err := s.db.WithContext(ctx).Where("sha256 = ?", sum).First(&m).Error
	if err == nil {
		return &m, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return nil, fmt.Errorf("media: lookup sha256: %w", err)
}

// gcBatch caps the number of media rows handled in one delete transaction, so a single transaction does not grow without bound with the number of orphans.
const gcBatch = 200

// CollectGarbage reclaims orphaned media: refresh the orphan flags first, then delete the media rows and files whose
// orphaned time is earlier than now-grace. Returns the number of deleted media.
//
// Files are removed after the database row deletion commits; before removing, confirm the bytes were not re-uploaded
// (a re-upload creates a new media row at the same path), otherwise a freshly written file would be deleted.
// A file that no longer exists is not an error.
func (s *Store) CollectGarbage(ctx context.Context, now time.Time, grace time.Duration) (int64, error) {
	if s.db == nil {
		return 0, nil
	}
	if _, _, err := store.MarkOrphanedMedia(ctx, s.db, now); err != nil {
		return 0, err
	}
	var removed int64
	for {
		rows, err := store.DeleteOrphanedMedia(ctx, s.db, now.Add(-grace), gcBatch)
		if err != nil {
			return removed, err
		}
		for _, m := range rows {
			if again, err := s.bySha256(ctx, m.Sha256); err != nil {
				return removed, err
			} else if again != nil {
				continue
			}
			abs, err := s.absPath(m.RelPath)
			if err != nil {
				return removed, err
			}
			if err := os.Remove(abs); err != nil && !errors.Is(err, os.ErrNotExist) {
				return removed, fmt.Errorf("media: remove orphaned file %s: %w", m.Sha256, err)
			}
		}
		removed += int64(len(rows))
		if len(rows) < gcBatch {
			return removed, nil
		}
	}
}
