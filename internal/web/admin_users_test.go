package web

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// Acceptance test for user management: one test per action, focusing on session invalidation and last-admin protection.

// createRoleUser creates a local user with the given role directly (the admin cases need to create a second admin).
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

// adminPost submits a form with CSRF as an admin.
func adminPost(t *testing.T, srv *Server, path string, values url.Values, cookies []*http.Cookie, csrf string) *http.Response {
	t.Helper()
	values.Set("csrf_token", csrf)
	rec := postForm(t, srv, path, values, cookies)
	return rec.Result()
}

// assertSessionsRevoked asserts that all of a user's sessions are revoked (revoked_at non-null).
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
