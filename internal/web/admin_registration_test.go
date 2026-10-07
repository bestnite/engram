package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// 管理面板注册与邀请（M6-3）的 HTTP 级验收。
// 关键点：策略改动写库后，下一次注册请求立即按新策略判定（同进程、不重启）。

// TestAdminRegistrationPolicyTakesEffectWithoutRestart 是 M6-3 的核心验收。
func TestAdminRegistrationPolicyTakesEffectWithoutRestart(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	srv.invites = store.NewInviteStore(db)

	// 缺省策略是 closed：自助注册被拒。
	if rec := postForm(t, srv, "/register", url.Values{
		"username": {"a1"}, "email": {"a1@example.com"}, "password": {"Sup3rSecret!"},
	}, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("register under default policy = %d, want 403 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	// 管理员改成 open：不重启，下一次注册立即放行。
	save := postForm(t, srv, "/admin/registration", url.Values{
		"csrf_token": {csrf}, "registration_policy": {"open"}, "registration_email_domains": {""},
	}, cookies)
	if save.Code != http.StatusSeeOther || !strings.Contains(save.Header().Get("Location"), "notice=saved") {
		t.Fatalf("POST /admin/registration = %d location %q, want 303 notice=saved", save.Code, save.Header().Get("Location"))
	}
	if rec := postForm(t, srv, "/register", url.Values{
		"username": {"a2"}, "email": {"a2@example.org"}, "password": {"Sup3rSecret!"},
	}, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("register after switch to open = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	// 改成 open + 白名单：非白名单域名立即被拒。
	if rec := postForm(t, srv, "/admin/registration", url.Values{
		"csrf_token": {csrf}, "registration_policy": {"open"}, "registration_email_domains": {"example.com"},
	}, cookies); rec.Code != http.StatusSeeOther {
		t.Fatalf("POST allowlist = %d, want 303", rec.Code)
	}
	denied := postForm(t, srv, "/register", url.Values{
		"username": {"a3"}, "email": {"a3@example.net"}, "password": {"Sup3rSecret!"},
	}, nil)
	if denied.Code != http.StatusForbidden || !strings.Contains(denied.Body.String(), "该邮箱域名不在允许注册的范围内") {
		t.Fatalf("allowlist denial = %d body %s, want 403 with localized notice", denied.Code, snippet(denied.Body.String()))
	}

	// 改回 closed：立即生效。
	if rec := postForm(t, srv, "/admin/registration", url.Values{
		"csrf_token": {csrf}, "registration_policy": {"closed"}, "registration_email_domains": {""},
	}, cookies); rec.Code != http.StatusSeeOther {
		t.Fatalf("POST closed = %d, want 303", rec.Code)
	}
	if rec := postForm(t, srv, "/register", url.Values{
		"username": {"a4"}, "email": {"a4@example.com"}, "password": {"Sup3rSecret!"},
	}, nil); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "自助注册已关闭") {
		t.Fatalf("register after switch to closed = %d, want 403 closed notice", rec.Code)
	}

	// 每次改动都留审计。
	n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionSettingUpdate)
	if err != nil {
		t.Fatalf("count setting.update audits: %v", err)
	}
	if n != 3 {
		t.Errorf("setting.update audit rows = %d, want 3", n)
	}
}

// TestAdminInviteCreateRevokeAndUsage 覆盖邀请创建 / 使用 / 撤销与用量展示。
func TestAdminInviteCreateRevokeAndUsage(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	// GET /admin/registration 已切到 SPA 应用壳；禁用 SPA 以覆盖 SSR 邀请列表的渲染。
	srv.spa = nil
	srv.invites = store.NewInviteStore(db)

	// 创建一条邀请（7 天有效）。
	created := postForm(t, srv, "/admin/invites", url.Values{
		"csrf_token": {csrf}, "role": {"user"}, "expires_days": {"7"},
	}, cookies)
	if created.Code != http.StatusSeeOther || !strings.Contains(created.Header().Get("Location"), "notice=invite_created") {
		t.Fatalf("POST /admin/invites = %d location %q, want 303 invite_created", created.Code, created.Header().Get("Location"))
	}
	invites, err := srv.invites.List(context.Background())
	if err != nil || len(invites) != 1 {
		t.Fatalf("List() = %d invites err %v, want 1", len(invites), err)
	}
	token := invites[0].Token

	// 列表页展示 token 与「可用」。
	page := getWithCookies(t, srv, "/admin/registration", cookies)
	if page.Code != http.StatusOK {
		t.Fatalf("GET /admin/registration = %d, want 200 (body %s)", page.Code, snippet(page.Body.String()))
	}
	if !strings.Contains(page.Body.String(), token) || !strings.Contains(page.Body.String(), "可用") {
		t.Errorf("registration page does not show the active invite; body = %s", snippet(page.Body.String()))
	}

	// 用邀请注册：放行一次。
	reg := postForm(t, srv, "/register", url.Values{
		"username": {"invitee"}, "email": {"invitee@example.com"}, "password": {"Sup3rSecret!"}, "invite": {token},
	}, nil)
	if reg.Code != http.StatusSeeOther {
		t.Fatalf("register with invite = %d, want 303 (body %s)", reg.Code, snippet(reg.Body.String()))
	}

	// 列表页展示「已使用」与使用者用户名。
	after := getWithCookies(t, srv, "/admin/registration", cookies)
	if !strings.Contains(after.Body.String(), "已使用") || !strings.Contains(after.Body.String(), "invitee") {
		t.Errorf("registration page does not show invite usage; body = %s", snippet(after.Body.String()))
	}

	// 新建第二条并撤销：token 立即与「不存在」同路被拒。
	if rec := postForm(t, srv, "/admin/invites", url.Values{
		"csrf_token": {csrf}, "role": {"user"},
	}, cookies); rec.Code != http.StatusSeeOther {
		t.Fatalf("create second invite = %d, want 303", rec.Code)
	}
	list, _ := srv.invites.List(context.Background())
	var revokeID uint64
	var revokedToken string
	for _, inv := range list {
		if inv.UsedAt == nil {
			revokeID, revokedToken = inv.ID, inv.Token
		}
	}
	if revokeID == 0 {
		t.Fatal("no active invite to revoke")
	}
	rev := postForm(t, srv, "/admin/invites/"+u64str(revokeID)+"/revoke", url.Values{"csrf_token": {csrf}}, cookies)
	if rev.Code != http.StatusSeeOther || !strings.Contains(rev.Header().Get("Location"), "notice=invite_revoked") {
		t.Fatalf("POST revoke = %d location %q, want 303 invite_revoked", rev.Code, rev.Header().Get("Location"))
	}
	if _, err := srv.invites.ByToken(context.Background(), revokedToken); err != store.ErrInviteNotFound {
		t.Errorf("ByToken(revoked) err = %v, want ErrInviteNotFound", err)
	}
	denied := postForm(t, srv, "/register", url.Values{
		"username": {"late"}, "email": {"late@example.com"}, "password": {"Sup3rSecret!"}, "invite": {revokedToken},
	}, nil)
	if denied.Code != http.StatusForbidden {
		t.Errorf("register with revoked invite = %d, want 403", denied.Code)
	}

	// 审计留痕：创建两条 + 撤销一条。
	audit := store.NewAuditStore(db)
	if n, _ := audit.CountByAction(context.Background(), store.ActionInviteCreate); n != 2 {
		t.Errorf("invite.create audits = %d, want 2", n)
	}
	if n, _ := audit.CountByAction(context.Background(), store.ActionInviteRevoke); n != 1 {
		t.Errorf("invite.revoke audits = %d, want 1", n)
	}
}

// TestAdminInvitesRejectInvalidInput 覆盖非法策略、非法角色与非法过期天数不落库。
func TestAdminInvitesRejectInvalidInput(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	srv.invites = store.NewInviteStore(db)

	bad := postForm(t, srv, "/admin/registration", url.Values{
		"csrf_token": {csrf}, "registration_policy": {"allowlist"},
	}, cookies)
	if bad.Code != http.StatusSeeOther || !strings.Contains(bad.Header().Get("Location"), "notice=invalid_policy") {
		t.Errorf("invalid policy = %d location %q, want 303 invalid_policy", bad.Code, bad.Header().Get("Location"))
	}
	settings, err := store.LoadSettings(context.Background(), db)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if _, ok := settings[auth.SettingKeyRegistrationPolicy]; ok {
		t.Error("invalid policy was persisted")
	}

	badInvite := postForm(t, srv, "/admin/invites", url.Values{
		"csrf_token": {csrf}, "role": {"superuser"}, "expires_days": {"abc"},
	}, cookies)
	if badInvite.Code != http.StatusSeeOther || !strings.Contains(badInvite.Header().Get("Location"), "notice=invite_create_failed") {
		t.Errorf("invalid invite = %d location %q, want 303 invite_create_failed", badInvite.Code, badInvite.Header().Get("Location"))
	}
	if n, _ := srv.invites.List(context.Background()); len(n) != 0 {
		t.Errorf("invalid invite was persisted: %d rows", len(n))
	}
}
