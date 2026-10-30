package store

import (
	"fmt"
	"strings"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/mailstyle"
)

// convertMailTemplatesToHTML is migration 0014: it rewrites every stored mail template from Markdown
// to the inline-styled HTML the mail pipeline now renders.
//
// The conversion is the one in internal/mailstyle, the same styles the built-in bodies carry, so a
// stored custom template keeps the look it had before administrators wrote HTML. A blank body is left
// alone: the renderer already treats it as "no custom template". The row keys are read first and each
// body is written back by its (type, locale) key, because the table has no single-column id.
func convertMailTemplatesToHTML(tx *gorm.DB) error {
	if !tx.Migrator().HasTable("mail_templates") {
		return nil
	}
	var rows []MailTemplate
	if err := tx.Select("type", "locale", "body_md").Find(&rows).Error; err != nil {
		return fmt.Errorf("load mail templates for conversion: %w", err)
	}
	for _, row := range rows {
		if strings.TrimSpace(row.BodyMD) == "" {
			continue
		}
		converted, err := mailstyle.MarkdownToHTML(row.BodyMD)
		if err != nil {
			return fmt.Errorf("convert mail template %s/%s: %w", row.Type, row.Locale, err)
		}
		if err := tx.Model(&MailTemplate{}).
			Where("type = ? AND locale = ?", row.Type, row.Locale).
			UpdateColumn("body_md", converted).Error; err != nil {
			return fmt.Errorf("store converted mail template %s/%s: %w", row.Type, row.Locale, err)
		}
	}
	return nil
}
