package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 每个用户对每个卡组的学习设置（调度预设与每日上限）。
//
// 进度早已按用户分开（card_states 的主键是 (card_id, user_id)）；学习设置同理必须按用户分开：
// 共享卡组的成员按自己的记忆参数复习、按自己的节奏引入新卡，不受属主的预设与上限支配。
//
// 存放位置按身份分两处，读写都只经过本文件的 StudySettings / SetStudySettings：
//   - 属主：decks 表上的 preset_id / new_per_day / reviews_per_day 三列（它们一直就是属主的设置，
//     保持原样避免迁移数据与改动大量调用点）；
//   - 其他成员：deck_member_settings 表，一行对应一个 (卡组, 用户)。授权写入时建行，
//     初始值是成员自己的默认预设与默认上限；撤销授权、删除卡组或删除用户时随之删除。

// DeckMemberSetting 是一个非属主成员对一个共享卡组的学习设置。
// 两个上限列不带数据库默认值：0 是合法值（不限），带默认值的整型零值会被 GORM 省略后由默认值覆盖。
type DeckMemberSetting struct {
	DeckID        uint64    `gorm:"primaryKey" json:"-"`
	UserID        uint64    `gorm:"primaryKey;index" json:"-"`
	PresetID      uint64    `gorm:"not null;index" json:"-"`
	NewPerDay     int       `gorm:"not null" json:"new_per_day"`
	ReviewsPerDay int       `gorm:"not null" json:"reviews_per_day"`
	UpdatedAt     time.Time `gorm:"not null" json:"updated_at"`
}

func (DeckMemberSetting) TableName() string { return "deck_member_settings" }

// StudySettings 是某用户复习某卡组时生效的设置。
type StudySettings struct {
	PresetID uint64
	Caps     DeckCaps
}

// StudySettingsFor 返回 userID 在每个给定卡组上的学习设置（键是卡组主键）。
//
// 属主读卡组列；成员读 deck_member_settings。成员行缺失（例如本表上线前的授权、由迁移补齐之前）
// 时回落到成员自己的默认预设与默认上限——不回落到属主的设置，否则就又把属主的参数用在了别人身上。
func StudySettingsFor(ctx context.Context, db *gorm.DB, userID uint64, decks []Deck) (map[uint64]StudySettings, error) {
	out := make(map[uint64]StudySettings, len(decks))
	var memberDecks []uint64
	for _, d := range decks {
		if d.OwnerUserID == userID {
			out[d.ID] = StudySettings{PresetID: d.PresetID, Caps: DeckCaps{NewPerDay: d.NewPerDay, ReviewsPerDay: d.ReviewsPerDay}}
			continue
		}
		memberDecks = append(memberDecks, d.ID)
	}
	if len(memberDecks) == 0 {
		return out, nil
	}
	var rows []DeckMemberSetting
	if err := db.WithContext(ctx).Where("user_id = ? AND deck_id IN ?", userID, memberDecks).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("store: load member study settings: %w", err)
	}
	for _, r := range rows {
		out[r.DeckID] = StudySettings{PresetID: r.PresetID, Caps: DeckCaps{NewPerDay: r.NewPerDay, ReviewsPerDay: r.ReviewsPerDay}}
	}
	if len(rows) == len(memberDecks) {
		return out, nil
	}
	fallback, err := defaultStudySettings(ctx, db, userID)
	if err != nil {
		return nil, err
	}
	for _, id := range memberDecks {
		if _, ok := out[id]; !ok {
			out[id] = fallback
		}
	}
	return out, nil
}

// StudySettings 返回 userID 在单个卡组上的学习设置（规则见 StudySettingsFor）。
func (s *DeckStore) StudySettings(ctx context.Context, userID uint64, deck *Deck) (StudySettings, error) {
	all, err := StudySettingsFor(ctx, s.db, userID, []Deck{*deck})
	if err != nil {
		return StudySettings{}, err
	}
	return all[deck.ID], nil
}

// defaultStudySettings 是成员的初始设置：自己的默认预设（没有则建）与卡组的默认上限。
func defaultStudySettings(ctx context.Context, db *gorm.DB, userID uint64) (StudySettings, error) {
	presets, err := EnsureDefaultPreset(ctx, db, userID)
	if err != nil {
		return StudySettings{}, fmt.Errorf("store: ensure default preset for user %d: %w", userID, err)
	}
	p := DefaultPreset(presets)
	if p == nil {
		return StudySettings{}, ErrNoDefaultPreset
	}
	return StudySettings{PresetID: p.ID, Caps: DeckCaps{NewPerDay: DefaultNewPerDay, ReviewsPerDay: DefaultReviewsPerDay}}, nil
}

// ensureMemberSettings 在成员还没有设置行时按初始值建一行；已有则不动（改角色不重置设置）。
func ensureMemberSettings(ctx context.Context, db *gorm.DB, deckID, userID uint64) error {
	initial, err := defaultStudySettings(ctx, db, userID)
	if err != nil {
		return err
	}
	row := DeckMemberSetting{
		DeckID: deckID, UserID: userID, PresetID: initial.PresetID,
		NewPerDay: initial.Caps.NewPerDay, ReviewsPerDay: initial.Caps.ReviewsPerDay, UpdatedAt: time.Now().UTC(),
	}
	if err := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return fmt.Errorf("store: create member study settings: %w", err)
	}
	return nil
}

// SetStudySettings 改 userID 在卡组上的学习设置；presetID 与 caps 为 nil 的项保持不变。
//
// 预设必须属于 userID 本人（否则返回 ErrDeckPresetInvalid）：挂上别人的预设，就能借「卡组读得到」
// 读出他人的参数。调用方负责确认 userID 至少是该卡组的 reader。
func (s *DeckStore) SetStudySettings(ctx context.Context, userID uint64, deck *Deck, presetID *uint64, caps *DeckCaps) error {
	updates := map[string]any{}
	if presetID != nil {
		var count int64
		if err := s.db.WithContext(ctx).Model(&Preset{}).
			Where("id = ? AND owner_user_id = ?", *presetID, userID).Count(&count).Error; err != nil {
			return fmt.Errorf("check preset ownership: %w", err)
		}
		if count == 0 {
			return ErrDeckPresetInvalid
		}
		updates["preset_id"] = *presetID
	}
	if caps != nil {
		if err := validDeckCaps(*caps); err != nil {
			return err
		}
		updates["new_per_day"] = caps.NewPerDay
		updates["reviews_per_day"] = caps.ReviewsPerDay
	}
	if len(updates) == 0 {
		return nil
	}
	if deck.OwnerUserID == userID {
		if err := s.db.WithContext(ctx).Model(&Deck{}).Where("id = ?", deck.ID).Updates(updates).Error; err != nil {
			return fmt.Errorf("update deck %d settings: %w", deck.ID, err)
		}
		return nil
	}
	if err := ensureMemberSettings(ctx, s.db, deck.ID, userID); err != nil {
		return err
	}
	updates["updated_at"] = time.Now().UTC()
	res := s.db.WithContext(ctx).Model(&DeckMemberSetting{}).
		Where("deck_id = ? AND user_id = ?", deck.ID, userID).Updates(updates)
	if res.Error != nil {
		return fmt.Errorf("update member settings of deck %d: %w", deck.ID, res.Error)
	}
	if res.RowsAffected == 0 {
		return errors.New("store: member settings row disappeared during update")
	}
	return nil
}
