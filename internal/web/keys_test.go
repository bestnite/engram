package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// keysPlaintextRe matches the one-time plaintext (at least 20 base64url characters after fcard_); the displayed prefix is only
// 8 characters, so it never accidentally matches a prefix in the list.
var keysPlaintextRe = regexp.MustCompile(`fcard_[A-Za-z0-9_-]{20,}`)

// newKeysAPI wires the real /api/v1 handler chain against the same database, to verify that a created key
// can reach GET /api/v1/decks with bearer auth.
func newKeysAPI(t *testing.T, db *gorm.DB) http.Handler {
	t.Helper()
	a, err := api.New(api.Deps{
		DB:      db,
		Logger:  discardLogger(),
		Keys:    store.NewAPIKeyStore(db),
		Users:   store.NewUserStore(db),
		Decks:   store.NewDeckStore(db),
		Notes:   store.NewNoteStore(db),
		Presets: store.NewPresetStore(db),
		Cards:   store.NewCardStore(db),
	})
	if err != nil {
		t.Fatalf("api.New() error = %v", err)
	}
	r := gin.New()
	a.Register(r)
	return r
}

// apiGetDecks requests GET /api/v1/decks with a bearer key and returns the status code.
func apiGetDecks(t *testing.T, h http.Handler, plaintext string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/decks", nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

// loginMember inserts a non-admin user row directly and signs in, returning their id, session cookie and server CSRF token.
// Acceptance explicitly requires that "non-admins" can create keys too, so it does not reuse newNotesServer's admin owner.
func loginMember(t *testing.T, srv *Server, db *gorm.DB, username string) (uint64, []*http.Cookie, string) {
	t.Helper()
	hasher := auth.NewPasswordHasher(auth.Params{Memory: 8 * 1024, Time: 1, Threads: 1, SaltLength: 16, KeyLength: 32})
	hash, err := hasher.Hash("Sup3rSecret!")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	u := store.User{
		Username: username, Email: username + "@example.com", DisplayName: username,
		PasswordHash: &hash, Role: store.RoleUser, Status: store.StatusActive,
		Locale: "zh-CN", Timezone: "UTC", DayCutoffHour: store.Ptr(4), CreatedAt: time.Now().UTC(),
	}
	if err := db.Create(&u).Error; err != nil {
		t.Fatalf("create member: %v", err)
	}
	// Sign-in goes through the SPA's same-origin JSON endpoint: failure is Fatal, success returns the session cookie and the bound CSRF token.
	cookies, csrf := loginJSON(t, srv, db, username, "Sup3rSecret!")
	return u.ID, cookies, csrf
}

// keyCount returns a user's current key count.
func keyCount(t *testing.T, db *gorm.DB, userID uint64) int {
	t.Helper()
	keys, err := store.NewAPIKeyStore(db).ListByUser(context.Background(), userID)
	if err != nil {
		t.Fatalf("ListByUser() error = %v", err)
	}
	return len(keys)
}
