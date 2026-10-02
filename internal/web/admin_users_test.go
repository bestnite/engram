package web

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"gorm.io/gorm"

	"example.com/engram/internal/auth"
	"example.com/engram/internal/store"
)

// M6-2 用户管理的验收测试：每个动作一条，重点覆盖会话作废与最后管理员保护。

// createRoleUser 直接建一个指定角色的本地用户（管理员用例需要造第二个管理员）。
func createRoleUser(t *testing.T, db *gorm.DB, username, role string) uint64 {
	t.Helper()
	accounts, err := auth.NewAccountService(store.NewUserStore(db), store.NewSessionStore(db),
		auth.NewPasswordHasher(auth.Params{Memory: 8 * 1024, Time: 1, Threads: 1, SaltLength: 16, KeyLength: 32}))
	if err != nil {
		t.Fatalf("NewAccountService() error = %v", err)
	}
	u, err := accounts.CreateLocalUser(context.Background(), auth.CreateUserInput{
		Username: username, Email: username + "@example.com", Password: "Sup3rSecret!", Role: role,
	})
	if err != nil {
		t.Fatalf("CreateLocalUser(%q) error = %v", username, err)
	}
	return u.ID
}

// adminPost 以管理员身份提交一个带 CSRF 的表单。
func adminPost(t *testing.T, srv *Server, path string, values url.Values, cookies []*http.Cookie, csrf string) *http.Response {
	t.Helper()
	values.Set("csrf_token", csrf)
	rec := postForm(t, srv, path, values, cookies)
	return rec.Result()
}

// assertSessionsRevoked 断言某用户的会话全部已作废（revoked_at 非空）。
func assertSessionsRevoked(t *testing.T, db *gorm.DB, userID uint64) {
	t.Helper()
	var total, revoked int64
	if err := db.Model(&store.Session{}).Where("user_id = ?", userID).Count(&total).Error; err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if err := db.Model(&store.Session{}).Where("user_id = ? AND revoked_at IS NOT NULL", userID).Count(&revoked).Error; err != nil {
		t.Fatalf("count revoked sessions: %v", err)
	}
	if total == 0 || revoked != total {
		t.Fatalf("sessions for user %d: %d/%d revoked, want all revoked", userID, revoked, total)
	}
}

func auditCount(t *testing.T, db *gorm.DB, action string) int64 {
	t.Helper()
	n, err := store.NewAuditStore(db).CountByAction(context.Background(), action)
	if err != nil {
		t.Fatalf("count audit %q: %v", action, err)
	}
	return n
}

// TestAdminUsersListSearchAndUsageCounts 覆盖列表、搜索与每用户用量计数。
func TestAdminUsersListSearchAndUsageCounts(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	aliceID, _, _ := createUserAndLogin(t, srv, db, "alice")
	deck := seedDeck(t, db, aliceID, "Alice deck")
	seedBasic(t, db, deck.ID, "front", "back")
	_ = ownerID

	body := getWithCookies(t, srv, "/admin/users", cookies).Body.String()
	if !strings.Contains(body, "alice") || !strings.Contains(body, "owner@example.com") {
		t.Errorf("user list does not render both users:\n%s", snippet(body))
	}
	// 用量计数：alice 有 1 个卡组、1 张卡片。
	if !strings.Contains(body, ">1</td>") {
		t.Errorf("usage counts (decks/cards) not rendered:\n%s", snippet(body))
	}

	filtered := getWithCookies(t, srv, "/admin/users?q=alice", cookies).Body.String()
	if !strings.Contains(filtered, "alice") {
		t.Errorf("search did not return the matching user")
	}
	if strings.Contains(filtered, "owner@example.com") {
		t.Errorf("search returned a non-matching user")
	}
}

// TestAdminUsersCreate 覆盖创建用户与重复用户名的失败分支。
func TestAdminUsersCreate(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	resp := adminPost(t, srv, "/admin/users", url.Values{
		"username": {"bob"}, "email": {"bob@example.com"}, "password": {"Sup3rSecret!"}, "role": {store.RoleUser},
	}, cookies, csrf)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create user status = %d, want 303", resp.StatusCode)
	}
	if _, err := store.NewUserStore(db).ByUsername(context.Background(), "bob"); err != nil {
		t.Fatalf("created user not found: %v", err)
	}
	if n := auditCount(t, db, store.ActionUserCreate); n < 1 {
		t.Errorf("user.create audit rows = %d, want >=1", n)
	}
	// 重名：应重定向到 create_failed 而不是 500。
	resp = adminPost(t, srv, "/admin/users", url.Values{
		"username": {"bob"}, "email": {"bob2@example.com"}, "password": {"Sup3rSecret!"}, "role": {store.RoleUser},
	}, cookies, csrf)
	if resp.StatusCode != http.StatusSeeOther || !strings.Contains(resp.Header.Get("Location"), "create_failed") {
		t.Errorf("duplicate create = %d %q, want 303 create_failed", resp.StatusCode, resp.Header.Get("Location"))
	}
}

// TestAdminUsersDisableInvalidatesSessions 覆盖禁用→旧会话立即失效→再启用。
func TestAdminUsersDisableInvalidatesSessions(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	aliceID, aliceCookies, _ := createUserAndLogin(t, srv, db, "alice")

	resp := adminPost(t, srv, "/admin/users/"+u64str(aliceID)+"/status",
		url.Values{"action": {"disable"}}, cookies, csrf)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("disable status = %d, want 303", resp.StatusCode)
	}
	u, err := store.NewUserStore(db).ByID(context.Background(), aliceID)
	if err != nil || u.Status != store.StatusDisabled {
		t.Fatalf("after disable user = (%+v, %v), want disabled", u, err)
	}
	assertSessionsRevoked(t, db, aliceID)
	// 旧 cookie 不能再进管理面板（会被守卫视为未登录）。
	if rec := getWithCookies(t, srv, "/admin/users", aliceCookies); rec.Code != http.StatusSeeOther {
		t.Errorf("disabled user's old session = %d, want 303 to login", rec.Code)
	}
	if n := auditCount(t, db, store.ActionUserDisable); n != 1 {
		t.Errorf("user.disable audit rows = %d, want 1", n)
	}

	resp = adminPost(t, srv, "/admin/users/"+u64str(aliceID)+"/status",
		url.Values{"action": {"enable"}}, cookies, csrf)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("enable status = %d, want 303", resp.StatusCode)
	}
	u, _ = store.NewUserStore(db).ByID(context.Background(), aliceID)
	if u.Status != store.StatusActive {
		t.Errorf("after enable status = %q, want active", u.Status)
	}
	if n := auditCount(t, db, store.ActionUserEnable); n != 1 {
		t.Errorf("user.enable audit rows = %d, want 1", n)
	}
}

// TestAdminUsersCannotDisableOrDeleteSelf 覆盖「不能把自己删了或禁了」。
func TestAdminUsersCannotDisableOrDeleteSelf(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)

	resp := adminPost(t, srv, "/admin/users/"+u64str(ownerID)+"/status",
		url.Values{"action": {"disable"}}, cookies, csrf)
	if !strings.Contains(resp.Header.Get("Location"), "self_forbidden") {
		t.Errorf("self disable location = %q, want self_forbidden", resp.Header.Get("Location"))
	}
	resp = adminPost(t, srv, "/admin/users/"+u64str(ownerID)+"/delete",
		url.Values{"confirm": {"1"}}, cookies, csrf)
	if !strings.Contains(resp.Header.Get("Location"), "self_forbidden") {
		t.Errorf("self delete location = %q, want self_forbidden", resp.Header.Get("Location"))
	}
	owner, err := store.NewUserStore(db).ByID(context.Background(), ownerID)
	if err != nil || owner.Status != store.StatusActive {
		t.Fatalf("owner was affected by self-protection test: %+v, %v", owner, err)
	}
}

// TestAdminUsersRoleChangeAndLastAdminProtected 覆盖改角色，以及「不能降掉最后一个管理员」。
func TestAdminUsersRoleChangeAndLastAdminProtected(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	admin2 := createRoleUser(t, db, "admin2", store.RoleAdmin)

	// 危险动作缺确认 → 拒绝且不改动。
	resp := adminPost(t, srv, "/admin/users/"+u64str(admin2)+"/role",
		url.Values{"role": {store.RoleUser}}, cookies, csrf)
	if !strings.Contains(resp.Header.Get("Location"), "confirm_required") {
		t.Fatalf("missing confirm location = %q, want confirm_required", resp.Header.Get("Location"))
	}
	if u, _ := store.NewUserStore(db).ByID(context.Background(), admin2); u.Role != store.RoleAdmin {
		t.Fatalf("role changed without confirmation")
	}

	// 带确认降级 admin2 → 现在 owner 是唯一管理员。
	resp = adminPost(t, srv, "/admin/users/"+u64str(admin2)+"/role",
		url.Values{"role": {store.RoleUser}, "confirm": {"1"}}, cookies, csrf)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("demote admin2 status = %d, want 303", resp.StatusCode)
	}
	if u, _ := store.NewUserStore(db).ByID(context.Background(), admin2); u.Role != store.RoleUser {
		t.Fatalf("admin2 role = %q, want user", u.Role)
	}

	// 再降 owner（唯一管理员）→ 必须被拒绝。
	resp = adminPost(t, srv, "/admin/users/"+u64str(ownerID)+"/role",
		url.Values{"role": {store.RoleUser}, "confirm": {"1"}}, cookies, csrf)
	if !strings.Contains(resp.Header.Get("Location"), "last_admin") {
		t.Fatalf("last admin demote location = %q, want last_admin", resp.Header.Get("Location"))
	}
	if u, _ := store.NewUserStore(db).ByID(context.Background(), ownerID); u.Role != store.RoleAdmin {
		t.Fatalf("the last admin was demoted")
	}
	if n := auditCount(t, db, store.ActionUserRoleChange); n != 1 {
		t.Errorf("user.role_change audit rows = %d, want 1 (only the successful change)", n)
	}
}

var tempPasswordRe = regexp.MustCompile(`<code class="[^"]*">([^<]+)</code>`)

// TestAdminUsersResetPasswordShowsOnceAndAudits 覆盖重置密码：临时口令展示一次、作废会话、审计不含明文。
func TestAdminUsersResetPasswordShowsOnceAndAudits(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	aliceID, _, _ := createUserAndLogin(t, srv, db, "alice")

	rec := postForm(t, srv, "/admin/users/"+u64str(aliceID)+"/password",
		url.Values{"confirm": {"1"}, "csrf_token": {csrf}}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset password status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	body := rec.Body.String()
	if !strings.Contains(body, "临时口令") {
		t.Fatalf("reset page does not show the temporary password label:\n%s", snippet(body))
	}
	m := tempPasswordRe.FindStringSubmatch(body)
	if len(m) != 2 || len(m[1]) < auth.MinPasswordLength {
		t.Fatalf("could not extract a valid temp password from body")
	}
	temp := m[1]
	assertSessionsRevoked(t, db, aliceID)
	if n := auditCount(t, db, store.ActionUserPasswordReset); n != 1 {
		t.Errorf("user.password_reset audit rows = %d, want 1", n)
	}
	// 审计明细绝不能包含临时口令明文。
	var rows []store.AuditLog
	if err := db.Where("action = ?", store.ActionUserPasswordReset).Find(&rows).Error; err != nil {
		t.Fatalf("load audit rows: %v", err)
	}
	for _, r := range rows {
		if r.DetailJSON != nil && strings.Contains(*r.DetailJSON, temp) {
			t.Fatalf("audit log leaked the temporary password")
		}
	}
}

// TestAdminUsersForceLogout 覆盖强制登出：作废会话且不改密码/状态。
func TestAdminUsersForceLogout(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	aliceID, aliceCookies, _ := createUserAndLogin(t, srv, db, "alice")

	resp := adminPost(t, srv, "/admin/users/"+u64str(aliceID)+"/logout", url.Values{}, cookies, csrf)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("force logout status = %d, want 303", resp.StatusCode)
	}
	assertSessionsRevoked(t, db, aliceID)
	if rec := getWithCookies(t, srv, "/", aliceCookies); rec.Code == http.StatusInternalServerError {
		t.Errorf("home page errored after force logout")
	}
	if n := auditCount(t, db, store.ActionUserForceLogout); n != 1 {
		t.Errorf("user.force_logout audit rows = %d, want 1", n)
	}
}

// TestAdminUsersDeleteInvalidatesSessions 覆盖删除：会话作废、用户与其数据移除、审计留痕。
func TestAdminUsersDeleteInvalidatesSessions(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	aliceID, aliceCookies, _ := createUserAndLogin(t, srv, db, "alice")
	deck := seedDeck(t, db, aliceID, "Alice deck")
	seedBasic(t, db, deck.ID, "front", "back")

	resp := adminPost(t, srv, "/admin/users/"+u64str(aliceID)+"/delete",
		url.Values{"confirm": {"1"}}, cookies, csrf)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete status = %d, want 303", resp.StatusCode)
	}
	if _, err := store.NewUserStore(db).ByID(context.Background(), aliceID); err == nil {
		t.Fatalf("user still exists after delete")
	}
	// 该用户拥有的卡组必须一并清理。
	var decks int64
	db.Model(&store.Deck{}).Where("owner_user_id = ?", aliceID).Count(&decks)
	if decks != 0 {
		t.Errorf("deleted user still owns %d decks", decks)
	}
	// 旧 cookie 不能再进面板。
	if rec := getWithCookies(t, srv, "/admin/users", aliceCookies); rec.Code != http.StatusSeeOther {
		t.Errorf("deleted user's old session = %d, want 303", rec.Code)
	}
	if n := auditCount(t, db, store.ActionUserDelete); n != 1 {
		t.Errorf("user.delete audit rows = %d, want 1", n)
	}
}

// TestAdminUsersRejectsAnonymous 覆盖未登录访问用户管理页被重定向到登录页。
func TestAdminUsersRejectsAnonymous(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	if rec := getWithCookies(t, srv, "/admin/users", nil); rec.Code != http.StatusSeeOther {
		t.Errorf("anonymous GET /admin/users = %d, want 303 to login", rec.Code)
	}
}
