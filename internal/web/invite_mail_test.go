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

// 本文件是 HTTP 级验收：邀请码可由管理员直接寄到被邀请人邮箱（B 类）。
//
// 覆盖四条验收线：
//  1. 为某邮箱建的邀请能投递并被接受；
//  2. 被撤销 / 过期的邀请链接被拒；
//  3. 手工拷链接的原有路径仍然能用；
//  4. SMTP 未配置时创建仍成功且如实回显 mail_notice（绝不静默）。
//
// 管理端邀请的创建/撤销与注册全部走 SPA 的同源 JSON 端点（SSR 表单端点已删除）。
// 投递用进程内替身（recordingMailSender）完成，不依赖外网或真实 SMTP。

// recordingMailSender 是 mail.Sender 的替身：只记录被投递的邮件，不碰网络。
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

// configureSMTP 写入生效的 SMTP 设置；host 与 from 齐备即视为已配置。
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

// startInviteMail 构造并启动一个已配置 SMTP 的 outbox，挂到测试服务上；返回替身。
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
	srv.mail = ob
	return sender
}

// waitForMail 轮询等待替身收到至少 n 封邮件（worker 异步投递）。
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

// inviteCreateResponse 是创建邀请的 JSON 回包：新邀请 + 发信结果码。
type inviteCreateResponse struct {
	Invite struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	} `json:"invite"`
	MailNotice string `json:"mail_notice"`
}

// createInviteJSON 通过管理 JSON 端点创建一条邀请，返回响应与解出的回包。
func createInviteJSON(t *testing.T, srv *Server, cookies []*http.Cookie, csrf string, body map[string]any) (*httptest.ResponseRecorder, inviteCreateResponse) {
	t.Helper()
	rec := postJSONWithSession(srv, "/api/v1/admin/invites", body, cookies, csrf)
	var resp inviteCreateResponse
	if rec.Code == http.StatusCreated {
		decodeJSON(t, rec, &resp)
	}
	return rec, resp
}

// registerWithInviteJSON 用邀请 token 走 SPA 的 JSON 注册（会话前双提交 CSRF）。
func registerWithInviteJSON(t *testing.T, srv *Server, username, email, invite string) *httptest.ResponseRecorder {
	t.Helper()
	cookie, headers := preSessionPair(t, srv, "/register")
	return postJSON(srv, "/api/v1/auth/register", map[string]string{
		"username": username, "email": email,
		"password": "Sup3rSecret!", "invite": invite,
	}, []*http.Cookie{cookie}, headers)
}

// TestInviteEmailDeliveredAndAccepted 是核心验收：
// 为某邮箱建的邀请被投递（邮件里带可用的接受链接），并且该链接能被接受。
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

	// 用邮件里的链接接受邀请：放行一次。
	reg := registerWithInviteJSON(t, srv, "invitee", "invitee@example.com", token)
	if reg.Code != http.StatusOK {
		t.Fatalf("register with mailed invite = %d, want 200 (body %s)", reg.Code, snippet(reg.Body.String()))
	}
	after, _ := srv.invites.ByToken(context.Background(), token)
	if after.UsedAt == nil {
		t.Error("invite was not marked used after acceptance")
	}
}

// TestInviteLinkCopiedManuallyStillWorks 证明加邮件没有破坏原有手工拷链接路径。
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

// TestRevokedAndExpiredInviteRefused 覆盖反面用例：被撤销 / 过期的链接被拒。
func TestRevokedAndExpiredInviteRefused(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	srv.invites = store.NewInviteStore(db)

	// 撤销：创建 → 撤销 → 用原 token 注册被拒。
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

	// 过期：直接写入一条已过期的邀请 → 注册被拒。
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

// TestInviteMailUnconfiguredStillCreatesInvite 覆盖 SMTP 未配置：请求寄信时回包如实说明
// mail_notice=invite_mail_unconfigured，但邀请仍然创建成功（绝不静默，也不因邮件未配置而阻断创建）。
func TestInviteMailUnconfiguredStillCreatesInvite(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	srv.invites = store.NewInviteStore(db)
	srv.mail = nil // 未装配邮件

	created, resp := createInviteJSON(t, srv, cookies, csrf, map[string]any{
		"role": "user", "email": "invitee@example.com", "send_email": true,
	})
	if created.Code != http.StatusCreated || resp.MailNotice != noticeInviteMailUnconfigured {
		t.Fatalf("POST with send_email while unconfigured = %d mail_notice %q, want 201 %q",
			created.Code, resp.MailNotice, noticeInviteMailUnconfigured)
	}
	// 邀请本身仍然创建成功。
	if n, _ := srv.invites.List(context.Background()); len(n) != 1 {
		t.Errorf("invites = %d, want 1 (invite must still be created)", len(n))
	}
}

// TestInviteMailHonorsRecipientPreference 证明 B 类可关：收件人若是本站用户并关掉了该类型，
// 就不寄信，但邀请仍然创建成功、链接仍可手拷。
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
