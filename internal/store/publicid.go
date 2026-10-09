// 本文件实现对外可见的不可预测 id（public_id）。
//
// 为什么需要它：路由与 JSON 里的 id 原本就是自增主键，可以被枚举和推测。数字主键保留不动
// （它是全部外键与索引的基石），但不再对外暴露——模型上的 ID 标 json:"-"，新增的
// PublicID 标 json:"id"，于是直接序列化模型就得到正确的对外形态（AGENTS.md §2.4：
// 一个模型同时服务业务、GORM 与 JSON，不做 DTO 映射层）。
//
// 生成点集中在 GORM 的 BeforeCreate 钩子：页面、REST、MCP、导入、克隆、测试种子等所有
// 创建路径都经过 Create，因此在这里补值即可，不必在每个调用点重复。钩子只填空值，
// 显式指定的 public_id 不会被覆盖。
package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// NewPublicID 生成一个对外 id：UUIDv7（RFC 9562），36 字符的标准文本形式。
//
// 选 v7 而不是 v4：v7 由「毫秒时间戳 + 随机位」构成，仍然不可预测，但值随时间递增，
// 作为唯一索引插入时不会像纯随机的 v4 那样把 B-tree 随机打散。
func NewPublicID() string {
	id, err := uuid.NewV7()
	if err != nil {
		// 只有系统随机源不可用才会失败；退回到 v4 仍然比返回空串安全。
		fallback, ferr := uuid.NewRandom()
		if ferr != nil {
			panic("store: cannot generate a public id: " + ferr.Error())
		}
		return fallback.String()
	}
	return id.String()
}

// assignPublicID 是各模型 BeforeCreate 钩子的公共实现：空值即生成。
func assignPublicID(p *string) {
	if *p == "" {
		*p = NewPublicID()
	}
}

// 以下钩子覆盖全部对外可见实体；未对外暴露的表（sessions、outbox、日志台账等）不需要这一列。
func (m *User) BeforeCreate(*gorm.DB) error     { assignPublicID(&m.PublicID); return nil }
func (m *Identity) BeforeCreate(*gorm.DB) error { assignPublicID(&m.PublicID); return nil }
func (m *Invite) BeforeCreate(*gorm.DB) error   { assignPublicID(&m.PublicID); return nil }
func (m *Preset) BeforeCreate(*gorm.DB) error   { assignPublicID(&m.PublicID); return nil }
func (m *Deck) BeforeCreate(*gorm.DB) error     { assignPublicID(&m.PublicID); return nil }
func (m *Note) BeforeCreate(*gorm.DB) error     { assignPublicID(&m.PublicID); return nil }
func (m *Card) BeforeCreate(*gorm.DB) error     { assignPublicID(&m.PublicID); return nil }
func (m *Review) BeforeCreate(*gorm.DB) error   { assignPublicID(&m.PublicID); return nil }
func (m *APIKey) BeforeCreate(*gorm.DB) error   { assignPublicID(&m.PublicID); return nil }
func (m *Job) BeforeCreate(*gorm.DB) error      { assignPublicID(&m.PublicID); return nil }
func (m *AuditLog) BeforeCreate(*gorm.DB) error { assignPublicID(&m.PublicID); return nil }

// firstByPublicID 是各 Store ByPublicID 的公共实现。
// 空串直接当作未找到：迁移前的旧行 public_id 可能为空，绝不能让空串匹配到一行。
func firstByPublicID[T any](ctx context.Context, db *gorm.DB, publicID string) (*T, error) {
	if publicID == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var out T
	if err := db.WithContext(ctx).First(&out, "public_id = ?", publicID).Error; err != nil {
		return nil, err
	}
	return &out, nil
}

// ByPublicID 按对外 id 取卡组。归档不影响读取，调用方按 ArchivedAt 决定如何展示。
func (s *DeckStore) ByPublicID(ctx context.Context, publicID string) (*Deck, error) {
	return firstByPublicID[Deck](ctx, s.db, publicID)
}

// ByPublicID 按对外 id 取未软删除的 note；已删除的 note 视为不存在（用 Restore 恢复）。
func (s *NoteStore) ByPublicID(ctx context.Context, publicID string) (*Note, error) {
	return firstByPublicID[Note](ctx, s.db, publicID)
}

// ByPublicID 按对外 id 取预设。
func (s *PresetStore) ByPublicID(ctx context.Context, publicID string) (*Preset, error) {
	return firstByPublicID[Preset](ctx, s.db, publicID)
}

// ByPublicID 按对外 id 取用户；不存在时返回 gorm.ErrRecordNotFound。
func (s *UserStore) ByPublicID(ctx context.Context, publicID string) (*User, error) {
	return firstByPublicID[User](ctx, s.db, publicID)
}

// ByPublicID 按对外 id 取用户级凭据。
func (s *APIKeyStore) ByPublicID(ctx context.Context, publicID string) (*APIKey, error) {
	return firstByPublicID[APIKey](ctx, s.db, publicID)
}

// ByPublicID 按对外 id 取一张可见的 card；所属 note 已软删除时返回 gorm.ErrRecordNotFound。
// 可见性走 visibleCards，与 CardStore.ByID 同一口径。
func (s *CardStore) ByPublicID(ctx context.Context, publicID string) (*Card, error) {
	if publicID == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var c Card
	if err := s.visibleCards(ctx).Select("cards.*").Where("cards.public_id = ?", publicID).First(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

// ByPublicID 按对外 id 取邀请；未找到返回 ErrInviteNotFound。
func (s *InviteStore) ByPublicID(ctx context.Context, publicID string) (*Invite, error) {
	inv, err := firstByPublicID[Invite](ctx, s.db, publicID)
	if err != nil {
		if IsNotFound(err) {
			return nil, ErrInviteNotFound
		}
		return nil, fmt.Errorf("load invite: %w", err)
	}
	return inv, nil
}

// ByPublicID 按对外 id 取绑定；未找到返回 ErrIdentityNotFound。
func (s *IdentityStore) ByPublicID(ctx context.Context, publicID string) (*Identity, error) {
	ident, err := firstByPublicID[Identity](ctx, s.db, publicID)
	if err != nil {
		if IsNotFound(err) {
			return nil, ErrIdentityNotFound
		}
		return nil, fmt.Errorf("load identity: %w", err)
	}
	return ident, nil
}
