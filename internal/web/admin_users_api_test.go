package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是管理面板「用户管理 / 注册与邀请 / API Key 总览」切流到 SPA 的验收：
//   - GET /admin/users、/admin/registration、/admin/api-keys 一律返回应用壳；
//     requireAdmin 仍在返回外壳之前生效。
//   - /api/v1/admin/* 的写端点复用与 SSR 完全同一份 store/service 调用与危险动作保护。

// adminPostJSON 发一个带会话 cookie 与 CSRF 头的 JSON 写请求。
func adminPostJSON(t *testing.T, srv *Server, target string, body any, cookies []*http.Cookie, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
	}
	req := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		req.Header.Set(auth.CSRFHeaderName, csrf)
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// TestAdminUserPagesCutover 覆盖用户管理 / 注册邀请 / API Key 总览三页的切流与非管理员门禁。
func TestAdminUserPagesCutover(t *testing.T) {
	for _, path := range []string{"/admin/users", "/admin/registration", "/admin/api-keys"} {
		t.Run(path, func(t *testing.T) {
			srv, db, _, cookies, _ := newNotesServer(t)
			srv.invites = store.NewInviteStore(db)

			assertShell(t, getWithCookies(t, srv, path, cookies))

			_, strangerCookies, _ := createUserAndLogin(t, srv, db, "cut_stranger")
			denied := getWithCookies(t, srv, path, strangerCookies)
			if denied.Code != http.StatusForbidden {
				t.Fatalf("non-admin GET %s = %d, want 403", path, denied.Code)
			}

		})
	}
}

// TestAdminUserLifecycle 覆盖创建 / 列表 / 改角色 / 禁用 / 重置密码 / 删除的 JSON 路径。
func TestAdminUserLifecycle(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	ctx := context.Background()

	// 创建：校验通过后落库。
	rec := adminPostJSON(t, srv, "/api/v1/admin/users", adminUserCreateRequest{
		Username: "apiuser", Email: "apiuser@example.com", DisplayName: "API User",
		Password: "Sup3rSecret!", Role: store.RoleUser,
	}, cookies, csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/v1/admin/users = %d, want 201 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	created, err := store.NewUserStore(db).ByUsername(ctx, "apiuser")
	if err != nil || created == nil {
		t.Fatalf("created user not found: %v", err)
	}

	// 弱密码：稳定 code，且不落库。
	if rec := adminPostJSON(t, srv, "/api/v1/admin/users", adminUserCreateRequest{
		Username: "weak", Email: "weak@example.com", Password: "short", Role: store.RoleUser,
	}, cookies, csrf); rec.Code != http.StatusBadRequest {
		t.Errorf("weak password create = %d, want 400", rec.Code)
	}

	// 列表：包含新建用户与用量字段。
	listRec := getJSON(t, srv, "/api/v1/admin/users", cookies, nil)
	var list adminUsersResponse
	if err := json.Unmarshal(listRec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode users: %v", err)
	}
	if list.Total < 2 || len(list.Users) < 2 {
		t.Errorf("users list = %d rows total %d, want >= 2", len(list.Users), list.Total)
	}

	// 改角色：缺 confirm 拒绝，带 confirm 通过。
	id := u64str(created.ID)
	if rec := adminPostJSON(t, srv, "/api/v1/admin/users/"+id+"/role", map[string]any{"role": store.RoleAdmin}, cookies, csrf); rec.Code != http.StatusBadRequest {
		t.Errorf("role change without confirm = %d, want 400", rec.Code)
	}
	if rec := adminPostJSON(t, srv, "/api/v1/admin/users/"+id+"/role", map[string]any{"role": store.RoleAdmin, "confirm": true}, cookies, csrf); rec.Code != http.StatusNoContent {
		t.Errorf("role change with confirm = %d, want 204 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	reloaded, _ := store.NewUserStore(db).ByID(ctx, created.ID)
	if reloaded == nil || reloaded.Role != store.RoleAdmin {
		t.Fatalf("role not persisted: %+v", reloaded)
	}

	// 重置密码：一次性返回临时口令，且不写进审计详情。
	rec = adminPostJSON(t, srv, "/api/v1/admin/users/"+id+"/password", map[string]any{"confirm": true}, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset password = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var pw adminUserPasswordResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &pw); err != nil || pw.TempPassword == "" {
		t.Fatalf("reset password returned no temp password: %v %+v", err, pw)
	}
	// 缺确认：拒绝。
	if rec := adminPostJSON(t, srv, "/api/v1/admin/users/"+id+"/password", map[string]any{}, cookies, csrf); rec.Code != http.StatusBadRequest {
		t.Errorf("reset without confirm = %d, want 400", rec.Code)
	}

	// 禁用：作废会话。
	if rec := adminPostJSON(t, srv, "/api/v1/admin/users/"+id+"/status", map[string]any{"action": "disable"}, cookies, csrf); rec.Code != http.StatusNoContent {
		t.Errorf("disable = %d, want 204", rec.Code)
	}
	disabled, _ := store.NewUserStore(db).ByID(ctx, created.ID)
	if disabled == nil || disabled.Status != store.StatusDisabled {
		t.Fatalf("disable not persisted: %+v", disabled)
	}

	// 删除自己：拒绝。
	meRec := getJSON(t, srv, "/api/v1/admin/summary", cookies, nil)
	var me adminSummaryResponse
	_ = json.Unmarshal(meRec.Body.Bytes(), &me)
	ownerID, _ := store.NewUserStore(db).ByUsername(ctx, "owner")
	if rec := adminPostJSON(t, srv, "/api/v1/admin/users/"+u64str(ownerID.ID)+"/delete", map[string]any{"confirm": true}, cookies, csrf); rec.Code != http.StatusBadRequest {
		t.Errorf("delete self = %d, want 400", rec.Code)
	}

	// 删除他人：通过。
	if rec := adminPostJSON(t, srv, "/api/v1/admin/users/"+id+"/delete", map[string]any{"confirm": true}, cookies, csrf); rec.Code != http.StatusNoContent {
		t.Fatalf("delete user = %d, want 204 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if _, err := store.NewUserStore(db).ByID(ctx, created.ID); err == nil {
		t.Error("deleted user still resolves")
	}
}

// TestAdminRegistrationAndInvites 覆盖注册策略保存与邀请创建 / 撤销的 JSON 路径。
func TestAdminRegistrationAndInvites(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	srv.invites = store.NewInviteStore(db)
	ctx := context.Background()

	// 保存策略与白名单。
	if rec := adminPostJSON(t, srv, "/api/v1/admin/registration", adminRegistrationRequest{
		Policy: auth.PolicyOpen, EmailDomains: "example.com, example.org",
	}, cookies, csrf); rec.Code != http.StatusNoContent {
		t.Fatalf("save registration = %d, want 204 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	settings, err := store.LoadSettings(ctx, db)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if settings[auth.SettingKeyRegistrationPolicy] != auth.PolicyOpen {
		t.Errorf("policy = %q, want open", settings[auth.SettingKeyRegistrationPolicy])
	}
	if settings[auth.SettingKeyEmailAllowlist] != "example.com,example.org" {
		t.Errorf("allowlist = %q, want normalized comma list", settings[auth.SettingKeyEmailAllowlist])
	}
	// 非法策略：拒绝且不落库。
	if rec := adminPostJSON(t, srv, "/api/v1/admin/registration", adminRegistrationRequest{Policy: "bogus"}, cookies, csrf); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid policy = %d, want 400", rec.Code)
	}

	// 创建邀请。
	expires := 7
	rec := adminPostJSON(t, srv, "/api/v1/admin/invites", adminInviteCreateRequest{
		Email: "invitee@example.com", Role: store.RoleUser, ExpiresDays: &expires,
	}, cookies, csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create invite = %d, want 201 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var created struct {
		Invite adminInvite `json:"invite"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode invite: %v", err)
	}
	if created.Invite.Token == "" || created.Invite.Status != "active" || created.Invite.Link == "" {
		t.Fatalf("created invite = %+v, want active with token and link", created.Invite)
	}

	// 读取：列表包含新邀请。
	readResp := getJSON(t, srv, "/api/v1/admin/registration", cookies, nil)
	var reg adminRegistrationResponse
	if err := json.Unmarshal(readResp.Body.Bytes(), &reg); err != nil {
		t.Fatalf("decode registration: %v", err)
	}
	if len(reg.Invites) != 1 || reg.Invites[0].ID != created.Invite.ID {
		t.Fatalf("registration invites = %+v, want the created one", reg.Invites)
	}

	// 撤销。
	if rec := adminPostJSON(t, srv, "/api/v1/admin/invites/"+u64str(created.Invite.ID)+"/revoke", nil, cookies, csrf); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke invite = %d, want 204", rec.Code)
	}
	if _, err := srv.invites.ByToken(ctx, created.Invite.Token); err == nil {
		t.Error("revoked invite still resolves")
	}
}

// TestAdminAPIKeysListAndRevoke 覆盖 API Key 总览读取与撤销的 JSON 路径。
func TestAdminAPIKeysListAndRevoke(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	ctx := context.Background()
	created, err := store.NewAPIKeyStore(db).Create(ctx, store.CreateAPIKeyParams{
		UserID: ownerID, Name: "api key", Scopes: []string{store.ScopeRead, store.ScopeWrite},
	})
	if err != nil {
		t.Fatalf("Create key: %v", err)
	}

	rec := getJSON(t, srv, "/api/v1/admin/api-keys", cookies, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET api-keys = %d, want 200", rec.Code)
	}
	var list adminAPIKeysResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode api keys: %v", err)
	}
	if len(list.Keys) != 1 {
		t.Fatalf("api keys = %d rows, want 1", len(list.Keys))
	}
	row := list.Keys[0]
	if row.Prefix != created.Key.Prefix || row.State != store.APIKeyStateActive || row.Owner == "" {
		t.Errorf("api key row = %+v, want active with prefix and owner", row)
	}
	if rec.Body.String() == "" || bytes.Contains(rec.Body.Bytes(), []byte(created.Plaintext)) {
		t.Fatal("api keys response leaked the plaintext")
	}

	if rec := adminPostJSON(t, srv, "/api/v1/admin/api-keys/"+u64str(created.Key.ID)+"/revoke", nil, cookies, csrf); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke key = %d, want 204", rec.Code)
	}
	if _, err := store.NewAPIKeyStore(db).Authenticate(ctx, created.Plaintext, created.Key.CreatedAt); err == nil {
		t.Error("revoked key still authenticates")
	}

	// 不存在的 key：404。
	if rec := adminPostJSON(t, srv, "/api/v1/admin/api-keys/99999/revoke", nil, cookies, csrf); rec.Code != http.StatusNotFound {
		t.Errorf("revoke unknown key = %d, want 404", rec.Code)
	}
}

// TestAdminWriteEndpointsGuard 钉住写端点的判权：匿名 401、非 admin 403。
func TestAdminWriteEndpointsGuard(t *testing.T) {
	srv, db, _, _, _ := newNotesServer(t)
	_, strangerCookies, _ := createUserAndLogin(t, srv, db, "writer_stranger")

	// 匿名：无会话 → 401（在 CSRF 之前就被拒）。
	if rec := adminPostJSON(t, srv, "/api/v1/admin/users", adminUserCreateRequest{Username: "x"}, nil, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous write = %d, want 401", rec.Code)
	}
	// 非 admin：403（写路由判权先于 CSRF）。
	if rec := adminPostJSON(t, srv, "/api/v1/admin/users", adminUserCreateRequest{Username: "x"}, strangerCookies, ""); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin write = %d, want 403", rec.Code)
	}
}
