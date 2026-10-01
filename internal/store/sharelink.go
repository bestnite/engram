package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// ErrShareLinkNotFound 表示分享链接不存在、已撤销或已过期。
//
// 三种情况故意合并成同一个哨兵：撤销/过期的链接必须与“从来没存在过”返回同样的结果
// （DESIGN.md §11 的安全要求、M5-3 验收“撤销或过期后的链接返回 404”），
// 否则调用方会泄漏“这个 token 曾经有效”这一事实。
var ErrShareLinkNotFound = errors.New("share link not found")

// ShareLinkDigest 返回明文 token 的 sha256 十六进制摘要。
//
// 库里只存摘要（DESIGN.md §11）：即使数据库泄漏，攻击者也拿不到可用的链接。
// 明文只在创建时返回一次，与 API Key 的处理方式一致（§7.2）。
func ShareLinkDigest(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// NewShareLinkToken 生成分享链接的明文 token：32 字节密码学随机值的 URL 安全编码。
// 用 RawURLEncoding 避免 '='、'+'、'/' 需要转义，token 可直接进 URL 路径。
func NewShareLinkToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate share link token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// ShareLinkInput 是创建分享链接的入参。
// PasswordHash 是已编码的 argon2id 哈希（由 auth 层生成），store 只负责落库，
// 不引入对 internal/auth 的依赖（auth 依赖 store，反向会成环）。
type ShareLinkInput struct {
	DeckID       uint64
	PasswordHash *string
	ExpiresAt    *time.Time
	CreatedBy    uint64
}

// ShareLinkStore 封装 share_links 表：创建（可选口令与过期）、列出、单个/批量撤销、按明文解析。
//
// 撤销写 revoked_at 而不是删除整行：分享链接保留历史便于审计“谁什么时候撤销了什么”。
// 解析路径统一过滤 revoked_at / expires_at，所以撤销与过期一样即时生效。
type ShareLinkStore struct {
	db *gorm.DB
}

// NewShareLinkStore 构造分享链接存储。
func NewShareLinkStore(db *gorm.DB) *ShareLinkStore { return &ShareLinkStore{db: db} }

// Create 生成明文 token、落库摘要并返回明文（明文只此一次机会，调用方必须立刻展示给用户）。
// 分享链接恒为 reader：它只用于免注册只读浏览（DESIGN.md §5），不接受调用方自定义角色。
func (s *ShareLinkStore) Create(ctx context.Context, in ShareLinkInput) (string, *ShareLink, error) {
	if in.DeckID == 0 || in.CreatedBy == 0 {
		return "", nil, errors.New("share link: deck_id and created_by are required")
	}
	plaintext, err := NewShareLinkToken()
	if err != nil {
		return "", nil, err
	}
	link := &ShareLink{
		Token:        ShareLinkDigest(plaintext),
		DeckID:       in.DeckID,
		Role:         RoleReader,
		PasswordHash: in.PasswordHash,
		ExpiresAt:    in.ExpiresAt,
		CreatedBy:    in.CreatedBy,
		CreatedAt:    time.Now().UTC(),
	}
	if err := s.db.WithContext(ctx).Create(link).Error; err != nil {
		return "", nil, fmt.Errorf("create share link: %w", err)
	}
	return plaintext, link, nil
}

// ListByDeck 按创建时间倒序列出卡组的全部分享链接（含已撤销/已过期，便于展示状态差异）。
func (s *ShareLinkStore) ListByDeck(ctx context.Context, deckID uint64) ([]ShareLink, error) {
	var links []ShareLink
	if err := s.db.WithContext(ctx).
		Where("deck_id = ?", deckID).Order("created_at DESC, token ASC").Find(&links).Error; err != nil {
		return nil, fmt.Errorf("list share links: %w", err)
	}
	return links, nil
}

// Revoke 撤销单个链接（按摘要定位）。已撤销或不存在时为幂等无操作。
// 用 token 摘要作为标识是因为表没有自增 id 列，且摘要本身不是密钥（不可反推明文）。
func (s *ShareLinkStore) Revoke(ctx context.Context, deckID uint64, digest string) error {
	if digest == "" {
		return errors.New("share link: token is required")
	}
	if err := s.db.WithContext(ctx).Model(&ShareLink{}).
		Where("deck_id = ? AND token = ? AND revoked_at IS NULL", deckID, digest).
		Update("revoked_at", time.Now().UTC()).Error; err != nil {
		return fmt.Errorf("revoke share link: %w", err)
	}
	return nil
}

// RevokeAll 一次撤销该卡组的全部有效链接（M5-3「可一次撤销全部」）。
// 返回实际撤销的条数，便于审计与测试断言。
func (s *ShareLinkStore) RevokeAll(ctx context.Context, deckID uint64) (int64, error) {
	now := time.Now().UTC()
	res := s.db.WithContext(ctx).Model(&ShareLink{}).
		Where("deck_id = ? AND revoked_at IS NULL", deckID).
		Update("revoked_at", now)
	if res.Error != nil {
		return 0, fmt.Errorf("revoke all share links: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// Resolve 用明文 token 找到有效链接；不存在、已撤销、已过期一律返回 ErrShareLinkNotFound。
//
// 撤销与过期在此统一收口：读取路径只有这一处判定，任何新增入口都不会漏掉过滤。
func (s *ShareLinkStore) Resolve(ctx context.Context, plaintext string, now time.Time) (*ShareLink, error) {
	if plaintext == "" {
		return nil, ErrShareLinkNotFound
	}
	var link ShareLink
	err := s.db.WithContext(ctx).Where("token = ?", ShareLinkDigest(plaintext)).First(&link).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrShareLinkNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("resolve share link: %w", err)
	}
	if link.RevokedAt != nil {
		return nil, ErrShareLinkNotFound
	}
	if link.ExpiresAt != nil && !now.Before(*link.ExpiresAt) {
		return nil, ErrShareLinkNotFound
	}
	return &link, nil
}

// Active 报告链接当前是否有效（未撤销且未过期）；共享管理页用它展示状态。
func (l ShareLink) Active(now time.Time) bool {
	if l.RevokedAt != nil {
		return false
	}
	if l.ExpiresAt != nil && !now.Before(*l.ExpiresAt) {
		return false
	}
	return true
}
