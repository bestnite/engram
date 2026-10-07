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

// 提醒是周期 worker 发出的，走的是另一条装配路径（不是 web 层的 renderMail）。这个用例
// 断言模板在**这条路径上**同样生效，并且退订入口同时出现在正文页脚与纯文本段末尾——
// 只在 RFC 8058 头里的话，读纯文本的人看不到它。

// newReminderWithTemplates 与 newReminder 相同，但注入模板查找与令牌服务。
func newReminderWithTemplates(t *testing.T, db *gorm.DB, enq Enqueuer, now func() time.Time) *Reminder {
	t.Helper()
	translator, err := i18n.New()
	if err != nil {
		t.Fatalf("i18n.New: %v", err)
	}
	tokens, err := auth.NewActionTokenService(store.NewActionTokenStore(db))
	if err != nil {
		t.Fatalf("NewActionTokenService: %v", err)
	}
	r, err := New(Deps{
		DB: db, Outbox: enq, Translator: translator, Now: now,
		BaseURL: "http://localhost:3012", Tokens: tokens,
		Templates:         mail.StoreLookup(store.NewMailTemplateStore(db), nil),
		SiteDefaultLocale: func() string { return "" },
	})
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
	if !strings.Contains(msg.HTMLBody, "<strong>Time to review</strong>") {
		t.Errorf("html body = %q, want the template rendered", msg.HTMLBody)
	}
	if !strings.Contains(msg.HTMLBody, "/unsubscribe?token=") {
		t.Errorf("html body = %q, want the unsubscribe link in the footer", msg.HTMLBody)
	}
	if !strings.Contains(msg.TextBody, "/unsubscribe?token=") {
		t.Errorf("text body = %q, want the unsubscribe line in the plain-text part", msg.TextBody)
	}
}

// TestReminderWithoutTemplateKeepsBuiltinBody 断言没配模板时与今天逐字一致：纯文本、无 HTML 段。
func TestReminderWithoutTemplateKeepsBuiltinBody(t *testing.T) {
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
	if msg.HTMLBody != "" {
		t.Errorf("html body = %q, want empty without a template", msg.HTMLBody)
	}
	if !strings.Contains(msg.TextBody, "/review") {
		t.Errorf("text body = %q, want the built-in body with the review link", msg.TextBody)
	}
}
