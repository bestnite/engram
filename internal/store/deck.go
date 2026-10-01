package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// 卡组可见性取值（DESIGN.md §5）。
const (
	DeckVisibilityPrivate  = "private"
	DeckVisibilityUnlisted = "unlisted"
	DeckVisibilityPublic   = "public"
)

// 卡组级每日上限的文档化默认值（DESIGN.md §3.3）：新卡 20、复习 200，0 表示不限。
// 与 decks 表的列默认值保持一致；改这里必须同步改 models.go 的 default 标签。
const (
	DefaultNewPerDay     = 20
	DefaultReviewsPerDay = 200
)

var (
	// ErrNotOwner 表示调用者不是资源的所有者，因此无权修改。
	// 复用同一个哨兵值，让上层用 errors.Is 一致地翻译成 403。
	ErrNotOwner = errors.New("actor is not the owner")
	// ErrInvalidVisibility 表示可见性不在允许集合内。
	ErrInvalidVisibility = errors.New("invalid deck visibility")
	// ErrDeckNameRequired 表示卡组名为空。
	ErrDeckNameRequired = errors.New("deck name is required")
	// ErrDeckPresetRequired 表示卡组未指定调度预设。
	ErrDeckPresetRequired = errors.New("deck preset is required")
	// ErrInvalidDeckCap 表示每日上限为负；0 是合法值（不限）。
	ErrInvalidDeckCap = errors.New("deck daily cap must not be negative")
)

// DeckCaps 是卡组级的每日上限；NewPerDay 与 ReviewsPerDay 都为 0 时表示不限。
type DeckCaps struct {
	NewPerDay     int
	ReviewsPerDay int
}

// validDeckCaps 校验上限非负。
func validDeckCaps(c DeckCaps) error {
	if c.NewPerDay < 0 || c.ReviewsPerDay < 0 {
		return fmt.Errorf("%w: new_per_day=%d reviews_per_day=%d", ErrInvalidDeckCap, c.NewPerDay, c.ReviewsPerDay)
	}
	return nil
}

// validDeckVisibility 判断可见性取值是否合法。
func validDeckVisibility(v string) bool {
	switch v {
	case DeckVisibilityPrivate, DeckVisibilityUnlisted, DeckVisibilityPublic:
		return true
	default:
		return false
	}
}

// requireDeckOwner 检查 actor 是否为卡组 owner，不是则返回 ErrNotOwner。
//
// M2-1 阶段 decks 是唯一的归属来源：deck_grants 里还没有任何行，所以\"谁能改\"只由
// owner_user_id 决定。M5-1 引入授权后，这里会扩展为 owner/editor 的读写判定，
// reader 与陌生人仍在此被拒 —— 本轮的 owner 检查是它的子集，不会推翻。
func requireDeckOwner(d *Deck, actorUserID uint64) error {
	if d.OwnerUserID != actorUserID {
		return fmt.Errorf("%w: deck %d is owned by user %d", ErrNotOwner, d.ID, d.OwnerUserID)
	}
	return nil
}

// DeckStore 封装 decks 表的 GORM 访问，集中全部权限判断的入口。
type DeckStore struct {
	db *gorm.DB
}

// NewDeckStore 构造卡组存储。
func NewDeckStore(db *gorm.DB) *DeckStore { return &DeckStore{db: db} }

// validateDeckForWrite 校验可写字段；create 时额外要求 owner 与 preset 已给定。
func validateDeckForWrite(d *Deck, create bool) error {
	if d.Name == "" {
		return ErrDeckNameRequired
	}
	if d.Visibility == "" {
		// 字符串型默认值由 store 层在 Go 侧给出（models.go 包注释）；DESIGN.md §2.2 默认 private。
		d.Visibility = DeckVisibilityPrivate
	}
	if !validDeckVisibility(d.Visibility) {
		return fmt.Errorf("%w: %q", ErrInvalidVisibility, d.Visibility)
	}
	if create {
		if d.OwnerUserID == 0 {
			return errors.New("deck owner is required")
		}
		if d.PresetID == 0 {
			return ErrDeckPresetRequired
		}
	}
	return nil
}

// Create 写入一个新卡组；owner 归属由 d.OwnerUserID 显式给出。
func (s *DeckStore) Create(ctx context.Context, d *Deck) error {
	if err := validateDeckForWrite(d, true); err != nil {
		return err
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now().UTC()
	}
	if err := s.db.WithContext(ctx).Create(d).Error; err != nil {
		return fmt.Errorf("create deck: %w", err)
	}
	return nil
}

// ByID 按主键取卡组；归档不影响读取，调用方按 ArchivedAt 决定如何展示。
func (s *DeckStore) ByID(ctx context.Context, id uint64) (*Deck, error) {
	var d Deck
	if err := s.db.WithContext(ctx).First(&d, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &d, nil
}

// ListByOwner 列出某用户名下的卡组（含已归档），按创建时间倒序。
func (s *DeckStore) ListByOwner(ctx context.Context, ownerUserID uint64) ([]Deck, error) {
	var decks []Deck
	if err := s.db.WithContext(ctx).Where("owner_user_id = ?", ownerUserID).
		Order("created_at DESC, id DESC").Find(&decks).Error; err != nil {
		return nil, fmt.Errorf("list decks by owner: %w", err)
	}
	return decks, nil
}

// Update 修改卡组的设置字段；只有 owner 能改。
// 归属、创建时间、归档状态不在可改字段内，避免一次整行覆盖顺带抹掉它们。
func (s *DeckStore) Update(ctx context.Context, actorUserID uint64, d *Deck) error {
	if d.ID == 0 {
		return errors.New("update deck: id is required")
	}
	existing, err := s.ByID(ctx, d.ID)
	if err != nil {
		return err
	}
	if err := requireDeckOwner(existing, actorUserID); err != nil {
		return err
	}
	if err := validateDeckForWrite(d, false); err != nil {
		return err
	}
	updates := map[string]any{
		"name":        d.Name,
		"description": d.Description,
		"visibility":  d.Visibility,
		"preset_id":   d.PresetID,
	}
	if err := s.db.WithContext(ctx).Model(&Deck{}).Where("id = ?", d.ID).Updates(updates).Error; err != nil {
		return fmt.Errorf("update deck: %w", err)
	}
	return nil
}

// SetVisibility 只改可见性一列；只有 owner 能改。
func (s *DeckStore) SetVisibility(ctx context.Context, actorUserID, deckID uint64, visibility string) error {
	if !validDeckVisibility(visibility) {
		return fmt.Errorf("%w: %q", ErrInvalidVisibility, visibility)
	}
	return s.mutateOwned(ctx, actorUserID, deckID, map[string]any{"visibility": visibility})
}

// Archive 打上归档时间；重复归档直接返回 nil（幂等）。
func (s *DeckStore) Archive(ctx context.Context, actorUserID, deckID uint64, at time.Time) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return s.mutateOwned(ctx, actorUserID, deckID, map[string]any{"archived_at": at})
}

// Restore 清空归档时间；未归档时幂等返回 nil。
func (s *DeckStore) Restore(ctx context.Context, actorUserID, deckID uint64) error {
	return s.mutateOwned(ctx, actorUserID, deckID, map[string]any{"archived_at": nil})
}

// Caps 读取卡组的每日上限（DESIGN.md §3.3）；卡组不存在时返回底层错误。
func (s *DeckStore) Caps(ctx context.Context, deckID uint64) (DeckCaps, error) {
	var d Deck
	if err := s.db.WithContext(ctx).Select("id", "new_per_day", "reviews_per_day").
		First(&d, "id = ?", deckID).Error; err != nil {
		return DeckCaps{}, err
	}
	return DeckCaps{NewPerDay: d.NewPerDay, ReviewsPerDay: d.ReviewsPerDay}, nil
}

// SetCaps 写卡组级每日上限；只有 owner 能改。
// 用 map 更新而非模型整体 Save：0 在这里是合法值（不限），map 会显式写入 0，
// 不受 GORM 对带默认值列的零值省略行为影响。
func (s *DeckStore) SetCaps(ctx context.Context, actorUserID, deckID uint64, caps DeckCaps) error {
	if err := validDeckCaps(caps); err != nil {
		return err
	}
	return s.mutateOwned(ctx, actorUserID, deckID, map[string]any{
		"new_per_day":     caps.NewPerDay,
		"reviews_per_day": caps.ReviewsPerDay,
	})
}

// mutateOwned 是带 owner 校验的单列/多列更新通道；所有修改型方法都经它落地。
func (s *DeckStore) mutateOwned(ctx context.Context, actorUserID, deckID uint64, updates map[string]any) error {
	existing, err := s.ByID(ctx, deckID)
	if err != nil {
		return err
	}
	if err := requireDeckOwner(existing, actorUserID); err != nil {
		return err
	}
	if err := s.db.WithContext(ctx).Model(&Deck{}).Where("id = ?", deckID).Updates(updates).Error; err != nil {
		return fmt.Errorf("update deck %d: %w", deckID, err)
	}
	return nil
}
