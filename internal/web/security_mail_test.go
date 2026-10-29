package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

// This file holds the acceptance tests for class-A transactional security mail.
//
// Covers: each of the five types' trigger points, reset links being single-use and expiring, only digests stored in the database, plaintext kept out of the logs,
// class A carrying no unsubscribe header, and the flow being disabled when SMTP is not configured. The pages have moved to the SPA, so every trigger point goes through same-origin JSON endpoints
// (/api/v1/auth/*, /api/v1/settings/*).
//
// The tests use a recording Sender to take the enqueued mail, so they do not depend on a real SMTP server; delivery itself is
// the outbox worker's job, and here we only verify "the trigger point really wrote a mail of the right type into the queue".

// recordingSender is a test double for mail.Sender: every delivery and handshake succeeds without recording anything.
type recordingSender struct{}

func (r *recordingSender) Send(_ context.Context, _ mail.SMTPConfig, _ mail.Message) error {
	return nil
}

// Test implements mail.Sender's handshake method; the test double always succeeds.
func (r *recordingSender) Test(_ context.Context, _ mail.SMTPConfig) error { return nil }

// securityTestServer gathers the handles one test needs.
type securityTestServer struct {
	srv     *Server
	db      *gorm.DB
	ownerID uint64
	cookies []*http.Cookie
	csrf    string
	logs    *bytes.Buffer
}

// newSecurityServer wires up a test server with accounts, sessions, TOTP and a mail outbox, and logs in owner.
// With withMail false the outbox is deliberately left unwired, to verify "the flow is disabled when SMTP is not configured".
func newSecurityServer(t *testing.T, withMail bool) securityTestServer {
	t.Helper()
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "security.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	users := store.NewUserStore(db)
	sessions := store.NewSessionStore(db)
	accounts, err := auth.NewAccountService(users, sessions, store.NewAPIKeyStore(db), auth.NewPasswordHasher(auth.Params{
		Memory: 8 * 1024, Time: 1, Threads: 1, SaltLength: 16, KeyLength: 32,
	}))
	if err != nil {
		t.Fatalf("NewAccountService() error = %v", err)
	}
	mgr, err := auth.NewSessionManager(users, sessions, auth.SessionConfig{Secret: testSessionSecret})
	if err != nil {
		t.Fatalf("NewSessionManager() error = %v", err)
	}
	auditor, err := auth.NewAuditor(store.NewAuditStore(db), nil)
	if err != nil {
		t.Fatalf("NewAuditor() error = %v", err)
	}
	codec, err := store.NewSecretCodec(testEncryptionKey())
	if err != nil {
		t.Fatalf("NewSecretCodec() error = %v", err)
	}
	totpSvc, err := auth.NewTOTPService(store.NewTOTPStore(db), codec, auth.DefaultTOTPIssuer)
	if err != nil {
		t.Fatalf("NewTOTPService() error = %v", err)
	}

	// SMTP is configured: only with host and from in settings does Outbox.Configured() become true.
	now := time.Now().UTC()
	for k, v := range map[string]string{"smtp_host": "smtp.test", "smtp_from": "noreply@test"} {
		if err := store.PutSetting(context.Background(), db, k, v, nil, now); err != nil {
			t.Fatalf("PutSetting(%s): %v", k, err)
		}
	}
	var outbox *mail.Outbox
	if withMail {
		outbox = mail.NewOutbox(mail.Deps{
			DB:      db,
			Secrets: codec,
			Logger:  discardLogger(),
			Sender:  &recordingSender{},
		})
	}

	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelInfo}))

	srv, err := New("127.0.0.1:0", Deps{
		DB:            db,
		Logger:        logger,
		SchemaVersion: func(ctx context.Context) (int, error) { return store.CurrentVersion(ctx, db) },
		Accounts:      accounts,
		Sessions:      mgr,
		Users:         users,
		Auditor:       auditor,
		TOTP:          totpSvc,
		Mail:          outbox,
		LoginLimiter: auth.NewLoginLimiter(auth.LimiterConfig{
			Sleep: func(context.Context, time.Duration) error { return nil },
		}),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	owner, err := accounts.CreateLocalUser(context.Background(), auth.CreateUserInput{
		Username: "owner", Email: "owner@example.com", Password: "Sup3rSecret!",
		Role: store.RoleAdmin, Locale: "en",
	})
	if err != nil {
		t.Fatalf("CreateLocalUser() error = %v", err)
	}
	// Login goes through the same-origin JSON endpoint: the session cookie and the session-bound CSRF token come back together.
	cookies, csrf := loginJSON(t, srv, db, "owner", "Sup3rSecret!")
	return securityTestServer{srv: srv, db: db, ownerID: srvOwnerID(owner), cookies: cookies, csrf: csrf, logs: logs}
}

func srvOwnerID(u *store.User) uint64 { return u.ID }

// outboxByType returns the mail rows of the given type in the queue (ascending by id).
func outboxByType(t *testing.T, db *gorm.DB, typ mail.Type) []store.OutboxMessage {
	t.Helper()
	var rows []store.OutboxMessage
	if err := db.Where("type = ?", string(typ)).Order("id asc").Find(&rows).Error; err != nil {
		t.Fatalf("query outbox by type: %v", err)
	}
	return rows
}

// tokenFromBody pulls the plaintext token out of the link in the mail body.
func tokenFromBody(t *testing.T, body string) string {
	t.Helper()
	m := tokenRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no token found in mail body: %s", snippet(body))
	}
	return m[1]
}

// assertNoUnsubscribeHeader asserts none of these class A mails carries an unsubscribe header.
// mail.Mailer gives a class A type no unsubscribe link, so headers_json is an empty object and neither
// List-Unsubscribe nor List-Unsubscribe-Post ever appears.
func assertNoUnsubscribeHeader(t *testing.T, rows []store.OutboxMessage) {
	t.Helper()
	for _, row := range rows {
		if strings.Contains(strings.ToLower(row.HeadersJSON), "unsubscribe") {
			t.Errorf("class A mail %d (%s) carries an unsubscribe header: %s", row.ID, row.Type, row.HeadersJSON)
		}
		raw := strings.TrimSpace(row.HeadersJSON)
		if raw == "" || raw == "{}" || raw == "null" {
			continue
		}
		var headers map[string]string
		if err := json.Unmarshal([]byte(raw), &headers); err != nil {
			t.Errorf("mail %d headers_json is not a JSON object: %q", row.ID, raw)
			continue
		}
		for k := range headers {
			if strings.EqualFold(k, "List-Unsubscribe") || strings.EqualFold(k, "List-Unsubscribe-Post") {
				t.Errorf("class A mail %d (%s) carries unsubscribe header %q", row.ID, row.Type, k)
			}
		}
	}
}

// issueResetToken goes through the real trigger point POST /api/v1/auth/forgot-password and returns the plaintext token in the mail body plus that mail.
func issueResetToken(t *testing.T, ts securityTestServer) (string, store.OutboxMessage) {
	t.Helper()
	cookie, headers := preSessionPair(t, ts.srv, "/forgot-password")
	rec := postJSON(ts.srv, "/api/v1/auth/forgot-password",
		map[string]string{"email": "owner@example.com"}, []*http.Cookie{cookie}, headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/forgot-password status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	rows := outboxByType(t, ts.db, mail.TypePasswordReset)
	if len(rows) == 0 {
		t.Fatal("no password_reset mail enqueued after the reset request")
	}
	last := rows[len(rows)-1]
	return tokenFromBody(t, last.TextBody), last
}

// resetPasswordJSON sets a new password with a one-time token via the JSON endpoint and returns the response.
func resetPasswordJSON(t *testing.T, ts securityTestServer, token, password string) *httptest.ResponseRecorder {
	t.Helper()
	cookie, headers := preSessionPair(t, ts.srv, "/reset-password")
	return postJSON(ts.srv, "/api/v1/auth/reset-password",
		map[string]string{"token": token, "password": password}, []*http.Cookie{cookie}, headers)
}

// resendVerificationJSON resends the verification mail (session endpoint).
func resendVerificationJSON(t *testing.T, ts securityTestServer) *httptest.ResponseRecorder {
	t.Helper()
	return postJSON(ts.srv, "/api/v1/settings/verify-email", nil, ts.cookies, map[string]string{auth.CSRFHeaderName: ts.csrf})
}

// verifyEmailJSON consumes a verification token (no login needed, double-submit CSRF).
func verifyEmailJSON(t *testing.T, ts securityTestServer, token string) *httptest.ResponseRecorder {
	t.Helper()
	cookie, headers := preSessionPair(t, ts.srv, "/verify-email")
	return postJSON(ts.srv, "/api/v1/auth/verify-email",
		map[string]string{"token": token}, []*http.Cookie{cookie}, headers)
}

// createTargetUser inserts an ordinary user row that an admin can act on directly (independent of the registration policy).
func createTargetUser(t *testing.T, ts securityTestServer) uint64 {
	t.Helper()
	u := &store.User{
		Username: "target", Email: "target@example.com", DisplayName: "Target",
		Role: store.RoleUser, Status: store.StatusActive, Locale: "en",
		Timezone: "UTC", CreatedAt: time.Now().UTC(),
	}
	if err := store.NewUserStore(ts.db).Create(context.Background(), u); err != nil {
		t.Fatalf("create target user: %v", err)
	}
	return u.ID
}

// —— password reset ——

var tokenRe = regexp.MustCompile(`token=([A-Za-z0-9_\-]+)`)

// TestSecurityMailFiveClassATypesDeliveredAtTriggers is the main acceptance for the five class A mail types:
// it triggers the five business actions one by one and asserts a row of the matching mail.Type appears in the outbox, each carrying no unsubscribe header.
func TestSecurityMailFiveClassATypesDeliveredAtTriggers(t *testing.T) {
	ts := newSecurityServer(t, true)

	// 1) New device / new IP login alert: the fixture's login is itself the trigger point.
	deviceRows := outboxByType(t, ts.db, mail.TypeNewDeviceLogin)
	if len(deviceRows) != 1 {
		t.Errorf("new_device_login rows after a fresh login = %d, want 1", len(deviceRows))
	}
	assertNoUnsubscribeHeader(t, deviceRows)

	// 2) Password reset.
	_, resetRow := issueResetToken(t, ts)
	assertNoUnsubscribeHeader(t, []store.OutboxMessage{resetRow})

	// 3) Email verification (resend the verification mail; needs login + session-bound CSRF).
	verify := resendVerificationJSON(t, ts)
	if verify.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/settings/verify-email status = %d, want 200 (body %s)", verify.Code, snippet(verify.Body.String()))
	}
	verifyRows := outboxByType(t, ts.db, mail.TypeEmailVerification)
	if len(verifyRows) != 1 {
		t.Errorf("email_verification rows after resend = %d, want 1", len(verifyRows))
	}
	assertNoUnsubscribeHeader(t, verifyRows)

	// 4) Credential change notice (password change) — the SSR form went away with the page layer, so this goes through the JSON endpoint the SPA uses.
	change := jsonRequest(t, ts.srv, http.MethodPatch, "/api/v1/settings/password",
		`{"old_password":"Sup3rSecret!","new_password":"N3wSup3rSecret!"}`, ts.cookies, ts.csrf)
	if change.Code != http.StatusNoContent {
		t.Fatalf("PATCH /api/v1/settings/password status = %d, want 204 (body %s)", change.Code, snippet(change.Body.String()))
	}
	credRows := outboxByType(t, ts.db, mail.TypeCredentialChanged)
	if len(credRows) != 1 {
		t.Errorf("credential_changed rows after a password change = %d, want 1", len(credRows))
	}
	assertNoUnsubscribeHeader(t, credRows)

	// 5) Account disabled notice (an admin disables another account).
	targetID := createTargetUser(t, ts)
	disable := jsonRequest(t, ts.srv, http.MethodPost, "/api/v1/admin/users/"+userPublicID(t, ts.db, targetID)+"/status",
		`{"action":"disable"}`, ts.cookies, ts.csrf)
	if disable.Code != http.StatusNoContent {
		t.Fatalf("POST /api/v1/admin/users/:id/status status = %d, want 204 (body %s)", disable.Code, snippet(disable.Body.String()))
	}
	statusRows := outboxByType(t, ts.db, mail.TypeAccountStatus)
	if len(statusRows) != 1 {
		t.Fatalf("account_status rows after disabling a user = %d, want 1", len(statusRows))
	}
	if statusRows[0].To != "target@example.com" {
		t.Errorf("account_status mail recipient = %q, want target@example.com", statusRows[0].To)
	}
	assertNoUnsubscribeHeader(t, statusRows)
}

// TestSecurityMailResetLinkIsSingleUse is a negative case: a reset link works once only, and a second use is rejected.
func TestSecurityMailResetLinkIsSingleUse(t *testing.T) {
	ts := newSecurityServer(t, true)
	plain, _ := issueResetToken(t, ts)

	// First time: the submit succeeds and the new password really takes effect (it can log in).
	first := resetPasswordJSON(t, ts, plain, "N3wSup3rSecret!")
	if first.Code != http.StatusOK {
		t.Fatalf("first reset submit status = %d, want 200 (body %s)", first.Code, snippet(first.Body.String()))
	}
	if !strings.Contains(first.Body.String(), `"reset":true`) {
		t.Errorf("first reset did not report success: %s", snippet(first.Body.String()))
	}
	loginJSON(t, ts.srv, ts.db, "owner", "N3wSup3rSecret!")

	// Second use of the same token: must be rejected (single use).
	second := resetPasswordJSON(t, ts, plain, "An0therSecret!")
	if second.Code != http.StatusBadRequest || apiErrorCode(t, second) != "token_used" {
		t.Fatalf("second reset submit = %d %s, want 400 token_used (body %s)", second.Code, apiErrorCode(t, second), snippet(second.Body.String()))
	}
}

// TestSecurityMailResetLinkExpires is a negative case: an expired reset link is rejected.
func TestSecurityMailResetLinkExpires(t *testing.T) {
	ts := newSecurityServer(t, true)
	// It issues an already expired token directly: Issue takes a TTL, and a negative value means already expired.
	plain, err := ts.srv.tokens.Issue(context.Background(), ts.ownerID, store.ActionTokenPasswordReset, "", -time.Minute)
	if err != nil {
		t.Fatalf("issue expired token: %v", err)
	}
	rec := resetPasswordJSON(t, ts, plain, "N3wSup3rSecret!")
	if rec.Code != http.StatusBadRequest || apiErrorCode(t, rec) != "token_expired" {
		t.Fatalf("expired reset submit = %d %s, want 400 token_expired (body %s)", rec.Code, apiErrorCode(t, rec), snippet(rec.Body.String()))
	}
}

// TestSecurityMailResetTokenStoredAsDigestOnly is a negative case: only the digest is stored, never the plaintext token.
func TestSecurityMailResetTokenStoredAsDigestOnly(t *testing.T) {
	ts := newSecurityServer(t, true)
	plain, _ := issueResetToken(t, ts)

	var rows []store.ActionToken
	if err := ts.db.Find(&rows).Error; err != nil {
		t.Fatalf("load action tokens: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no action_tokens row written for a password reset")
	}
	want := store.HashActionToken(plain)
	found := false
	for _, row := range rows {
		if row.TokenHash == plain {
			t.Errorf("action token %d stores the plaintext token verbatim", row.ID)
		}
		if row.TokenHash == want {
			found = true
		}
	}
	if !found {
		t.Errorf("no action_tokens row holds sha256(%s); rows = %+v", plain, rows)
	}
	if strings.Contains(fmt.Sprintf("%+v", rows), plain) {
		t.Errorf("the plaintext token appears in the stored action_tokens rows: %+v", rows)
	}
}

// TestSecurityMailTokenNeverLogged is a negative case: the plaintext token never reaches the logs.
func TestSecurityMailTokenNeverLogged(t *testing.T) {
	ts := newSecurityServer(t, true)
	plain, _ := issueResetToken(t, ts)

	// It walks the consume path once more (success + second failure) to make sure the failure branch does not print plaintext either.
	resetPasswordJSON(t, ts, plain, "N3wSup3rSecret!")
	resetPasswordJSON(t, ts, plain, "N3wSup3rSecret!")

	if logs := ts.logs.String(); strings.Contains(logs, plain) {
		t.Errorf("the plaintext reset token appears in the server log:\n%s", snippet(logs))
	}
}

// TestSecurityMailClassAHasNoUnsubscribeHeader is a negative case: none of the five class A mail types carries an unsubscribe header.
func TestSecurityMailClassAHasNoUnsubscribeHeader(t *testing.T) {
	ts := newSecurityServer(t, true)
	// Triggers all five types.
	_, _ = issueResetToken(t, ts)
	resendVerificationJSON(t, ts)
	jsonRequest(t, ts.srv, http.MethodPatch, "/api/v1/settings/password",
		`{"old_password":"Sup3rSecret!","new_password":"N3wSup3rSecret!"}`, ts.cookies, ts.csrf)
	targetID := createTargetUser(t, ts)
	jsonRequest(t, ts.srv, http.MethodPost, "/api/v1/admin/users/"+userPublicID(t, ts.db, targetID)+"/status",
		`{"action":"disable"}`, ts.cookies, ts.csrf)

	for _, typ := range []mail.Type{
		mail.TypePasswordReset, mail.TypeEmailVerification, mail.TypeNewDeviceLogin,
		mail.TypeCredentialChanged, mail.TypeAccountStatus,
	} {
		rows := outboxByType(t, ts.db, typ)
		if len(rows) == 0 {
			t.Errorf("no %s mail enqueued; cannot assert its headers", typ)
			continue
		}
		assertNoUnsubscribeHeader(t, rows)
	}
}

// TestSecurityMailEmailVerifyAndChangeFlow covers both paths — email verification and email change confirmation (JSON).
func TestSecurityMailEmailVerifyAndChangeFlow(t *testing.T) {
	ts := newSecurityServer(t, true)

	// Email verification: resend → consume the token → email_verified_at gets set; the token is single-use.
	resend := resendVerificationJSON(t, ts)
	if resend.Code != http.StatusOK {
		t.Fatalf("resend verification status = %d, want 200 (body %s)", resend.Code, snippet(resend.Body.String()))
	}
	verifyRows := outboxByType(t, ts.db, mail.TypeEmailVerification)
	plain := tokenFromBody(t, verifyRows[len(verifyRows)-1].TextBody)
	ok := verifyEmailJSON(t, ts, plain)
	if ok.Code != http.StatusOK {
		t.Fatalf("verify-email status = %d, want 200 (body %s)", ok.Code, snippet(ok.Body.String()))
	}
	var owner store.User
	if err := ts.db.First(&owner, ts.ownerID).Error; err != nil {
		t.Fatalf("reload owner: %v", err)
	}
	if owner.EmailVerifiedAt == nil {
		t.Error("email_verified_at is still NULL after consuming the verification link")
	}
	again := verifyEmailJSON(t, ts, plain)
	if again.Code != http.StatusBadRequest || apiErrorCode(t, again) != "token_used" {
		t.Errorf("reused verification token = %d %s, want 400 token_used", again.Code, apiErrorCode(t, again))
	}

	// Email change: a confirmation mail goes to the new address, and nothing in the database changes before confirmation.
	change := postJSON(ts.srv, "/api/v1/settings/email",
		map[string]string{"email": "new-owner@example.com"}, ts.cookies, map[string]string{auth.CSRFHeaderName: ts.csrf})
	if change.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/settings/email status = %d, want 200 (body %s)", change.Code, snippet(change.Body.String()))
	}
	rows := outboxByType(t, ts.db, mail.TypeEmailVerification)
	changeRows := rows[len(rows)-1]
	if changeRows.To != "new-owner@example.com" {
		t.Errorf("email change confirmation recipient = %q, want new-owner@example.com", changeRows.To)
	}
	// The email in the database is unchanged before confirmation.
	if err := ts.db.First(&owner, ts.ownerID).Error; err != nil {
		t.Fatalf("reload owner: %v", err)
	}
	if owner.Email != "owner@example.com" {
		t.Fatalf("email changed before confirmation: %q", owner.Email)
	}
	changeToken := tokenFromBody(t, changeRows.TextBody)
	cc, ch := preSessionPair(t, ts.srv, "/confirm-email-change")
	confirm := postJSON(ts.srv, "/api/v1/auth/confirm-email-change",
		map[string]string{"token": changeToken}, []*http.Cookie{cc}, ch)
	if confirm.Code != http.StatusOK {
		t.Fatalf("confirm-email-change status = %d, want 200 (body %s)", confirm.Code, snippet(confirm.Body.String()))
	}
	if err := ts.db.First(&owner, ts.ownerID).Error; err != nil {
		t.Fatalf("reload owner: %v", err)
	}
	if owner.Email != "new-owner@example.com" {
		t.Errorf("email after confirmation = %q, want new-owner@example.com", owner.Email)
	}
}

// TestSecurityMailNewDeviceLoginDeduped asserts a repeated login with the same fingerprint no longer alerts (so a mail is not sent on every login).
func TestSecurityMailNewDeviceLoginDeduped(t *testing.T) {
	ts := newSecurityServer(t, true)
	before := len(outboxByType(t, ts.db, mail.TypeNewDeviceLogin))
	// Log in once more from the same httptest client (same IP + same User-Agent).
	loginJSON(t, ts.srv, ts.db, "owner", "Sup3rSecret!")
	after := len(outboxByType(t, ts.db, mail.TypeNewDeviceLogin))
	if after != before {
		t.Errorf("new_device_login rows %d -> %d after an identical second login, want no new mail", before, after)
	}
}

// TestSecurityMailDisabledWhenSMTPNotConfigured asserts the flow is disabled and states why when SMTP is not configured, never silently.
// The SPA renders "email is not enabled on this site" from mail_ready=false in the response; the server no longer renders an explanation page.
func TestSecurityMailDisabledWhenSMTPNotConfigured(t *testing.T) {
	ts := newSecurityServer(t, false)
	cookie, headers := preSessionPair(t, ts.srv, "/forgot-password")
	rec := postJSON(ts.srv, "/api/v1/auth/forgot-password",
		map[string]string{"email": "owner@example.com"}, []*http.Cookie{cookie}, headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/forgot-password status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var res struct {
		MailReady bool `json:"mail_ready"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if res.MailReady {
		t.Error("mail_ready = true while SMTP is unconfigured")
	}
	if rows := outboxByType(t, ts.db, mail.TypePasswordReset); len(rows) != 0 {
		t.Errorf("password_reset mail enqueued while SMTP is unconfigured: %d rows", len(rows))
	}
}
