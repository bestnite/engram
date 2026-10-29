package reminder

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

// TestReminderCarriesUnsubscribeHeaders is the evidence on the class-C sender side:
// a review reminder is an optional type, so when the unsubscribe token service is wired in, the mail carries both RFC 8058 headers,
// and the token in them really does name the single type review_reminder and belongs to the right user.
func TestReminderCarriesUnsubscribeHeaders(t *testing.T) {
	db := newTestDB(t)
	userID := seedUserWithDueCard(t, db, "dave", "dave@example.com", "en", "Asia/Shanghai", 4,
		time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	enableReminder(t, db, userID)

	enq := &fakeEnqueuer{configured: true}
	clock := shanghaiAt(t, 20, 0)
	translator, err := i18n.New()
	if err != nil {
		t.Fatalf("i18n.New: %v", err)
	}
	tokens, err := auth.NewActionTokenService(store.NewActionTokenStore(db))
	if err != nil {
		t.Fatalf("NewActionTokenService: %v", err)
	}
	mailer := mail.NewMailer(mail.MailerDeps{
		DB: db, Outbox: enq, Tokens: tokens, BaseURL: "https://example.com", Locales: translator.SupportedCodes(),
	})
	r, err := New(Deps{DB: db, Mailer: mailer, Translator: translator, Now: func() time.Time { return clock }})
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

// TestReminderWithoutTokenServiceHasNoUnsubscribeHeader is the negative case:
// with no token service wired in, the reminder is still sent but carries no unsubscribe header (never a half-formed or forged header).
func TestReminderWithoutTokenServiceHasNoUnsubscribeHeader(t *testing.T) {
	db := newTestDB(t)
	userID := seedUserWithDueCard(t, db, "erin", "erin@example.com", "en", "Asia/Shanghai", 4,
		time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	enableReminder(t, db, userID)

	enq := &fakeEnqueuer{configured: true}
	clock := shanghaiAt(t, 20, 0)
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
