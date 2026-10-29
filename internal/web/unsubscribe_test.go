package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is the HTTP-level acceptance for (RFC 8058 one-click unsubscribe). It covers three acceptances:
//  1. a one-click POST disables only the type the token names;
//  2. a token cannot be replayed onto another type or another user;
//  3. class A mail carries no unsubscribe header (the class A positive case is in internal/mail/unsubscribe_test.go).
//
// The one-click POST always goes **without cookies** (not logged in), to prove the endpoint really works login-free.
// After the SSR pages were removed, unsubscribe is carried by three transports: GET /unsubscribe returns the app shell and sets the double-submit cookie,
// POST /unsubscribe is the RFC 8058 One-Click machine endpoint (status code only, no template rendered),
// and /api/v1/unsubscribe is the SPA's same-origin JSON transport (GET reads without consuming, POST consumes).

// tokenFromUnsubscribeHeader pulls the token out of the List-Unsubscribe header (of the form <URL>).
func tokenFromUnsubscribeHeader(t *testing.T, header string) string {
	t.Helper()
	raw := strings.Trim(strings.TrimSpace(header), "<>")
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse List-Unsubscribe URL %q: %v", raw, err)
	}
	token := u.Query().Get("token")
	if token == "" {
		t.Fatalf("List-Unsubscribe URL %q carries no token", raw)
	}
	return token
}

// waitForMailOfType waits for the double to receive a mail of the given type (the worker delivers asynchronously; the fixture login enqueues other types first).
func waitForMailOfType(t *testing.T, sender *recordingMailSender, typ string) mail.Message {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, m := range sender.messages() {
			if m.Type == typ {
				return m
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for a %s mail, got %d messages", typ, len(sender.messages()))
	return mail.Message{}
}

// oneClickPost sends an RFC 8058 One-Click POST (no cookie, no CSRF), optionally with extra form fields,
// to verify that "a type parameter slipped in by the caller is ignored".
func oneClickPost(t *testing.T, srv *Server, token string, extra url.Values) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"List-Unsubscribe": {"One-Click"}}
	for k, v := range extra {
		form[k] = v
	}
	return postForm(t, srv, "/unsubscribe?token="+url.QueryEscape(token), form, nil)
}

// TestUnsubscribeOneClickDisablesOnlyThatType is the main evidence for acceptance ①, and also proves:
//   - a class B (optional type) mail carries both RFC 8058 headers, and the token in them works;
//   - the one-click POST returns 200 while **logged out** (no cookie);
//   - only the invite named by the token is disabled, and the user's switches for other optional types are unaffected;
//   - even if the caller stuffs type=deck_shared into the form, it is not honoured.
func TestUnsubscribeOneClickDisablesOnlyThatType(t *testing.T) {
	ts := newSecurityServer(t, true)
	ts.srv.invites = store.NewInviteStore(ts.db)
	sender := startInviteMail(t, ts.srv, ts.db)
	ctx := context.Background()

	// The recipient must be a user of this site: unsubscribing changes a user preference, and a non-user has no switch to turn off.
	invitee, err := ts.srv.accounts.CreateLocalUser(ctx, auth.CreateUserInput{
		Username: "invitee", Email: "invitee@example.com", Password: "Sup3rSecret!", Role: store.RoleUser,
	})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	prefs := store.NewEmailPrefStore(ts.db)
	// Turn on another optional type (class C is off by default, so turn it on explicitly) to verify "only that one is disabled".
	if err := prefs.SetChoices(ctx, invitee.ID, map[string]bool{
		string(mail.TypeReviewReminder): true,
	}, time.Now().UTC()); err != nil {
		t.Fatalf("SetChoices: %v", err)
	}

	// The admin sends the invite to this registered address (the SSR /admin/invites form endpoint is gone, so it goes through the JSON endpoint).
	created := postJSON(ts.srv, "/api/v1/admin/invites", map[string]any{
		"role": "user", "email": "invitee@example.com", "send_email": true,
	}, ts.cookies, map[string]string{auth.CSRFHeaderName: ts.csrf})
	if created.Code != http.StatusCreated {
		t.Fatalf("POST /api/v1/admin/invites = %d, want 201 (body %s)",
			created.Code, snippet(created.Body.String()))
	}
	// On a successful send, mail_notice comes back with the stable result code invite_mail_queued (non-empty means enqueued).
	if !strings.Contains(created.Body.String(), `"mail_notice":"`+noticeInviteMailQueued+`"`) {
		t.Fatalf("invite create did not queue the mail: %s", snippet(created.Body.String()))
	}

	// The fixture login itself enqueues a new_device_login; only the invite one is taken.
	msg := waitForMailOfType(t, sender, string(mail.TypeInvite))
	unsub := msg.Headers[mail.HeaderListUnsubscribe]
	if unsub == "" {
		t.Fatalf("optional B-class mail carries no %s header; headers = %v", mail.HeaderListUnsubscribe, msg.Headers)
	}
	if got := msg.Headers[mail.HeaderListUnsubscribePost]; got != mail.ListUnsubscribePostValue {
		t.Errorf("%s = %q, want %q", mail.HeaderListUnsubscribePost, got, mail.ListUnsubscribePostValue)
	}
	token := tokenFromUnsubscribeHeader(t, unsub)

	// One-click POST: no cookies at all (logged out), with a type parameter deliberately stuffed in to see whether it is honoured.
	rec := oneClickPost(t, ts.srv, token, url.Values{"type": {"deck_shared"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("one-click POST (no login) status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	choices, err := prefs.Choices(ctx, invitee.ID)
	if err != nil {
		t.Fatalf("Choices: %v", err)
	}
	if on, present := choices[string(mail.TypeInvite)]; !present || on {
		t.Errorf("invite preference = %v (present=%v), want explicitly off after unsubscribe", on, present)
	}
	if !choices[string(mail.TypeReviewReminder)] {
		t.Errorf("review_reminder preference = %v, want still on (only the named type may change)", choices[string(mail.TypeReviewReminder)])
	}
	if !mail.ResolveEnabled(choices, mail.TypeDeckShared) {
		t.Errorf("deck_shared resolved = false, want still on (the injected type=deck_shared must be ignored)")
	}
	if !mail.ResolveEnabled(choices, mail.TypeInvite) {
		t.Logf("after one-click, invite is off and the other optional types are untouched: %v", choices)
	} else {
		t.Error("invite is still enabled after a successful one-click unsubscribe")
	}
}

// TestUnsubscribeTokenNotReplayableAcrossTypesOrUsers is acceptance ②:
//   - POSTing with an already used token is refused (single use, not replayable);
//   - a token only affects its own user, and another user's preferences are completely untouched.
func TestUnsubscribeTokenNotReplayableAcrossTypesOrUsers(t *testing.T) {
	ts := newSecurityServer(t, false)
	ctx := context.Background()
	prefs := store.NewEmailPrefStore(ts.db)

	// User A: the token's owner.
	userA, err := ts.srv.accounts.CreateLocalUser(ctx, auth.CreateUserInput{
		Username: "a", Email: "a@example.com", Password: "Sup3rSecret!", Role: store.RoleUser,
	})
	if err != nil {
		t.Fatalf("CreateLocalUser A: %v", err)
	}
	// User B: must not be affected by A's token.
	userB, err := ts.srv.accounts.CreateLocalUser(ctx, auth.CreateUserInput{
		Username: "b", Email: "b@example.com", Password: "Sup3rSecret!", Role: store.RoleUser,
	})
	if err != nil {
		t.Fatalf("CreateLocalUser B: %v", err)
	}

	token, err := ts.srv.tokens.Issue(ctx, userA.ID, store.ActionTokenUnsubscribe, string(mail.TypeInvite), mail.UnsubscribeTTL)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	first := oneClickPost(t, ts.srv, token, nil)
	if first.Code != http.StatusOK {
		t.Fatalf("first one-click status = %d, want 200 (body %s)", first.Code, snippet(first.Body.String()))
	}
	// Replaying the same token: must be refused (single use).
	second := oneClickPost(t, ts.srv, token, nil)
	if second.Code != http.StatusBadRequest {
		t.Fatalf("replayed one-click status = %d, want 400 (body %s)", second.Code, snippet(second.Body.String()))
	}
	if !strings.Contains(second.Body.String(), "token_used") {
		t.Errorf("replay did not report the link as used (token_used): %s", snippet(second.Body.String()))
	}

	choicesA, err := prefs.Choices(ctx, userA.ID)
	if err != nil {
		t.Fatalf("Choices A: %v", err)
	}
	if on, present := choicesA[string(mail.TypeInvite)]; !present || on {
		t.Errorf("A.invite = %v (present=%v), want off", on, present)
	}
	choicesB, err := prefs.Choices(ctx, userB.ID)
	if err != nil {
		t.Fatalf("Choices B: %v", err)
	}
	if len(choicesB) != 0 {
		t.Errorf("B's preferences were touched by A's unsubscribe token: %v", choicesB)
	}
}

// TestUnsubscribeRefusesClassAToken is the server-side defence for acceptance ③:
// even if someone holds a token that "names class A" (the normal flow never issues one), the endpoint refuses it and writes no preference.
func TestUnsubscribeRefusesClassAToken(t *testing.T) {
	ts := newSecurityServer(t, false)
	ctx := context.Background()

	token, err := ts.srv.tokens.Issue(ctx, 1, store.ActionTokenUnsubscribe, string(mail.TypePasswordReset), mail.UnsubscribeTTL)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	rec := oneClickPost(t, ts.srv, token, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("class A token status = %d, want 400 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	choices, err := store.NewEmailPrefStore(ts.db).Choices(ctx, 1)
	if err != nil {
		t.Fatalf("Choices: %v", err)
	}
	if _, present := choices[string(mail.TypePasswordReset)]; present {
		t.Errorf("a class A token wrote a preference: %v", choices)
	}
}

// TestUnsubscribePageDoesNotConsumeToken proves the GET confirm page does not consume the token (preventing stray unsubscribes from link prefetching),
// and that the real unsubscribe is still done by POST.
func TestUnsubscribePageDoesNotConsumeToken(t *testing.T) {
	ts := newSecurityServer(t, false)
	ctx := context.Background()
	user, err := ts.srv.accounts.CreateLocalUser(ctx, auth.CreateUserInput{
		Username: "c", Email: "c@example.com", Password: "Sup3rSecret!", Role: store.RoleUser,
	})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	token, err := ts.srv.tokens.Issue(ctx, user.ID, store.ActionTokenUnsubscribe, string(mail.TypeReviewReminder), mail.UnsubscribeTTL)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	for i := 0; i < 2; i++ {
		page := getWithCookies(t, ts.srv, "/unsubscribe?token="+url.QueryEscape(token), nil)
		if page.Code != http.StatusOK {
			t.Fatalf("GET /unsubscribe #%d status = %d, want 200 (body %s)", i+1, page.Code, snippet(page.Body.String()))
		}
		// GET now returns the SPA app shell (no longer an SSR confirm page) and sets the pre-session double-submit cookie.
		if !strings.Contains(page.Body.String(), `id="app"`) {
			t.Errorf("GET /unsubscribe #%d did not serve the SPA shell: %s", i+1, snippet(page.Body.String()))
		}
		if findCookie(page, auth.CSRFDoubleSubmitCookieName) == nil {
			t.Errorf("GET /unsubscribe #%d did not initialize the double-submit cookie", i+1)
		}
	}
	// GET does not consume: the preference is unchanged and the token still works.
	choices, _ := store.NewEmailPrefStore(ts.db).Choices(ctx, user.ID)
	if len(choices) != 0 {
		t.Fatalf("GET consumed the token or wrote a preference: %v", choices)
	}
	rec := oneClickPost(t, ts.srv, token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST after GETs status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	choices, _ = store.NewEmailPrefStore(ts.db).Choices(ctx, user.ID)
	if on, present := choices[string(mail.TypeReviewReminder)]; !present || on {
		t.Errorf("review_reminder = %v (present=%v), want off after POST", on, present)
	}
}

// TestUnsubscribeExpiredTokenRejected proves an expired token is rejected with a stable hint.
func TestUnsubscribeExpiredTokenRejected(t *testing.T) {
	ts := newSecurityServer(t, false)
	ctx := context.Background()
	token, err := ts.srv.tokens.Issue(ctx, 1, store.ActionTokenUnsubscribe, string(mail.TypeInvite), -time.Minute)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	rec := oneClickPost(t, ts.srv, token, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expired token status = %d, want 400 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if !strings.Contains(rec.Body.String(), "token_expired") {
		t.Errorf("expired token did not report expiry (token_expired): %s", snippet(rec.Body.String()))
	}
}

// ── SPA same-origin JSON transport (read and confirm) ─────────────────────

// TestUnsubscribeJSONReadDoesNotConsume asserts the read endpoint returns the optional type the token names without consuming the token (it can be read repeatedly).
func TestUnsubscribeJSONReadDoesNotConsume(t *testing.T) {
	ts := newSecurityServer(t, false)
	ctx := context.Background()
	user, err := ts.srv.accounts.CreateLocalUser(ctx, auth.CreateUserInput{
		Username: "unsub-reader", Email: "unsub-reader@example.com", Password: "Sup3rSecret!", Role: store.RoleUser,
	})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	token, err := ts.srv.tokens.Issue(ctx, user.ID, store.ActionTokenUnsubscribe, string(mail.TypeReviewReminder), mail.UnsubscribeTTL)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	for i := 0; i < 2; i++ {
		rec := get(t, ts.srv, "/api/v1/unsubscribe?token="+url.QueryEscape(token), nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/v1/unsubscribe #%d = %d, want 200 (body %s)", i+1, rec.Code, snippet(rec.Body.String()))
		}
		if !strings.Contains(rec.Body.String(), `"type":"review_reminder"`) {
			t.Errorf("read did not return the named type: %s", snippet(rec.Body.String()))
		}
	}
	if choices, _ := store.NewEmailPrefStore(ts.db).Choices(ctx, user.ID); len(choices) != 0 {
		t.Fatalf("read consumed the token or wrote a preference: %v", choices)
	}
}

// TestUnsubscribeJSONConfirmConsumesOnlyNamedType asserts the confirm endpoint consumes the token and disables only the named optional type,
// and writes one audit row identical to SSR's.
func TestUnsubscribeJSONConfirmConsumesOnlyNamedType(t *testing.T) {
	ts := newSecurityServer(t, false)
	ctx := context.Background()
	user, err := ts.srv.accounts.CreateLocalUser(ctx, auth.CreateUserInput{
		Username: "unsub-confirm", Email: "unsub-confirm@example.com", Password: "Sup3rSecret!", Role: store.RoleUser,
	})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	prefs := store.NewEmailPrefStore(ts.db)
	// Another optional type is turned on explicitly, to verify the confirm disables only the named one.
	if err := prefs.SetChoices(ctx, user.ID, map[string]bool{string(mail.TypeDeckShared): true}, time.Now().UTC()); err != nil {
		t.Fatalf("SetChoices: %v", err)
	}
	token, err := ts.srv.tokens.Issue(ctx, user.ID, store.ActionTokenUnsubscribe, string(mail.TypeReviewReminder), mail.UnsubscribeTTL)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// The pre-session double-submit cookie and mirrored token are set by GET /unsubscribe.
	cookie, headers := preSessionPair(t, ts.srv, "/unsubscribe")
	rec := postJSON(ts.srv, "/api/v1/unsubscribe", map[string]string{"token": token}, []*http.Cookie{cookie}, headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/unsubscribe = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if !strings.Contains(rec.Body.String(), `"type":"review_reminder"`) {
		t.Errorf("confirm did not return the named type: %s", snippet(rec.Body.String()))
	}

	choices, err := prefs.Choices(ctx, user.ID)
	if err != nil {
		t.Fatalf("Choices: %v", err)
	}
	if on, present := choices[string(mail.TypeReviewReminder)]; !present || on {
		t.Errorf("review_reminder = %v (present=%v), want off", on, present)
	}
	if !choices[string(mail.TypeDeckShared)] {
		t.Errorf("deck_shared was touched by the confirm: %v", choices)
	}
	n, err := store.NewAuditStore(ts.db).CountByAction(ctx, store.ActionUserEmailUnsubscribe)
	if err != nil {
		t.Fatalf("CountByAction: %v", err)
	}
	if n != 1 {
		t.Errorf("audit rows for %s = %d, want 1", store.ActionUserEmailUnsubscribe, n)
	}
}

// TestUnsubscribeJSONConfirmRequiresPreSessionCSRF asserts a confirm request without the double-submit mirrored token is refused,
// without consuming the token or writing a preference.
func TestUnsubscribeJSONConfirmRequiresPreSessionCSRF(t *testing.T) {
	ts := newSecurityServer(t, false)
	ctx := context.Background()
	user, err := ts.srv.accounts.CreateLocalUser(ctx, auth.CreateUserInput{
		Username: "unsub-csrf", Email: "unsub-csrf@example.com", Password: "Sup3rSecret!", Role: store.RoleUser,
	})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	token, err := ts.srv.tokens.Issue(ctx, user.ID, store.ActionTokenUnsubscribe, string(mail.TypeInvite), mail.UnsubscribeTTL)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	cookie, _ := preSessionPair(t, ts.srv, "/unsubscribe")
	rec := postJSON(ts.srv, "/api/v1/unsubscribe", map[string]string{"token": token}, []*http.Cookie{cookie}, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("confirm without the mirror token = %d, want 403 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if choices, _ := store.NewEmailPrefStore(ts.db).Choices(ctx, user.ID); len(choices) != 0 {
		t.Fatalf("a CSRF-rejected confirm consumed the token or wrote a preference: %v", choices)
	}
}

// TestUnsubscribeMachineEndpointDoesNotRenderTemplate asserts the RFC 8058 One-Click endpoint returns only a status code and
// a minimal JSON body, never rendering an SSR template.
func TestUnsubscribeMachineEndpointDoesNotRenderTemplate(t *testing.T) {
	ts := newSecurityServer(t, false)
	ctx := context.Background()
	user, err := ts.srv.accounts.CreateLocalUser(ctx, auth.CreateUserInput{
		Username: "unsub-machine", Email: "unsub-machine@example.com", Password: "Sup3rSecret!", Role: store.RoleUser,
	})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	token, err := ts.srv.tokens.Issue(ctx, user.ID, store.ActionTokenUnsubscribe, string(mail.TypeInvite), mail.UnsubscribeTTL)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	rec := oneClickPost(t, ts.srv, token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("one-click status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	body := rec.Body.String()
	if strings.Contains(body, "<html") || strings.Contains(body, "<!DOCTYPE") {
		t.Errorf("one-click endpoint rendered a template: %s", snippet(body))
	}
	if !strings.Contains(body, `"unsubscribed":true`) {
		t.Errorf("one-click endpoint body = %s, want a minimal JSON result", snippet(body))
	}
}
