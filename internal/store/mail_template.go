package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrMailTemplateNotFound means there is no custom template for that (type, locale). Callers fall back to the built-in body based on it,
// so it is a **normal state** rather than an error; the sentinel exists to tell 'none' apart from 'the query failed'.
var ErrMailTemplateNotFound = errors.New("mail template not found")

// MailTemplateStore reads and writes admin-defined mail templates.
//
// Only four actions: fetch one, list all, overwrite whole, delete — saving is a **full replacement** with no partial update,
// because subject and body must come from the same edit, otherwise a 'new subject with old body' intermediate state appears.
type MailTemplateStore struct{ db *gorm.DB }

// NewMailTemplateStore constructs the template store.
func NewMailTemplateStore(db *gorm.DB) *MailTemplateStore { return &MailTemplateStore{db: db} }

// ByTypeLocale fetches one template; no customization yields ErrMailTemplateNotFound.
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

// List lists all custom templates. The upper bound is 'number of mail types × number of locales' (a few dozen rows), so one fetch is enough.
func (s *MailTemplateStore) List(ctx context.Context) ([]MailTemplate, error) {
	var rows []MailTemplate
	if err := s.db.WithContext(ctx).Order("type ASC, locale ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list mail templates: %w", err)
	}
	return rows, nil
}

// Upsert overwrites one template wholesale. It uses clause.OnConflict rather than 'read then write': with concurrent saves of the same key,
// read-then-write would hit the unique key, while OnConflict has consistent semantics on PostgreSQL and SQLite (AGENTS.md §2.3.4).
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

// Delete removes one custom template (falling back to the built-in body). Deleting a missing row is not a failure: the user's intent is
// 'back to the built-in', and that state already holds.
func (s *MailTemplateStore) Delete(ctx context.Context, mailType, locale string) error {
	err := s.db.WithContext(ctx).
		Where("type = ? AND locale = ?", mailType, locale).
		Delete(&MailTemplate{}).Error
	if err != nil {
		return fmt.Errorf("delete mail template: %w", err)
	}
	return nil
}
