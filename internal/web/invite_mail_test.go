package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is HTTP-level acceptance: an invite code can be mailed by an admin straight to the invitee's email (type B).
//
// Covers four acceptance lines:
//  1. an invite created for an email can be delivered and accepted;
//  2. revoked / expired invite links are refused;
//  3. the original manual copy-the-link path still works;
//  4. with SMTP unconfigured creation still succeeds and echo mail_notice faithfully (never silently).
//
// Admin-side invite creation/revocation and signup all go through the SPA's same-origin JSON endpoints (the SSR form endpoints have been deleted).
// Delivery uses an in-process stand-in (recordingMailSender) and needs neither the internet nor a real SMTP server.

// recordingMailSender is a stand-in for mail.Sender: it only records delivered mail and never touches the network.
type recordingMailSender struct {
	mu   sync.Mutex
	sent []mail.Message
}

func (r *recordingMailSender) Send(ctx context.Context, cfg mail.SMTPConfig, m mail.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, m)
	return nil
}

func (r *recordingMailSender) Test(ctx context.Context, cfg mail.SMTPConfig) error { return nil }

func (r *recordingMailSender) messages() []mail.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]mail.Message(nil), r.sent...)
}

// configureSMTP writes working SMTP settings; host and from both present counts as configured.
func configureSMTP(t *testing.T, db *gorm.DB) {
	t.Helper()
	now := time.Now().UTC()
	for k, v := range map[string]string{
		mail.SettingKeySMTPHost: "127.0.0.1",
		mail.SettingKeySMTPPort: "2525",
		mail.SettingKeySMTPFrom: "no-reply@example.com",
	} {
		if err := store.PutSetting(context.Background(), db, k, v, nil, now); err != nil {
			t.Fatalf("PutSetting(%s): %v", k, err)
		}
	}
}

// startInviteMail builds and starts an outbox with SMTP configured, attaches it to the test server, and returns the stand-in.
func startInviteMail(t *testing.T, srv *Server, db *gorm.DB) *recordingMailSender {
	t.Helper()
	configureSMTP(t, db)
	sender := &recordingMailSender{}
	ob := mail.NewOutbox(mail.Deps{
		DB: db, Sender: sender,
		Retry:        mail.RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond},
		PollInterval: 5 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	ob.Start(ctx)
	t.Cleanup(func() {
		ob.Stop()
		cancel()
	})
	srv.setMailOutbox(ob, srv.baseURL)
	return sender
}

// waitForMail polls until the stand-in has received at least n messages (the worker delivers asynchronously).
func waitForMail(t *testing.T, sender *recordingMailSender, n int) []mail.Message {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if msgs := sender.messages(); len(msgs) >= n {
			return msgs
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d delivered messages, got %d", n, len(sender.messages()))
	return nil
}

// inviteCreateResponse is the JSON reply for creating an invite: the new invite plus the mail result code.
type inviteCreateResponse struct {
	Invite struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	} `json:"invite"`
	MailNotice string `json:"mail_notice"`
}

// createInviteJSON creates an invite through the admin JSON endpoint and returns the response plus the decoded reply.
func createInviteJSON(t *testing.T, srv *Server, cookies []*http.Cookie, csrf string, body map[string]any) (*httptest.ResponseRecorder, inviteCreateResponse) {
	t.Helper()
	rec := postJSONWithSession(srv, "/api/v1/admin/invites", body, cookies, csrf)
	var resp inviteCreateResponse
	if rec.Code == http.StatusCreated {
		decodeJSON(t, rec, &resp)
	}
	return rec, resp
}

// registerWithInviteJSON signs up through the SPA's JSON registration with an invite token (pre-session double-submit CSRF).
func registerWithInviteJSON(t *testing.T, srv *Server, username, email, invite string) *httptest.ResponseRecorder {
	t.Helper()
	cookie, headers := preSessionPair(t, srv, "/register")
	return postJSON(srv, "/api/v1/auth/register", map[string]string{
		"username": username, "email": email,
		"password": "Sup3rSecret!", "invite": invite,
	}, []*http.Cookie{cookie}, headers)
}

// TestInviteEmailDeliveredAndAccepted is the core acceptance:
// an invite created for an email is delivered (the mail carries a usable accept link), and that link can be accepted.
func TestInviteEmailDeliveredAndAccepted(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	srv.invites = store.NewInviteStore(db)
	sender := startInviteMail(t, srv, db)

	created, resp := createInviteJSON(t, srv, cookies, csrf, map[string]any{
		"role": "user", "expires_days": 7,
		"email": "invitee@example.com", "send_email": true,
	})
	if created.Code != http.StatusCreated || resp.MailNotice != noticeInviteMailQueued {
		t.Fatalf("POST /api/v1/admin/invites = %d mail_notice %q, want 201 %q",
			created.Code, resp.MailNotice, noticeInviteMailQueued)
	}

	invites, err := srv.invites.List(context.Background())
	if err != nil || len(invites) != 1 {
		t.Fatalf("List() = %d invites err %v, want 1", len(invites), err)
	}
	token := invites[0].Token

	msgs := waitForMail(t, sender, 1)
	msg := msgs[0]
	if msg.To != "invitee@example.com" {
		t.Errorf("delivered To = %q, want invitee@example.com", msg.To)
	}
	if msg.Type != string(mail.TypeInvite) {
		t.Errorf("delivered Type = %q, want %q", msg.Type, mail.TypeInvite)
	}
	if len(msg.Headers) != 0 {
		t.Errorf("Headers = %v, want empty for a non-user recipient (no preferences to unsubscribe from)", msg.Headers)
	}
	if !strings.Contains(msg.TextBody, token) {
		t.Errorf("delivered body does not carry the invite token:\n%s", msg.TextBody)
	}

	// Accept the invite with the link from the mail: it goes through once.
	reg := registerWithInviteJSON(t, srv, "invitee", "invitee@example.com", token)
	if reg.Code != http.StatusOK {
		t.Fatalf("register with mailed invite = %d, want 200 (body %s)", reg.Code, snippet(reg.Body.String()))
	}
	after, _ := srv.invites.ByToken(context.Background(), token)
	if after.UsedAt == nil {
		t.Error("invite was not marked used after acceptance")
	}
}

// TestInviteLinkCopiedManuallyStillWorks proves that adding email did not break the original manual copy-the-link path.
func TestInviteLinkCopiedManuallyStillWorks(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	srv.invites = store.NewInviteStore(db)
	startInviteMail(t, srv, db)

	created, resp := createInviteJSON(t, srv, cookies, csrf, map[string]any{"role": "user"})
	if created.Code != http.StatusCreated || resp.MailNotice != "" {
		t.Fatalf("POST /api/v1/admin/invites = %d mail_notice %q, want 201 (no mail)",
			created.Code, resp.MailNotice)
	}
	invites, _ := srv.invites.List(context.Background())
	if len(invites) != 1 {
		t.Fatalf("invites = %d, want 1", len(invites))
	}
	reg := registerWithInviteJSON(t, srv, "manual", "manual@example.com", invites[0].Token)
	if reg.Code != http.StatusOK {
		t.Fatalf("register with copied invite = %d, want 200 (body %s)", reg.Code, snippet(reg.Body.String()))
	}
}

// TestRevokedAndExpiredInviteRefused covers the negative cases: revoked / expired links are refused.
func TestRevokedAndExpiredInviteRefused(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	srv.invites = store.NewInviteStore(db)

	// Revocation: create → revoke → signing up with the original token is refused.
	if rec, _ := createInviteJSON(t, srv, cookies, csrf, map[string]any{"role": "user"}); rec.Code != http.StatusCreated {
		t.Fatalf("create invite = %d, want 201", rec.Code)
	}
	invites, _ := srv.invites.List(context.Background())
	if len(invites) != 1 {
		t.Fatalf("invites = %d, want 1", len(invites))
	}
	revoked := invites[0]
	rev := postJSONWithSession(srv, "/api/v1/admin/invites/"+revoked.PublicID+"/revoke", nil, cookies, csrf)
	if rev.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d, want 204", rev.Code)
	}
	denied := registerWithInviteJSON(t, srv, "late", "late@example.com", revoked.Token)
	if denied.Code != http.StatusForbidden || apiErrorCode(t, denied) != "invite_invalid" {
		t.Errorf("register with revoked invite = %d %s, want 403 invite_invalid", denied.Code, apiErrorCode(t, denied))
	}

	// Expiry: write an already-expired invite directly → signup is refused.
	expired := &store.Invite{Role: store.RoleUser}
	exp := time.Now().UTC().Add(-time.Hour)
	expired.ExpiresAt = &exp
	if err := srv.invites.Create(context.Background(), expired); err != nil {
		t.Fatalf("create expired invite: %v", err)
	}
	deniedExp := registerWithInviteJSON(t, srv, "stale", "stale@example.com", expired.Token)
	if deniedExp.Code != http.StatusForbidden || apiErrorCode(t, deniedExp) != "invite_invalid" {
		t.Errorf("register with expired invite = %d %s, want 403 invite_invalid", deniedExp.Code, apiErrorCode(t, deniedExp))
	}
}

// TestInviteMailUnconfiguredStillCreatesInvite covers unconfigured SMTP: when mail is requested the reply states
// mail_notice=invite_mail_unconfigured faithfully, yet the invite is still created successfully (never silently, and creation is not blocked because mail is unconfigured).
func TestInviteMailUnconfiguredStillCreatesInvite(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	srv.invites = store.NewInviteStore(db)
	srv.setMailOutbox(nil, srv.baseURL) // mail not wired up

	created, resp := createInviteJSON(t, srv, cookies, csrf, map[string]any{
		"role": "user", "email": "invitee@example.com", "send_email": true,
	})
	if created.Code != http.StatusCreated || resp.MailNotice != noticeInviteMailUnconfigured {
		t.Fatalf("POST with send_email while unconfigured = %d mail_notice %q, want 201 %q",
			created.Code, resp.MailNotice, noticeInviteMailUnconfigured)
	}
	// The invite itself is still created successfully.
	if n, _ := srv.invites.List(context.Background()); len(n) != 1 {
		t.Errorf("invites = %d, want 1 (invite must still be created)", len(n))
	}
}

// TestInviteMailHonorsRecipientPreference proves type B is optional: if the recipient is a site user who has turned the type off,
// no mail is sent, but the invite is still created successfully and the link can still be copied by hand.
func TestInviteMailHonorsRecipientPreference(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	srv.invites = store.NewInviteStore(db)
	sender := startInviteMail(t, srv, db)

	invitee, err := srv.accounts.CreateLocalUser(context.Background(), auth.CreateUserInput{
		Username: "pref-user", Email: "pref-user@example.com", Password: "Sup3rSecret!", Role: store.RoleUser,
	})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	if err := store.NewEmailPrefStore(db).SetChoices(context.Background(), invitee.ID,
		map[string]bool{string(mail.TypeInvite): false}, time.Now().UTC()); err != nil {
		t.Fatalf("SetChoices: %v", err)
	}

	created, resp := createInviteJSON(t, srv, cookies, csrf, map[string]any{
		"role": "user", "email": "pref-user@example.com", "send_email": true,
	})
	if created.Code != http.StatusCreated || resp.MailNotice != noticeInviteMailOptedOut {
		t.Fatalf("POST opted-out recipient = %d mail_notice %q, want 201 %q",
			created.Code, resp.MailNotice, noticeInviteMailOptedOut)
	}
	time.Sleep(50 * time.Millisecond)
	if got := len(sender.messages()); got != 0 {
		t.Errorf("delivered %d messages, want 0 (recipient opted out)", got)
	}
	if n, _ := srv.invites.List(context.Background()); len(n) != 1 {
		t.Errorf("invites = %d, want 1 (invite must still be created)", len(n))
	}
}
