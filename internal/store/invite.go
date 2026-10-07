package store

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// 邀请相关的稳定哨兵值；上层据此统一翻译成 403 与本地化提示。
var (
	// ErrInviteNotFound 表示 token 不存在（含已被撤销：撤销实现为删除整行）。
	ErrInviteNotFound = errors.New("invite not found")
	// ErrInviteUsed 表示 token 已被使用，一次性语义不允许复用。
	ErrInviteUsed = errors.New("invite already used")
	// ErrInviteExpired 表示 token 已过 expires_at。
	ErrInviteExpired = errors.New("invite expired")
	// ErrInviteEmailMismatch 表示注册邮箱与邀请限定的邮箱不一致。
	ErrInviteEmailMismatch = errors.New("invite is restricted to another email")
	// ErrInviteTokenRequired 表示生成邀请时 token 为空且随机生成失败。
	ErrInviteTokenRequired = errors.New("invite token is required")
)

// InviteStore 封装 invites 表；token 一次性、可限定邮箱、可设过期（DESIGN.md §2.2、§4.2）。
//
// 关于“撤销”：DESIGN.md §2.2 的 invites 表没有 revoked_at 列，因此撤销实现为删除整行。
// 被撤销的 token 在 ByToken/MarkUsed 里与“不存在”同路被拒。若将来需要保留撤销审计，
// 应新增 revoked_at 列（需要独写 models.go 的 owner 排期），本实现不擅自加列。
type InviteStore struct {
	db *gorm.DB
}

// NewInviteStore 构造邀请存储。
func NewInviteStore(db *gorm.DB) *InviteStore { return &InviteStore{db: db} }

// newInviteToken 生成 32 字节随机 token 的 URL 安全编码（与分享链接同规格，DESIGN.md §4.6）。
func newInviteToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate invite token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// Create 写入一条邀请。token 为空时自动生成；Email 为 nil 表示不限定邮箱；
// ExpiresAt 为 nil 表示不过期。CreatedAt 为空时补当前 UTC 时间。
func (s *InviteStore) Create(ctx context.Context, inv *Invite) error {
	if inv.Token == "" {
		token, err := newInviteToken()
		if err != nil {
			return err
		}
		inv.Token = token
	}
	if inv.Role == "" {
		// 字符串默认值由 store 层在 Go 侧给出（models.go 包注释）。
		inv.Role = RoleUser
	}
	if inv.CreatedAt.IsZero() {
		inv.CreatedAt = time.Now().UTC()
	}
	if err := s.db.WithContext(ctx).Create(inv).Error; err != nil {
		return fmt.Errorf("create invite: %w", err)
	}
	return nil
}

// ByToken 按 token 取邀请；不存在时返回 ErrInviteNotFound。
func (s *InviteStore) ByToken(ctx context.Context, token string) (*Invite, error) {
	var inv Invite
	if err := s.db.WithContext(ctx).First(&inv, "token = ?", token).Error; err != nil {
		if IsNotFound(err) {
			return nil, ErrInviteNotFound
		}
		return nil, fmt.Errorf("load invite: %w", err)
	}
	return &inv, nil
}

// List 返回全部邀请（含已用与已过期），按创建时间倒序；供 M6 管理面板展示使用情况。
func (s *InviteStore) List(ctx context.Context) ([]Invite, error) {
	var invites []Invite
	if err := s.db.WithContext(ctx).Order("created_at DESC, id DESC").Find(&invites).Error; err != nil {
		return nil, fmt.Errorf("list invites: %w", err)
	}
	return invites, nil
}

// Validate 校验 token 此刻是否可用，并核对注册邮箱是否符合邀请限定。
// 已用 / 已过期 / 不存在（含已撤销）/ 邮箱不匹配分别返回对应哨兵值。
func (s *InviteStore) Validate(ctx context.Context, token, email string, now time.Time) (*Invite, error) {
	inv, err := s.ByToken(ctx, token)
	if err != nil {
		return nil, err
	}
	return inv, UsableInvite(inv, email, now)
}

// UsableInvite 是不依赖数据库的可用性判定，便于表驱动测试直接覆盖。
func UsableInvite(inv *Invite, email string, now time.Time) error {
	if inv.UsedAt != nil {
		return ErrInviteUsed
	}
	if inv.ExpiresAt != nil && !inv.ExpiresAt.After(now) {
		return ErrInviteExpired
	}
	if inv.Email != nil && strings.ToLower(strings.TrimSpace(*inv.Email)) != strings.ToLower(strings.TrimSpace(email)) {
		return ErrInviteEmailMismatch
	}
	return nil
}

// Accept 在单个事务里消费邀请并执行建号回调（B-12）。
//
// 旧的“先 MarkUsed 占用、建号失败再 Release”会在两步之间留下可观测的中间态（先显示已使用、
// 又变回可用）。这里把占用与建号放进同一个事务：要么都提交，要么都回滚，外界看不到半完成状态。
// 占用仍是条件更新（used_at IS NULL），因此并发下同一个 token 只有一个事务能占用成功 —— 一码一用。
//
// create 在建号失败时返回错误即触发整体回滚，token 保持可用；成功时 used_by 与 used_at 一并落库。
func (s *InviteStore) Accept(ctx context.Context, token string, now time.Time, create func(tx *gorm.DB) (*User, error)) (*User, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var u *User
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&Invite{}).
			Where("token = ? AND used_at IS NULL", token).
			Update("used_at", now)
		if res.Error != nil {
			return fmt.Errorf("claim invite: %w", res.Error)
		}
		if res.RowsAffected != 1 {
			// 并发下已被他人占用，或 token 本就不存在/已用。
			return ErrInviteUsed
		}
		created, cerr := create(tx)
		if cerr != nil {
			return cerr
		}
		u = created
		if err := tx.Model(&Invite{}).Where("token = ?", token).
			Update("used_by", u.ID).Error; err != nil {
			return fmt.Errorf("set invite used_by: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return u, nil
}

// MarkUsed 原子地把 token 标记为已用：只有 used_at 仍为 NULL 的行会被更新。
// 返回 claimed=false 表示并发下已被他人抢先使用，调用方必须放弃建号 —— 这是
// “一次邀请只创建一个用户”的强制点。
func (s *InviteStore) MarkUsed(ctx context.Context, token string, at time.Time) (bool, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	res := s.db.WithContext(ctx).Model(&Invite{}).
		Where("token = ? AND used_at IS NULL", token).
		Update("used_at", at)
	if res.Error != nil {
		return false, fmt.Errorf("mark invite used: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// SetUsedBy 记录接受邀请的用户；建号成功后调用，best-effort。
func (s *InviteStore) SetUsedBy(ctx context.Context, token string, userID uint64) error {
	if err := s.db.WithContext(ctx).Model(&Invite{}).
		Where("token = ?", token).
		Update("used_by", userID).Error; err != nil {
		return fmt.Errorf("set invite used_by: %w", err)
	}
	return nil
}

// Release 回滚一次 MarkUsed：建号失败时把 used_at 清空，让邀请仍可用。
func (s *InviteStore) Release(ctx context.Context, token string) error {
	if err := s.db.WithContext(ctx).Model(&Invite{}).
		Where("token = ?", token).
		Update("used_at", nil).Error; err != nil {
		return fmt.Errorf("release invite: %w", err)
	}
	return nil
}

// Revoke 撤销一条邀请：删除整行。行不存在时返回 ErrInviteNotFound。
func (s *InviteStore) Revoke(ctx context.Context, id uint64) error {
	res := s.db.WithContext(ctx).Delete(&Invite{}, "id = ?", id)
	if res.Error != nil {
		return fmt.Errorf("revoke invite: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrInviteNotFound
	}
	return nil
}

// DeleteExpired 删除已过期的邀请行，返回删除行数。
//
// **只删 expires_at 非空且已到点的行**：expires_at 可空，NULL 表示「这张邀请不过期」，
// 不能被当成过期清掉。同理，已使用（used_at 非空）但未到期的邀请也保留——它是「谁用了这张
// 邀请」的凭据，等它自然到点再回收。
func (s *InviteStore) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	res := s.db.WithContext(ctx).
		Where("expires_at IS NOT NULL AND expires_at <= ?", before).
		Delete(&Invite{})
	if res.Error != nil {
		return 0, fmt.Errorf("delete expired invites: %w", res.Error)
	}
	return res.RowsAffected, nil
}
