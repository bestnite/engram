package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是管理面板只读页（概览 / 健康 / 审计）切流到 SPA 的验收，外加它们的 JSON 端点：
//   - GET /admin、/admin/health、/admin/audit 一律返回应用壳（SSR 页面层已删除）。
//   - /api/v1/admin/* 的 JSON 端点判权与 requireAdmin 同源：匿名 401、非 admin 403、bearer 403。
//
// 页面迁移不改动授权判定：requireAdmin 仍在返回应用壳之前生效，非管理员永远拿不到外壳。

// getJSON 发一个带 cookie 的 GET，用于断言 JSON 端点的判权与响应形态。
func getJSON(t *testing.T, srv *Server, target string, cookies []*http.Cookie, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// TestAdminReadPagesCutover 覆盖三个只读页的切流、回退与非管理员门禁。
func TestAdminReadPagesCutover(t *testing.T) {
	for _, path := range []string{"/admin", "/admin/health", "/admin/audit"} {
		t.Run(path, func(t *testing.T) {
			srv, db, _, cookies, _ := newNotesServer(t)

			// SPA 已加载：返回应用壳，绝不渲染 SSR 页面。
			assertServesShell(t, getWithCookies(t, srv, path, cookies), "GET "+path)

			// 匿名：重定向登录页，绝不返回外壳。
			anon := get(t, srv, path, nil)
			if anon.Code != http.StatusSeeOther || anon.Header().Get("Location") != "/login" {
				t.Errorf("anonymous GET %s = %d %q, want 303 /login", path, anon.Code, anon.Header().Get("Location"))
			}

			// 非 admin：403，且响应体里没有应用壳。
			_, strangerCookies, _ := createUserAndLogin(t, srv, db, "read_stranger")
			denied := getWithCookies(t, srv, path, strangerCookies)
			if denied.Code != http.StatusForbidden {
				t.Fatalf("non-admin GET %s = %d, want 403 (body %s)", path, denied.Code, snippet(denied.Body.String()))
			}
			if strings.Contains(denied.Body.String(), `id="app"`) {
				t.Errorf("a non-admin must not receive the SPA shell at %s", path)
			}

		})
	}
}

// TestAdminJSONGuard 钉住 JSON 端点的判权：匿名 401、非 admin 403、bearer 403。
func TestAdminJSONGuard(t *testing.T) {
	srv, db, _, _, _ := newNotesServer(t)
	paths := []string{"/api/v1/admin/summary", "/api/v1/admin/health", "/api/v1/admin/audit"}

	for _, path := range paths {
		// 匿名：401（浏览器 fetch 不能跟着 303 去登录页拿 HTML）。
		if rec := getJSON(t, srv, path, nil, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("anonymous GET %s = %d, want 401", path, rec.Code)
		}
		// bearer / API Key 凭据不属于浏览器会话：403。
		if rec := getJSON(t, srv, path, nil, map[string]string{"Authorization": "Bearer whatever"}); rec.Code != http.StatusForbidden {
			t.Errorf("bearer GET %s = %d, want 403", path, rec.Code)
		}
	}

	// 非 admin：403，并写越权审计。
	_, strangerCookies, _ := createUserAndLogin(t, srv, db, "json_stranger")
	for _, path := range paths {
		if rec := getJSON(t, srv, path, strangerCookies, nil); rec.Code != http.StatusForbidden {
			t.Errorf("non-admin GET %s = %d, want 403 (body %s)", path, rec.Code, snippet(rec.Body.String()))
		}
	}
	denied, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionPermissionDenied)
	if err != nil {
		t.Fatalf("count permission.denied audits: %v", err)
	}
	if denied == 0 {
		t.Error("no permission.denied audit rows written for SPA admin API attempts")
	}
}

// TestAdminJSONSummaryMatchesInstance 断言概览 JSON 的计数与库里一致。
func TestAdminJSONSummaryMatchesInstance(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "json deck")
	seedBasic(t, db, deck.ID, "q", "a")

	rec := getJSON(t, srv, "/api/v1/admin/summary", cookies, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET summary = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var got adminSummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	var total, active int64
	if err := db.Model(&store.User{}).Count(&total).Error; err != nil {
		t.Fatalf("count users: %v", err)
	}
	if err := db.Model(&store.User{}).Where("status = ?", store.StatusActive).Count(&active).Error; err != nil {
		t.Fatalf("count active users: %v", err)
	}
	if got.UsersTotal != total || got.UsersActive != active {
		t.Errorf("summary users = (%d/%d), want (%d/%d)", got.UsersTotal, got.UsersActive, total, active)
	}
	if got.Decks != 1 {
		t.Errorf("summary decks = %d, want 1", got.Decks)
	}
	if got.Notes < 1 || got.Cards < 1 {
		t.Errorf("summary notes/cards = (%d/%d), want >= 1", got.Notes, got.Cards)
	}
}

// TestAdminJSONHealthShape 断言健康 JSON 的读数形态：连通 ok、schema 与 due 有值。
func TestAdminJSONHealthShape(t *testing.T) {
	srv, db, _, cookies, _ := newNotesServer(t)

	rec := getJSON(t, srv, "/api/v1/admin/health", cookies, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET health = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var got adminHealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if got.Database != "ok" {
		t.Errorf("health database = %q, want ok", got.Database)
	}
	wantSchema, err := store.CurrentVersion(context.Background(), db)
	if err != nil {
		t.Fatalf("CurrentVersion: %v", err)
	}
	if got.SchemaVersion == nil || *got.SchemaVersion != wantSchema {
		t.Errorf("health schema_version = %v, want %d", got.SchemaVersion, wantSchema)
	}
	if got.Due == nil {
		t.Error("health due is null, want a count")
	}
}

// TestAdminJSONAuditFilters 断言审计 JSON 与 SSR 用同一份过滤语义与 notice 码。
func TestAdminJSONAuditFilters(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	aliceID := createRoleUser(t, db, "audit_alice", store.RoleUser)
	if err := db.Where("1 = 1").Delete(&store.AuditLog{}).Error; err != nil {
		t.Fatalf("clear audit log: %v", err)
	}
	deckTarget, deck7 := "deck", uint64(7)
	rows := []store.AuditLog{
		{UserID: store.Ptr(ownerID), Action: "deck.grant", TargetType: &deckTarget, TargetID: &deck7},
		{UserID: store.Ptr(aliceID), Action: "deck.grant", TargetType: &deckTarget, TargetID: &deck7},
		{UserID: nil, Action: "system.tick"},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed audit rows: %v", err)
	}

	decode := func(query string) (adminAuditResponse, int) {
		t.Helper()
		rec := getJSON(t, srv, "/api/v1/admin/audit"+query, cookies, nil)
		var got adminAuditResponse
		if rec.Code == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode audit%s: %v", query, err)
			}
		}
		return got, rec.Code
	}

	for _, tc := range []struct {
		query string
		total int64
		rows  int
	}{
		{"", 3, 3},
		{"?action=deck.grant", 2, 2},
		{"?target_type=deck&target_id=7", 2, 2},
		{"?action=does.not.exist", 0, 0},
	} {
		got, code := decode(tc.query)
		if code != http.StatusOK {
			t.Fatalf("GET audit%s = %d, want 200", tc.query, code)
		}
		if got.Total != tc.total || len(got.Rows) != tc.rows {
			t.Errorf("audit%s total/rows = %d/%d, want %d/%d", tc.query, got.Total, len(got.Rows), tc.total, tc.rows)
		}
	}

	// 用户名查不到时不能退化成「不过滤」：结果 0 行并给出稳定 notice 码。
	got, _ := decode("?user=nobody")
	if got.Total != 0 || got.Notice != "user_not_found" {
		t.Errorf("unknown-user filter = total %d notice %q, want 0 / user_not_found", got.Total, got.Notice)
	}

	// 系统动作、无目标的行：actor 与 target 均为 null。
	got, _ = decode("?action=system.tick")
	if len(got.Rows) != 1 || got.Rows[0].Actor != nil || got.Rows[0].Target != nil {
		t.Errorf("system row = %+v, want null actor and target", got.Rows)
	}
}
