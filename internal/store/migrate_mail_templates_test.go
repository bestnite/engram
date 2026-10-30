package store

import (
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/mailstyle"
)

// TestConvertMailTemplatesToHTML is the migration case: a stored Markdown template is rewritten to
// the inline-styled HTML the new mail pipeline renders, template placeholders survive intact, and a
// blank body is left alone.
func TestConvertMailTemplatesToHTML(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(&MailTemplate{}); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			const markdown = "## Reset your password\n\nUse the button below.\n\n[Choose a new password]({{url}})\n\n> The link expires at {{expires}}."
			now := time.Now().UTC()
			rows := []MailTemplate{
				{Type: "password_reset", Locale: "en", Subject: "Reset", BodyMD: markdown, UpdatedAt: now},
				{Type: "password_reset", Locale: "zh-CN", Subject: "Reset", BodyMD: "", UpdatedAt: now},
			}
			for i := range rows {
				if err := db.Create(&rows[i]).Error; err != nil {
					t.Fatalf("seed mail template: %v", err)
				}
			}

			if err := convertMailTemplatesToHTML(db); err != nil {
				t.Fatalf("convertMailTemplatesToHTML() error = %v", err)
			}

			var converted MailTemplate
			if err := db.Where("type = ? AND locale = ?", "password_reset", "en").Take(&converted).Error; err != nil {
				t.Fatalf("read converted template: %v", err)
			}
			want, err := mailstyle.MarkdownToHTML(markdown)
			if err != nil {
				t.Fatalf("MailStyle.MarkdownToHTML() error = %v", err)
			}
			if converted.BodyMD != want {
				t.Errorf("converted body = %q, want %q", converted.BodyMD, want)
			}
			for _, wantHTML := range []string{`<h2 style="`, `href="{{url}}"`, "<blockquote"} {
				if !strings.Contains(converted.BodyMD, wantHTML) {
					t.Errorf("converted body does not contain %q:\n%s", wantHTML, converted.BodyMD)
				}
			}
			if strings.Contains(converted.BodyMD, "%7B%7B") {
				t.Errorf("converted body percent-encoded a placeholder:\n%s", converted.BodyMD)
			}
			if converted.Subject != "Reset" {
				t.Errorf("subject = %q, want it untouched", converted.Subject)
			}

			var blank MailTemplate
			if err := db.Where("type = ? AND locale = ?", "password_reset", "zh-CN").Take(&blank).Error; err != nil {
				t.Fatalf("read blank template: %v", err)
			}
			if blank.BodyMD != "" {
				t.Errorf("blank body = %q, want it left alone", blank.BodyMD)
			}
		})
	}
}
