package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrMailTemplateNotFound 表示该 (类型, 语言) 没有自定义模板。调用方据此回退内置正文，
// 所以它是**正常状态**而不是错误；用哨兵是为了让「没有」与「查库失败」可区分。
var ErrMailTemplateNotFound = errors.New("mail template not found")

// MailTemplateStore 读写管理员自定义的邮件模板。
//
// 只有取一份、列全部、整份覆盖、删掉四个动作：保存就是**整份替换**，不做部分更新——
// 主题与正文必须来自同一次编辑，否则会出现「新主题配旧正文」的中间态。
type MailTemplateStore struct{ db *gorm.DB }

// NewMailTemplateStore 构造模板存储。
func NewMailTemplateStore(db *gorm.DB) *MailTemplateStore { return &MailTemplateStore{db: db} }

// ByTypeLocale 取一份模板；没有自定义即 ErrMailTemplateNotFound。
func (s *MailTemplateStore) ByTypeLocale(ctx context.Context, mailType, locale string) (*MailTemplate, error) {
	var row MailTemplate
	err := s.db.WithContext(ctx).
		Where("type = ? AND locale = ?", mailType, locale).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrMailTemplateNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load mail template: %w", err)
	}
	return &row, nil
}

// List 列出全部自定义模板。数量上界是「邮件类型数 × 语言数」（几十行），一次取完即可。
func (s *MailTemplateStore) List(ctx context.Context) ([]MailTemplate, error) {
	var rows []MailTemplate
	if err := s.db.WithContext(ctx).Order("type ASC, locale ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list mail templates: %w", err)
	}
	return rows, nil
}

// Upsert 整份覆盖一份模板。用 clause.OnConflict 而不是「先查再写」：并发保存同一键时
// 先查再写会撞唯一键，而 OnConflict 在 PostgreSQL 与 SQLite 上语义一致（AGENTS.md §2.3.4）。
func (s *MailTemplateStore) Upsert(ctx context.Context, tpl *MailTemplate) error {
	if tpl.UpdatedAt.IsZero() {
		tpl.UpdatedAt = time.Now().UTC()
	}
	err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "type"}, {Name: "locale"}},
		DoUpdates: clause.AssignmentColumns([]string{"subject", "body_md", "updated_by", "updated_at"}),
	}).Create(tpl).Error
	if err != nil {
		return fmt.Errorf("save mail template: %w", err)
	}
	return nil
}

// Delete 删掉一份自定义模板（回到内置正文）。删不存在的行不算失败：用户的意图是
// 「回到内置」，那个状态已经成立。
func (s *MailTemplateStore) Delete(ctx context.Context, mailType, locale string) error {
	err := s.db.WithContext(ctx).
		Where("type = ? AND locale = ?", mailType, locale).
		Delete(&MailTemplate{}).Error
	if err != nil {
		return fmt.Errorf("delete mail template: %w", err)
	}
	return nil
}
