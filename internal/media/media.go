// Package media 实现本地文件系统的媒体存储（DESIGN.md §6.3）。
//
// 内容寻址：文件按 <sha256[:2]>/<sha256>.<ext> 存放，同一份字节天然只存一份；
// 落盘用临时文件 + rename，避免读者看到半截文件；不接任何对象存储，也不做服务端压缩。
// 本包只负责字节与 media 元数据，HTTP 代理与上传端点属于 internal/web。
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
	"strings"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/mediatype"
	"git.nite07.com/nite/engram/internal/store"
)

// 稳定的错误码（AGENTS.md §2.1：标识符英文）。上传端点把它们映射成 HTTP 状态码，
// 调用方按 code 判断，不要匹配 message。
const (
	CodeTooLarge       = "media_too_large"
	CodeMimeNotAllowed = "media_mime_not_allowed"
	CodeMagicMismatch  = "media_magic_mismatch"
	// CodeQuotaExceeded 表示本次上传会超出该用户的媒体总量配额（M2-13）。
	// 用量是「该用户 note 引用到的媒体去重求和」，由路由层在落盘前算出，见 internal/store 的 UserMediaUsage。
	CodeQuotaExceeded = "media_quota_exceeded"
)

// Error 是带稳定 code 的媒体错误。
type Error struct {
	Code string
	Msg  string
}

func (e *Error) Error() string { return e.Code + ": " + e.Msg }

var (
	// ErrTooLarge 表示文件超过管理员配置的上传上限。
	ErrTooLarge = &Error{Code: CodeTooLarge, Msg: "file exceeds the configured upload limit"}
	// ErrMimeNotAllowed 表示探测到的类型不在白名单内。
	ErrMimeNotAllowed = &Error{Code: CodeMimeNotAllowed, Msg: "detected media type is not allowed"}
	// ErrMagicMismatch 表示声明的类型与 magic bytes 不符，或魔数无法识别。
	ErrMagicMismatch = &Error{Code: CodeMagicMismatch, Msg: "declared media type does not match file magic bytes"}
	// ErrNotFound 表示媒体不存在。
	ErrNotFound = &Error{Code: "media_not_found", Msg: "media not found"}
)

// defaultMaxBytes 是上传上限的默认值（10 MiB）；DESIGN.md §6.3 要求由管理员配置，
// 路由层会先用系统设置覆盖它。
const defaultMaxBytes = 10 * 1024 * 1024

// DefaultMaxBytes 暴露默认上限，供路由层在系统设置缺失时回退。
func DefaultMaxBytes() int64 { return defaultMaxBytes }

// DefaultAllowedMimes 是 DESIGN.md §6.3 的默认白名单；判定实现已抽到叶子包 mediatype，
// 这里保留同名入口，避免改动大量调用点（F9）。
func DefaultAllowedMimes() []string { return mediatype.DefaultAllowedMimes() }

// Store 是本地媒体存储：root 是目录根，db 记录元数据。
type Store struct {
	root string
	db   *gorm.DB
}

// New 构造存储并确保根目录与临时目录存在。
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

// Root 返回存储根目录。
func (s *Store) Root() string { return s.root }

func (s *Store) tempDir() string { return filepath.Join(s.root, "tmp") }

// absPath 把相对路径拼成绝对路径，并挡住越出根目录的路径（防御性）。
func (s *Store) absPath(rel string) (string, error) {
	clean := filepath.Clean(rel)
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("media: unsafe relative path %q", rel)
	}
	return filepath.Join(s.root, clean), nil
}

// SaveOptions 是一次保存的入参。
type SaveOptions struct {
	// Limit 是单文件字节上限；<=0 时用默认值。
	Limit int64
	// AllowedMimes 是允许的 mime 集合；空时用默认白名单。
	AllowedMimes []string
	// DeclaredMime 是客户端声明的 Content-Type，用于与 magic bytes 交叉校验（可为空）。
	DeclaredMime string
	// CreatedBy 是上传者用户 id（可空）。
	CreatedBy *uint64
}

// Save 写入一份媒体：限量读入临时文件、校验 magic bytes、按 sha256 去重、rename 落盘并写元数据。
// 返回的 Media 在去重命中时是已存在的那一行。
func (s *Store) Save(ctx context.Context, r io.Reader, opts SaveOptions) (*store.Media, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultMaxBytes
	}
	allowed := opts.AllowedMimes
	if len(allowed) == 0 {
		allowed = DefaultAllowedMimes()
	}

	tmp, err := os.CreateTemp(s.tempDir(), "upload-*")
	if err != nil {
		return nil, fmt.Errorf("media: create temp file: %w", err)
	}
	tmpName := tmp.Name()
	// 任何提前返回都要清掉临时文件，避免泄漏。
	committed := false
	defer func() {
		if !committed {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	hasher := sha256.New()
	// 多读 1 字节，用来发现“超过上限”，而不是把超大文件整个读完。
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

	// 读回文件头做 magic bytes 校验：只看字节，不信扩展名。
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("media: seek temp file: %w", err)
	}
	head := make([]byte, mediatype.HeadBytes)
	n, err := io.ReadFull(tmp, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("media: read temp head: %w", err)
	}
	detected, ext, ok := mediatype.Detect(head[:n])
	if !ok {
		return nil, ErrMagicMismatch
	}
	if !mediatype.Allowed(detected, allowed) {
		return nil, ErrMimeNotAllowed
	}
	// 客户端声明了类型时必须与魔数一致，否则是伪装文件。
	if declared := mediatype.Normalize(opts.DeclaredMime); declared != "" && declared != detected {
		return nil, ErrMagicMismatch
	}

	sum := hex.EncodeToString(hasher.Sum(nil))
	relPath := filepath.Join(sum[:2], sum+"."+ext)

	// 去重：数据库里已有同一 sha256 直接返回（文件也已存在）。
	if existing, err := s.bySha256(ctx, sum); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
	}

	finalPath, err := s.absPath(relPath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return nil, fmt.Errorf("media: create shard dir: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("media: close temp file: %w", err)
	}
	// 同目录 rename 原子替换；文件已存在说明并发下另一请求已落盘，直接复用。
	if err := os.Rename(tmpName, finalPath); err != nil {
		if _, statErr := os.Stat(finalPath); statErr != nil {
			return nil, fmt.Errorf("media: commit file: %w", err)
		}
	}
	committed = true

	row := &store.Media{
		Sha256:    sum,
		RelPath:   relPath,
		Mime:      detected,
		Bytes:     written,
		CreatedBy: opts.CreatedBy,
		CreatedAt: time.Now().UTC(),
	}
	if s.db != nil {
		if err := s.db.WithContext(ctx).Create(row).Error; err != nil {
			// 并发下可能撞唯一索引：回读已有行，视为去重成功。
			if existing, rerr := s.bySha256(ctx, sum); rerr == nil && existing != nil {
				return existing, nil
			}
			return nil, fmt.Errorf("media: record metadata: %w", err)
		}
	}
	return row, nil
}

// Open 按 id 取元数据并打开文件；不存在时返回 ErrNotFound。
func (s *Store) Open(ctx context.Context, id uint64) (*store.Media, *os.File, error) {
	if s.db == nil {
		return nil, nil, ErrNotFound
	}
	var m store.Media
	if err := s.db.WithContext(ctx).First(&m, "id = ?", id).Error; err != nil {
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

// bySha256 按 sha256 查元数据；不存在返回 (nil, nil)。
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
