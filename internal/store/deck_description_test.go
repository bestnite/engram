package store

import (
	"context"
	"testing"
	"time"
)

// formatFlagDeck is the decks table while a description was HTML or Markdown: the flag column migration 0018 drops.
type formatFlagDeck struct {
	ID                  uint64 `gorm:"primaryKey"`
	OwnerUserID         uint64 `gorm:"not null;index"`
	Name                string `gorm:"not null"`
	Description         string `gorm:"not null"`
	DescriptionMarkdown bool   `gorm:"column:description_markdown;not null;default:false"`
	PresetID            uint64 `gorm:"not null;index"`
	CreatedAt           time.Time
}

func (formatFlagDeck) TableName() string { return "decks" }

func TestDescriptionFromHTML(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"paragraphs become one line", "<p>one</p><p>two</p>", "one two"},
		{"line breaks become spaces", "a<br>b<br/>c", "a b c"},
		{"a newline in the source is whitespace", "a\nb", "a b"},
		{"references are decoded", "flags &amp; maps &lt;3", "flags & maps <3"},
		{"inline markup is removed", "<b>bold</b> and <span style=\"color:red\">red</span>", "bold and red"},
		{"speech and sound tags leave no text", "x[sound:a.mp3] y", "x y"},
		{"empty markup is empty", "<div><br></div>", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := descriptionFromHTML(tc.in); got != tc.want {
				t.Errorf("descriptionFromHTML(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestBuiltinPlainTextDeckDescriptionsMigration converts the stored descriptions and drops the flag column: an HTML
// description becomes the text a reader saw, a Markdown one keeps its source, and text without markup is untouched even
// when it holds a "<". A second Sync applies nothing.
func TestBuiltinPlainTextDeckDescriptionsMigration(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			if err := db.AutoMigrate(&formatFlagDeck{}); err != nil {
				t.Fatalf("create decks table with description_markdown: %v", err)
			}
			seeded := []formatFlagDeck{
				{OwnerUserID: 1, Name: "html", Description: "<p>Intro <b>text</b></p><p>more &amp; more</p>", PresetID: 1},
				{OwnerUserID: 1, Name: "markdown", Description: "# Title **bold** <b>kept</b>", DescriptionMarkdown: true, PresetID: 1},
				{OwnerUserID: 1, Name: "text", Description: "a < b and 3 > 2", PresetID: 1},
				{OwnerUserID: 1, Name: "empty", Description: "", PresetID: 1},
			}
			for i := range seeded {
				if err := db.Create(&seeded[i]).Error; err != nil {
					t.Fatalf("seed deck %q: %v", seeded[i].Name, err)
				}
			}

			if _, err := Sync(ctx, db, BuiltinMigrations); err != nil {
				t.Fatalf("Sync(BuiltinMigrations) error = %v", err)
			}
			if db.Migrator().HasColumn("decks", "description_markdown") {
				t.Error("decks still has a description_markdown column after the migration")
			}
			want := map[string]string{
				"html":     "Intro text more & more",
				"markdown": "# Title **bold** <b>kept</b>",
				"text":     "a < b and 3 > 2",
				"empty":    "",
			}
			var rows []struct{ Name, Description string }
			if err := db.Table("decks").Select("name, description").Scan(&rows).Error; err != nil {
				t.Fatalf("read decks after migration: %v", err)
			}
			if len(rows) != len(want) {
				t.Fatalf("decks after migration = %d rows, want %d", len(rows), len(want))
			}
			for _, row := range rows {
				if row.Description != want[row.Name] {
					t.Errorf("deck %q description = %q, want %q", row.Name, row.Description, want[row.Name])
				}
			}

			applied, err := Sync(ctx, db, BuiltinMigrations)
			if err != nil {
				t.Fatalf("second Sync(BuiltinMigrations) error = %v", err)
			}
			if applied != 0 {
				t.Errorf("second Sync() applied = %d, want 0", applied)
			}
		})
	}
}
