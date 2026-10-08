package web

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// 用户管理的验收测试：每个动作一条，重点覆盖会话作废与最后管理员保护。

// createRoleUser 直接建一个指定角色的本地用户（管理员用例需要造第二个管理员）。
func createRoleUser(t *testing.T, db *gorm.DB, username, role string) uint64 {
	t.Helper()
	accounts, err := auth.NewAccountService(store.NewUserStore(db), store.NewSessionStore(db), store.NewAPIKeyStore(db),
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

var tempPasswordRe = regexp.MustCompile(`<code class="[^"]*">([^<]+)</code>`)
