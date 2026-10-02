package reminder

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"example.com/engram/internal/auth"
	"example.com/engram/internal/i18n"
	"example.com/engram/internal/mail"
	"example.com/engram/internal/store"
)

// TestReminderCarriesUnsubscribeHeaders 是 M1-22 在 C 类发信方一侧的证据：
// 复习提醒是可选类型，装配了退订令牌服务时，邮件带 RFC 8058 两个头，
// 且头里的令牌确实指名 review_reminder 这一个类型、归属正确的用户。
func TestReminderCarriesUnsubscribeHeaders(t *testing.T) {
	db := newTestDB(t)
	userID := seedUserWithDueCard(t, db, "dave", "dave@example.com", "en", "Asia/Shanghai", 4,
		time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	enableReminder(t, db, userID)

	enq := &fakeEnqueuer{configured: true}
	clock := shanghaiAt(t, 10, 0)
	translator, err := i18n.New()
	if err != nil {
		t.Fatalf("i18n.New: %v", err)
	}
	tokens, err := auth.NewActionTokenService(store.NewActionTokenStore(db))
	if err != nil {
		t.Fatalf("NewActionTokenService: %v", err)
	}
	r, err := New(Deps{
		DB: db, Outbox: enq, Translator: translator, Tokens: tokens,
		Now: func() time.Time { return clock }, BaseURL: "https://example.com",
	})
	if err != nil {
		t.Fatalf("reminder.New: %v", err)
	}
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	msg := enq.last()
	header := msg.Headers[mail.HeaderListUnsubscribe]
	if header == "" {
		t.Fatalf("optional C-class reminder carries no %s header; headers = %v", mail.HeaderListUnsubscribe, msg.Headers)
	}
	if got := msg.Headers[mail.HeaderListUnsubscribePost]; got != mail.ListUnsubscribePostValue {
		t.Errorf("%s = %q, want %q", mail.HeaderListUnsubscribePost, got, mail.ListUnsubscribePostValue)
	}

	raw := strings.Trim(strings.TrimSpace(header), "<>")
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse List-Unsubscribe URL %q: %v", raw, err)
	}
	tok, err := tokens.Consume(context.Background(), store.ActionTokenUnsubscribe, u.Query().Get("token"))
	if err != nil {
		t.Fatalf("consume unsubscribe token: %v", err)
	}
	if tok.UserID != userID {
		t.Errorf("token user = %d, want %d", tok.UserID, userID)
	}
	if tok.Payload != string(mail.TypeReviewReminder) {
		t.Errorf("token payload = %q, want %q (it must name exactly this type)", tok.Payload, mail.TypeReviewReminder)
	}
}

// TestReminderWithoutTokenServiceHasNoUnsubscribeHeader 是反面用例：
// 未装配令牌服务时提醒照常发出，但不带退订头（绝不出现半截或伪造的头）。
func TestReminderWithoutTokenServiceHasNoUnsubscribeHeader(t *testing.T) {
	db := newTestDB(t)
	userID := seedUserWithDueCard(t, db, "erin", "erin@example.com", "en", "Asia/Shanghai", 4,
		time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	enableReminder(t, db, userID)

	enq := &fakeEnqueuer{configured: true}
	clock := shanghaiAt(t, 10, 0)
	r := newReminder(t, db, enq, func() time.Time { return clock })
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if enq.count() != 1 {
		t.Fatalf("enqueued %d, want 1", enq.count())
	}
	if h := enq.last().Headers; len(h) != 0 {
		t.Errorf("reminder without a token service carries headers: %v", h)
	}
}
