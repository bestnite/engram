package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// keysPlaintextRe 匹配一次性明文（fcard_ 后至少 20 个 base64url 字符）；展示前缀只有
// 8 个字符，因此不会误命中列表里的前缀。
var keysPlaintextRe = regexp.MustCompile(`fcard_[A-Za-z0-9_-]{20,}`)

// newKeysAPI 用同一份数据库装配真实的 /api/v1 handler 链，用于验证创建出来的 key
// 能通过 bearer 鉴权访问 GET /api/v1/decks。
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

// apiGetDecks 用 bearer key 请求 GET /api/v1/decks，返回状态码。
func apiGetDecks(t *testing.T, h http.Handler, plaintext string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/decks", nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

// loginMember 直接落一行非管理员用户并登录，返回其 id、会话 cookie 与服务端 CSRF token。
// 验收明确要求「非管理员」也能创建 key，因此不复用 newNotesServer 的 admin owner。
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
	login := postForm(t, srv, "/login", url.Values{
		"username": {username}, "password": {"Sup3rSecret!"},
	}, nil)
	if login.Code != http.StatusSeeOther {
		t.Fatalf("POST /login (%s) = %d, want 303", username, login.Code)
	}
	var sess store.Session
	if err := db.Where("user_id = ?", u.ID).Order("created_at desc").First(&sess).Error; err != nil {
		t.Fatalf("load member session: %v", err)
	}
	return u.ID, login.Result().Cookies(), sess.CSRFToken
}

// keyCount 返回某用户当前的 key 数。
func keyCount(t *testing.T, db *gorm.DB, userID uint64) int {
	t.Helper()
	keys, err := store.NewAPIKeyStore(db).ListByUser(context.Background(), userID)
	if err != nil {
		t.Fatalf("ListByUser() error = %v", err)
	}
	return len(keys)
}
