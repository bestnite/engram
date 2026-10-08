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

// 本文件是（RFC 8058 一键退订）的 HTTP 级验收。覆盖三条验收：
//  1. 一键 POST 只关掉令牌指名的那个类型；
//  2. 令牌不能被重放到另一个类型或另一个用户；
//  3. A 类邮件不带退订头（A 类的正例见 internal/mail/unsubscribe_test.go）。
//
// 一键 POST 一律**不带 cookie**（不登录），以证明端点确实在免登录下可用。
// 移除 SSR 页面后，退订由三条传输承担：GET /unsubscribe 返回应用壳并下发双提交 cookie，
// POST /unsubscribe 是 RFC 8058 One-Click 的机器端点（只回状态码，不渲染模板），
// /api/v1/unsubscribe 是 SPA 的同源 JSON 传输（GET 读不消费、POST 消费）。

// tokenFromUnsubscribeHeader 从 List-Unsubscribe 头（形如 <URL>）里取出令牌。
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

// waitForMailOfType 等待替身收到指定类型的邮件（worker 异步投递；夹具登录会先入队其它类型）。
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

// oneClickPost 发一个 RFC 8058 One-Click POST（无 cookie、无 CSRF），可附带额外表单字段，
// 用来验证「调用方塞进来的类型参数被忽略」。
func oneClickPost(t *testing.T, srv *Server, token string, extra url.Values) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"List-Unsubscribe": {"One-Click"}}
	for k, v := range extra {
		form[k] = v
	}
	return postForm(t, srv, "/unsubscribe?token="+url.QueryEscape(token), form, nil)
}

// TestUnsubscribeOneClickDisablesOnlyThatType 是验收①的主证据，并顺带证明：
//   - B 类（可选类型）邮件带上了两个 RFC 8058 头，且头里的令牌可用；
//   - 一键 POST 在**未登录**（无 cookie）下返回 200；
//   - 只关掉令牌指名的 invite，用户其它可选类型的开关不受影响；
//   - 即便调用方在表单里塞一个 type=deck_shared，也不会被采纳。
func TestUnsubscribeOneClickDisablesOnlyThatType(t *testing.T) {
	ts := newSecurityServer(t, true)
	ts.srv.invites = store.NewInviteStore(ts.db)
	sender := startInviteMail(t, ts.srv, ts.db)
	ctx := context.Background()

	// 收件人必须是本站用户：退订改的是用户偏好，非用户没有可关的开关。
	invitee, err := ts.srv.accounts.CreateLocalUser(ctx, auth.CreateUserInput{
		Username: "invitee", Email: "invitee@example.com", Password: "Sup3rSecret!", Role: store.RoleUser,
	})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	prefs := store.NewEmailPrefStore(ts.db)
	// 打开另一个可选类型（C 类默认关，先显式打开），以便验证「只关掉那一个」。
	if err := prefs.SetChoices(ctx, invitee.ID, map[string]bool{
		string(mail.TypeReviewReminder): true,
	}, time.Now().UTC()); err != nil {
		t.Fatalf("SetChoices: %v", err)
	}

	// 管理员把邀请寄给这个已注册邮箱（SSR 的 /admin/invites 表单端点已删除，走 JSON 端点）。
	created := postJSON(ts.srv, "/api/v1/admin/invites", map[string]any{
		"role": "user", "email": "invitee@example.com", "send_email": true,
	}, ts.cookies, map[string]string{auth.CSRFHeaderName: ts.csrf})
	if created.Code != http.StatusCreated {
		t.Fatalf("POST /api/v1/admin/invites = %d, want 201 (body %s)",
			created.Code, snippet(created.Body.String()))
	}
	// 发信成功时 mail_notice 回带稳定结果码 invite_mail_queued（非空表示已入队）。
	if !strings.Contains(created.Body.String(), `"mail_notice":"`+noticeInviteMailQueued+`"`) {
		t.Fatalf("invite create did not queue the mail: %s", snippet(created.Body.String()))
	}

	// 夹具登录本身会入队一封 new_device_login；只取邀请那一封。
	msg := waitForMailOfType(t, sender, string(mail.TypeInvite))
	unsub := msg.Headers[mail.HeaderListUnsubscribe]
	if unsub == "" {
		t.Fatalf("optional B-class mail carries no %s header; headers = %v", mail.HeaderListUnsubscribe, msg.Headers)
	}
	if got := msg.Headers[mail.HeaderListUnsubscribePost]; got != mail.ListUnsubscribePostValue {
		t.Errorf("%s = %q, want %q", mail.HeaderListUnsubscribePost, got, mail.ListUnsubscribePostValue)
	}
	token := tokenFromUnsubscribeHeader(t, unsub)

	// 一键 POST：不带任何 cookie（未登录），并故意塞一个 type 参数看它是否被采纳。
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

// TestUnsubscribeTokenNotReplayableAcrossTypesOrUsers 是验收②：
//   - 一枚令牌用过后再 POST 被拒（一次性，不可重放）；
//   - 令牌只影响它自己的用户，另一个用户的偏好完全不动。
func TestUnsubscribeTokenNotReplayableAcrossTypesOrUsers(t *testing.T) {
	ts := newSecurityServer(t, false)
	ctx := context.Background()
	prefs := store.NewEmailPrefStore(ts.db)

	// 用户 A：令牌归属者。
	userA, err := ts.srv.accounts.CreateLocalUser(ctx, auth.CreateUserInput{
		Username: "a", Email: "a@example.com", Password: "Sup3rSecret!", Role: store.RoleUser,
	})
	if err != nil {
		t.Fatalf("CreateLocalUser A: %v", err)
	}
	// 用户 B：不应被 A 的令牌影响。
	userB, err := ts.srv.accounts.CreateLocalUser(ctx, auth.CreateUserInput{
		Username: "b", Email: "b@example.com", Password: "Sup3rSecret!", Role: store.RoleUser,
	})
	if err != nil {
		t.Fatalf("CreateLocalUser B: %v", err)
	}

	token, err := ts.srv.tokens.Issue(ctx, userA.ID, store.ActionTokenUnsubscribe, string(mail.TypeInvite), auth.UnsubscribeTTL)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	first := oneClickPost(t, ts.srv, token, nil)
	if first.Code != http.StatusOK {
		t.Fatalf("first one-click status = %d, want 200 (body %s)", first.Code, snippet(first.Body.String()))
	}
	// 重放同一令牌：必须被拒（一次性）。
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

// TestUnsubscribeRefusesClassAToken 是验收③的服务端防线：
// 即便有人拿着一枚「指名 A 类」的令牌（正常流程绝不会签发），端点也拒绝，不写任何偏好。
func TestUnsubscribeRefusesClassAToken(t *testing.T) {
	ts := newSecurityServer(t, false)
	ctx := context.Background()

	token, err := ts.srv.tokens.Issue(ctx, 1, store.ActionTokenUnsubscribe, string(mail.TypePasswordReset), auth.UnsubscribeTTL)
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

// TestUnsubscribePageDoesNotConsumeToken 证明 GET 确认页不消费令牌（防链接预取误退订），
// 且真正退订仍由 POST 完成。
func TestUnsubscribePageDoesNotConsumeToken(t *testing.T) {
	ts := newSecurityServer(t, false)
	ctx := context.Background()
	user, err := ts.srv.accounts.CreateLocalUser(ctx, auth.CreateUserInput{
		Username: "c", Email: "c@example.com", Password: "Sup3rSecret!", Role: store.RoleUser,
	})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	token, err := ts.srv.tokens.Issue(ctx, user.ID, store.ActionTokenUnsubscribe, string(mail.TypeReviewReminder), auth.UnsubscribeTTL)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	for i := 0; i < 2; i++ {
		page := getWithCookies(t, ts.srv, "/unsubscribe?token="+url.QueryEscape(token), nil)
		if page.Code != http.StatusOK {
			t.Fatalf("GET /unsubscribe #%d status = %d, want 200 (body %s)", i+1, page.Code, snippet(page.Body.String()))
		}
		// GET 现在返回 SPA 应用壳（不再渲染 SSR 确认页），并下发会话前双提交 cookie。
		if !strings.Contains(page.Body.String(), `id="app"`) {
			t.Errorf("GET /unsubscribe #%d did not serve the SPA shell: %s", i+1, snippet(page.Body.String()))
		}
		if findCookie(page, auth.CSRFDoubleSubmitCookieName) == nil {
			t.Errorf("GET /unsubscribe #%d did not initialize the double-submit cookie", i+1)
		}
	}
	// GET 不消费：偏好未变，令牌仍可用。
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

// TestUnsubscribeExpiredTokenRejected 证明过期令牌被拒并给出稳定提示。
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

// ── SPA 同源 JSON 传输（读与确认）──────────────────────────────────────────────

// TestUnsubscribeJSONReadDoesNotConsume 断言读端点返回令牌指名的可选类型，但不消费令牌（可反复读）。
func TestUnsubscribeJSONReadDoesNotConsume(t *testing.T) {
	ts := newSecurityServer(t, false)
	ctx := context.Background()
	user, err := ts.srv.accounts.CreateLocalUser(ctx, auth.CreateUserInput{
		Username: "unsub-reader", Email: "unsub-reader@example.com", Password: "Sup3rSecret!", Role: store.RoleUser,
	})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	token, err := ts.srv.tokens.Issue(ctx, user.ID, store.ActionTokenUnsubscribe, string(mail.TypeReviewReminder), auth.UnsubscribeTTL)
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

// TestUnsubscribeJSONConfirmConsumesOnlyNamedType 断言确认端点消费令牌、只关掉指名的可选类型，
// 并落下一条与 SSR 相同的审计行。
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
	// 另一个可选类型显式打开，验证确认只关掉指名的那一个。
	if err := prefs.SetChoices(ctx, user.ID, map[string]bool{string(mail.TypeDeckShared): true}, time.Now().UTC()); err != nil {
		t.Fatalf("SetChoices: %v", err)
	}
	token, err := ts.srv.tokens.Issue(ctx, user.ID, store.ActionTokenUnsubscribe, string(mail.TypeReviewReminder), auth.UnsubscribeTTL)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// 会话前双提交 cookie 与镜像 token 由 GET /unsubscribe 下发。
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

// TestUnsubscribeJSONConfirmRequiresPreSessionCSRF 断言确认请求缺少双提交镜像 token 时被拒，
// 且不消费令牌、不写偏好。
func TestUnsubscribeJSONConfirmRequiresPreSessionCSRF(t *testing.T) {
	ts := newSecurityServer(t, false)
	ctx := context.Background()
	user, err := ts.srv.accounts.CreateLocalUser(ctx, auth.CreateUserInput{
		Username: "unsub-csrf", Email: "unsub-csrf@example.com", Password: "Sup3rSecret!", Role: store.RoleUser,
	})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	token, err := ts.srv.tokens.Issue(ctx, user.ID, store.ActionTokenUnsubscribe, string(mail.TypeInvite), auth.UnsubscribeTTL)
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

// TestUnsubscribeMachineEndpointDoesNotRenderTemplate 断言 RFC 8058 One-Click 端点只回状态码与
// 最小 JSON 体，绝不渲染 SSR 模板。
func TestUnsubscribeMachineEndpointDoesNotRenderTemplate(t *testing.T) {
	ts := newSecurityServer(t, false)
	ctx := context.Background()
	user, err := ts.srv.accounts.CreateLocalUser(ctx, auth.CreateUserInput{
		Username: "unsub-machine", Email: "unsub-machine@example.com", Password: "Sup3rSecret!", Role: store.RoleUser,
	})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	token, err := ts.srv.tokens.Issue(ctx, user.ID, store.ActionTokenUnsubscribe, string(mail.TypeInvite), auth.UnsubscribeTTL)
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
