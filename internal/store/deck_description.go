package store

import (
	"fmt"
	"regexp"
	"strings"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/notetype"
)

// A deck description is plain text. Two sources still hand over HTML: an Anki package, whose deck descriptions are
// HTML by definition, and the decks stored while descriptions were HTML or Markdown, which migration 0018 converts.

// descriptionMarkup matches what marks a stored description as HTML: a tag or a character reference. Migration 0018
// converts only such descriptions, so text that merely contains "<" followed by a space or a digit ("a < b") is kept.
var descriptionMarkup = regexp.MustCompile(`(?i)<[a-z!/]|&(#[0-9]+|#x[0-9a-f]+|[a-z]+);`)

// descriptionLineBreak matches the markup that ends a line of text: a line break and the end of a block element.
var descriptionLineBreak = regexp.MustCompile(`(?i)<br\s*/?>|</(p|div|li|h[1-6]|blockquote|pre|tr)\s*>`)

// descriptionFromHTML turns an HTML description into the text a reader saw, on one line: a description may hold no
// control character, a newline included, so every line break and block end becomes a space, as a newline in HTML
// source already is. Tags are removed and references decoded.
func descriptionFromHTML(html string) string {
	text := notetype.PlainText(descriptionLineBreak.ReplaceAllString(html, " "))
	return strings.Join(strings.Fields(text), " ")
}

// plainTextDeckDescriptions is migration 0018: deck descriptions become plain text and the description_markdown
// column that chose between HTML and Markdown is dropped.
//
// A Markdown description keeps its source: Markdown is written to be read as text. An HTML description becomes the
// text a reader saw, by descriptionFromHTML. Soft-deleted decks are converted too, because they can be restored.
func plainTextDeckDescriptions(tx *gorm.DB) error {
	m := tx.Migrator()
	if !m.HasTable("decks") {
		return nil
	}
	hasFlag := m.HasColumn("decks", "description_markdown")
	columns := "id, description"
	if hasFlag {
		columns += ", description_markdown"
	}
	var rows []struct {
		ID                  uint64
		Description         string
		DescriptionMarkdown bool `gorm:"column:description_markdown"`
	}
	if err := tx.Table("decks").Select(columns).Order("id ASC").Scan(&rows).Error; err != nil {
		return fmt.Errorf("plain text deck descriptions: read decks: %w", err)
	}
	for _, row := range rows {
		// A Markdown description, and an HTML one without markup, is already the text to keep.
		if row.DescriptionMarkdown || !descriptionMarkup.MatchString(row.Description) {
			continue
		}
		text := descriptionFromHTML(row.Description)
		if text == row.Description {
			continue
		}
		if err := tx.Table("decks").Where("id = ?", row.ID).UpdateColumn("description", text).Error; err != nil {
			return fmt.Errorf("plain text deck descriptions: deck %d: %w", row.ID, err)
		}
	}
	if !hasFlag {
		return nil
	}
	var stmt string
	switch tx.Dialector.Name() {
	case "sqlite":
		stmt = "ALTER TABLE decks DROP COLUMN description_markdown"
	case "postgres":
		stmt = "ALTER TABLE decks DROP COLUMN IF EXISTS description_markdown"
	default:
		return fmt.Errorf("plain text deck descriptions: unsupported dialect %q", tx.Dialector.Name())
	}
	return tx.Exec(stmt).Error
}
