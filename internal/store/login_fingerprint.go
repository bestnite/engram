package store

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 本文件是「新设备 / 新 IP 登录提醒」的持久化层。
//
// 只有记住「这个用户曾从哪些 (IP, 设备) 组合登录过」，才能判断本次登录是不是新的。
// 指纹是 IP 与 User-Agent 的哈希：存哈希而不是原文，既足够做去重，又避免在库里
// 长期堆积用户代理原文（隐私最小化）。

// LoginFingerprint 记录某用户见过的一个登录指纹。
type LoginFingerprint struct {
	ID uint64 `gorm:"primaryKey" json:"id"`
	// UserID + Fingerprint 唯一，构成「这个用户是否见过该设备/IP」的判定依据。
	UserID      uint64    `gorm:"not null;uniqueIndex:idx_login_fp_user_fp,priority:1" json:"user_id"`
	Fingerprint string    `gorm:"not null;uniqueIndex:idx_login_fp_user_fp,priority:2" json:"fingerprint"`
	FirstSeenAt time.Time `gorm:"not null" json:"first_seen_at"`
	LastSeenAt  time.Time `gorm:"not null" json:"last_seen_at"`
}

// TableName 固定表名，避免复数化规则漂移。
func (LoginFingerprint) TableName() string { return "login_fingerprints" }

// LoginFingerprintStore 封装 login_fingerprints 表的 GORM 访问。
type LoginFingerprintStore struct {
	db *gorm.DB
}

// NewLoginFingerprintStore 构造登录指纹存储。
func NewLoginFingerprintStore(db *gorm.DB) *LoginFingerprintStore {
	return &LoginFingerprintStore{db: db}
}

// Touch 记录一次登录指纹，返回 known=true 表示此前已经见过（不是新设备/IP）。
//
// 先查后插；插入用 OnConflict DoNothing，并发下重复插入时 RowsAffected=0，
// 同样按「已知」处理，避免同一秒内的两次登录各发一封提醒。
func (s *LoginFingerprintStore) Touch(ctx context.Context, userID uint64, fingerprint string, now time.Time) (bool, error) {
	now = now.UTC()
	var row LoginFingerprint
	err := s.db.WithContext(ctx).
		Where("user_id = ? AND fingerprint = ?", userID, fingerprint).
		First(&row).Error
	if err == nil {
		if uerr := s.db.WithContext(ctx).Model(&LoginFingerprint{}).
			Where("id = ?", row.ID).
			Update("last_seen_at", now).Error; uerr != nil {
			return true, fmt.Errorf("touch login fingerprint: %w", uerr)
		}
		return true, nil
	}
	if !IsNotFound(err) {
		return false, fmt.Errorf("load login fingerprint: %w", err)
	}
	insert := LoginFingerprint{
		UserID:      userID,
		Fingerprint: fingerprint,
		FirstSeenAt: now,
		LastSeenAt:  now,
	}
	res := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&insert)
	if res.Error != nil {
		return false, fmt.Errorf("record login fingerprint: %w", res.Error)
	}
	return res.RowsAffected == 0, nil
}

// DeleteStale 删除 last_seen_at 早于 before 的指纹行，返回删除行数。
//
// 这一张表的增长源是「每个用户见过的每个 (IP, 设备) 组合一行」，只增不减。按最后出现时间回收
// 意味着「很久没从这个设备登录过」就不再被当成已知设备——代价是它下次登录会被判为新设备并触发
// 提醒。保留期因此不能太短（调用方给 90 天）：宁可多留一会儿，也不要对正常用户误报新设备。
func (s *LoginFingerprintStore) DeleteStale(ctx context.Context, before time.Time) (int64, error) {
	res := s.db.WithContext(ctx).Where("last_seen_at <= ?", before).Delete(&LoginFingerprint{})
	if res.Error != nil {
		return 0, fmt.Errorf("delete stale login fingerprints: %w", res.Error)
	}
	return res.RowsAffected, nil
}
