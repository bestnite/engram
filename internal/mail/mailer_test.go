package mail

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// recordingOutbox stands in for the outbox: it records what is enqueued and can report SMTP as unconfigured or fail.
type recordingOutbox struct {
	configured bool
	err        error
	msgs       []Message
}

func (o *recordingOutbox) Configured() bool { return o.configured }

func (o *recordingOutbox) Enqueue(_ context.Context, m Message) error {
	if o.err != nil {
		return o.err
	}
	o.msgs = append(o.msgs, m)
	return nil
}

// fixedTokens issues a fixed token so the unsubscribe link is predictable.
type fixedTokens struct{ err error }

func (f fixedTokens) Issue(context.Context, uint64, string, string, time.Duration) (string, error) {
	return "TOKEN", f.err
}

// echoLocalizer returns message ids as copy, which is enough to see which footer and name were chosen.
type echoLocalizer struct{}

func (echoLocalizer) T(id string) string                    { return id }
func (echoLocalizer) Tf(id string, _ map[string]any) string { return id }
func (echoLocalizer) Locale() string                        { return "en" }

// TestMailerSendGates pins the order and outcome of Send's gates: SMTP readiness, a known type, the recipient's
// preference (class A ignores it), then the enqueue itself.
func TestMailerSendGates(t *testing.T) {
	cases := []struct {
		name   string
		outbox *recordingOutbox
		mail   Mail
		optOut bool
		want   SendResult
	}{
		{name: "smtp not configured", outbox: &recordingOutbox{}, mail: Mail{Type: TypePasswordReset, UserID: 1}, want: SendNotConfigured},
		{name: "unknown type", outbox: &recordingOutbox{configured: true}, mail: Mail{Type: Type("nope"), UserID: 1}, want: SendFailed},
		{name: "class C defaults to off", outbox: &recordingOutbox{configured: true}, mail: Mail{Type: TypeReviewReminder, UserID: 1, Vars: Vars{"count": "1", "url": "/review"}}, want: SendOptedOut},
		{name: "class A ignores an opt-out", outbox: &recordingOutbox{configured: true}, optOut: true, mail: Mail{Type: TypePasswordReset, UserID: 1, Vars: Vars{"url": "/r", "expires": "x"}}, want: SendQueued},
		{name: "class B honours an opt-out", outbox: &recordingOutbox{configured: true}, optOut: true, mail: Mail{Type: TypeInvite, UserID: 1, Vars: Vars{"url": "/r"}}, want: SendOptedOut},
		{name: "outside recipient gets the type default", outbox: &recordingOutbox{configured: true}, mail: Mail{Type: TypeInvite, Vars: Vars{"url": "/r"}}, want: SendQueued},
		{name: "enqueue failure", outbox: &recordingOutbox{configured: true, err: errors.New("disk full")}, mail: Mail{Type: TypePasswordReset, UserID: 1, Vars: Vars{"url": "/r", "expires": "x"}}, want: SendFailed},
		{name: "enqueue reports smtp missing", outbox: &recordingOutbox{configured: true, err: ErrNotConfigured}, mail: Mail{Type: TypePasswordReset, UserID: 1, Vars: Vars{"url": "/r", "expires": "x"}}, want: SendNotConfigured},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := testDB(t)
			if tc.optOut {
				if err := store.NewEmailPrefStore(db).SetChoices(context.Background(), 1,
					map[string]bool{string(tc.mail.Type): false}, time.Now()); err != nil {
					t.Fatalf("SetChoices: %v", err)
				}
			}
			m := NewMailer(MailerDeps{DB: db, Outbox: tc.outbox})
			if got := m.Send(context.Background(), echoLocalizer{}, tc.mail); got != tc.want {
				t.Fatalf("Send() = %v, want %v", got, tc.want)
			}
			if queued := len(tc.outbox.msgs); (tc.want == SendQueued) != (queued == 1) {
				t.Errorf("outbox holds %d messages for result %v", queued, tc.want)
			}
		})
	}
}

// TestMailerComposeSiteAndUnsubscribe pins what Compose decides on the sender's behalf: the site name (the admin's
// setting over the language pack) and the unsubscribe link, which needs an optional type, an account, a token service
// and an absolute site root.
func TestMailerComposeSiteAndUnsubscribe(t *testing.T) {
	cases := []struct {
		name      string
		siteName  string
		baseURL   string
		tokens    TokenIssuer
		mail      Mail
		wantSite  string
		wantUnsub string
	}{
		{name: "language pack name and no link for class A", baseURL: "https://example.com", tokens: fixedTokens{},
			mail: Mail{Type: TypePasswordReset, UserID: 1, Vars: Vars{"url": "/r", "expires": "x"}}, wantSite: "app.name"},
		{name: "configured name and link for class C", siteName: "My Site", baseURL: "https://example.com", tokens: fixedTokens{},
			mail:     Mail{Type: TypeReviewReminder, UserID: 1, Vars: Vars{"count": "1", "url": "/review"}},
			wantSite: "My Site", wantUnsub: "https://example.com/unsubscribe?token=TOKEN"},
		{name: "per-mail base wins over none configured", tokens: fixedTokens{},
			mail:     Mail{Type: TypeInvite, UserID: 1, BaseURL: "http://localhost:8080/", Vars: Vars{"url": "/r"}},
			wantSite: "app.name", wantUnsub: "http://localhost:8080/unsubscribe?token=TOKEN"},
		{name: "no absolute root, no link", tokens: fixedTokens{},
			mail: Mail{Type: TypeReviewReminder, UserID: 1, Vars: Vars{"count": "1", "url": "/review"}}, wantSite: "app.name"},
		{name: "no account, no link", baseURL: "https://example.com", tokens: fixedTokens{},
			mail: Mail{Type: TypeInvite, Vars: Vars{"url": "/r"}}, wantSite: "app.name"},
		{name: "token failure, no link", baseURL: "https://example.com", tokens: fixedTokens{err: errors.New("db down")},
			mail: Mail{Type: TypeReviewReminder, UserID: 1, Vars: Vars{"count": "1", "url": "/review"}}, wantSite: "app.name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := testDB(t)
			if tc.siteName != "" {
				setSettings(t, db, map[string]string{store.SettingSiteName: tc.siteName})
			}
			m := NewMailer(MailerDeps{DB: db, Tokens: tc.tokens, BaseURL: tc.baseURL})
			msg := m.Compose(context.Background(), echoLocalizer{}, tc.mail)
			if !strings.Contains(msg.HTMLBody, tc.wantSite) {
				t.Errorf("HTML body does not carry the site name %q", tc.wantSite)
			}
			gotHeader := msg.Headers[HeaderListUnsubscribe]
			if tc.wantUnsub == "" {
				if gotHeader != "" || strings.Contains(msg.TextBody, "/unsubscribe?token=") {
					t.Errorf("unexpected unsubscribe link: header %q", gotHeader)
				}
				if !strings.Contains(msg.HTMLBody, "mail.footer.automated") {
					t.Errorf("a mail without an unsubscribe link must carry the automated footer")
				}
				return
			}
			if !strings.Contains(gotHeader, tc.wantUnsub) || !strings.Contains(msg.TextBody, tc.wantUnsub) {
				t.Errorf("unsubscribe link = header %q, want %q in the header and the text body", gotHeader, tc.wantUnsub)
			}
		})
	}
}

// TestMailerSiteDefaultLocale ignores a configured default locale the site does not support.
func TestMailerSiteDefaultLocale(t *testing.T) {
	cases := []struct{ configured, want string }{{"en", "en"}, {"fr", ""}, {"", ""}}
	for _, tc := range cases {
		db := testDB(t)
		if tc.configured != "" {
			setSettings(t, db, map[string]string{store.SettingSiteDefaultLocale: tc.configured})
		}
		m := NewMailer(MailerDeps{DB: db, Locales: []string{"zh-CN", "en"}})
		if got := m.SiteDefaultLocale(context.Background()); got != tc.want {
			t.Errorf("configured %q: SiteDefaultLocale() = %q, want %q", tc.configured, got, tc.want)
		}
	}
}
