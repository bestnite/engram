package reminder

import (
	"context"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

// Reminders are sent by a periodic worker rather than a web request. This case asserts the admin template takes
// effect **on this path** too, and that the unsubscribe entry appears in both the body footer and the end of the plain-text part —
// if it were only in the RFC 8058 header, people reading plain text would not see it.

// newReminderWithTemplates is the same as newReminder but injects the template lookup and the token service.
func newReminderWithTemplates(t *testing.T, db *gorm.DB, enq mail.Enqueuer, now func() time.Time) *Reminder {
	t.Helper()
	translator, err := i18n.New()
	if err != nil {
		t.Fatalf("i18n.New: %v", err)
	}
	tokens, err := auth.NewActionTokenService(store.NewActionTokenStore(db))
	if err != nil {
		t.Fatalf("NewActionTokenService: %v", err)
	}
	mailer := mail.NewMailer(mail.MailerDeps{
		DB: db, Outbox: enq, Tokens: tokens, BaseURL: "http://localhost:3012", Locales: translator.SupportedCodes(),
		Templates: mail.StoreLookup(store.NewMailTemplateStore(db), nil),
	})
	r, err := New(Deps{DB: db, Mailer: mailer, Translator: translator, Now: now})
	if err != nil {
		t.Fatalf("reminder.New: %v", err)
	}
	return r
}

func TestReminderUsesAdminTemplate(t *testing.T) {
	db := newTestDB(t)
	enq := &fakeEnqueuer{configured: true}
	now := shanghaiAt(t, 19, 5)
	userID := seedUserWithDueCard(t, db, "tpluser", "tpl@example.com", "en", "Asia/Shanghai", 4, now.Add(-time.Hour))
	enableReminder(t, db, userID)

	if err := store.NewMailTemplateStore(db).Upsert(context.Background(), &store.MailTemplate{
		Type: string(mail.TypeReviewReminder), Locale: "en",
		Subject:   "{{count}} cards are due",
		BodyMD:    "**Time to review**\n\n[Open review]({{url}})\n\n[Unsubscribe]({{unsubscribe_url}})",
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save template: %v", err)
	}

	r := newReminderWithTemplates(t, db, enq, func() time.Time { return now })
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if enq.count() == 0 {
		t.Fatal("no reminder enqueued")
	}
	msg := enq.last()
	if !strings.HasSuffix(msg.Subject, "cards are due") {
		t.Errorf("subject = %q, want the template's subject with the count substituted", msg.Subject)
	}
	if !strings.Contains(msg.HTMLBody, "Time to review</strong>") {
		t.Errorf("html body = %q, want the template rendered", msg.HTMLBody)
	}
	if !strings.Contains(msg.HTMLBody, "/unsubscribe?token=") {
		t.Errorf("html body = %q, want the unsubscribe link in the footer", msg.HTMLBody)
	}
	if !strings.Contains(msg.TextBody, "/unsubscribe?token=") {
		t.Errorf("text body = %q, want the unsubscribe line in the plain-text part", msg.TextBody)
	}
}

// TestReminderWithoutTemplateRendersBuiltinHTML asserts that with no template the built-in body goes through the same
// renderer: the HTML part is non-empty and carries the review link, and the plain-text part still carries that link.
func TestReminderWithoutTemplateRendersBuiltinHTML(t *testing.T) {
	db := newTestDB(t)
	enq := &fakeEnqueuer{configured: true}
	now := shanghaiAt(t, 19, 5)
	userID := seedUserWithDueCard(t, db, "tpluser2", "tpl2@example.com", "en", "Asia/Shanghai", 4, now.Add(-time.Hour))
	enableReminder(t, db, userID)

	r := newReminderWithTemplates(t, db, enq, func() time.Time { return now })
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	msg := enq.last()
	if !strings.Contains(msg.HTMLBody, "<!DOCTYPE html>") {
		t.Errorf("html body = %q, want the fixed HTML shell", msg.HTMLBody)
	}
	if !strings.Contains(msg.HTMLBody, "Time to review") || !strings.Contains(msg.HTMLBody, "/review") {
		t.Errorf("html body = %q, want the built-in body with the review link", msg.HTMLBody)
	}
	if !strings.Contains(msg.TextBody, "/review") {
		t.Errorf("text body = %q, want the built-in body with the review link", msg.TextBody)
	}
}
