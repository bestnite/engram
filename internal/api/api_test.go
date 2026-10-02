package api

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// testEnv 是 API 测试的装配：真实 SQLite + 全部 store + 可注入时钟。
type testEnv struct {
	api  *API
	db   *gorm.DB
	keys *store.APIKeyStore
	now  time.Time
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestEnv 打开一个临时 SQLite 库并装配 API；readLimit/writeLimit 用于限流用例。
func newTestEnv(t *testing.T, readLimit, writeLimit int) *testEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	auditor, err := auth.NewAuditor(store.NewAuditStore(db))
	if err != nil {
		t.Fatalf("NewAuditor() error = %v", err)
	}
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	keys := store.NewAPIKeyStore(db)
	a, err := New(Deps{
		DB:         db,
		Logger:     testLogger(),
		Keys:       keys,
		Users:      store.NewUserStore(db),
		Decks:      store.NewDeckStore(db),
		Notes:      store.NewNoteStore(db),
		Presets:    store.NewPresetStore(db),
		Cards:      store.NewCardStore(db),
		Auditor:    auditor,
		Now:        func() time.Time { return now },
		ReadLimit:  readLimit,
		WriteLimit: writeLimit,
	})
	if err != nil {
		t.Fatalf("api.New() error = %v", err)
	}
	return &testEnv{api: a, db: db, keys: keys, now: now}
}

func (e *testEnv) router() *gin.Engine {
	r := gin.New()
	e.api.Register(r)
	return r
}

// seedUser 直接写一行 users；status 固定 active。
func seedUser(t *testing.T, db *gorm.DB, name, role string) *store.User {
	t.Helper()
	u := store.User{
		Username: name, Email: name + "@example.com", DisplayName: name,
		Role: role, Status: store.StatusActive, Locale: "en",
		Timezone: "UTC", DayCutoffHour: 4, CreatedAt: time.Now().UTC(),
	}
	if err := db.Create(&u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	return &u
}

// seedKey 创建一个 API key；expires 非 nil 时设置过期时间。
func seedKey(t *testing.T, keys *store.APIKeyStore, userID uint64, scopes []string, expires *time.Time) *store.CreatedAPIKey {
	t.Helper()
	created, err := keys.Create(context.Background(), store.CreateAPIKeyParams{
		UserID: userID, Name: "test", Scopes: scopes, ExpiresAt: expires,
	})
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}
	return created
}

// seedDeck 创建一个属于 ownerID 的卡组，并顺带建一个 Default 预设。
func seedDeck(t *testing.T, db *gorm.DB, ownerID uint64) *store.Deck {
	t.Helper()
	preset := store.NewPreset(ownerID, "Default")
	if err := store.NewPresetStore(db).Create(context.Background(), &preset); err != nil {
		t.Fatalf("create preset: %v", err)
	}
	d := store.Deck{
		OwnerUserID: ownerID, Name: "deck", Visibility: "private",
		PresetID: preset.ID, CreatedAt: time.Now().UTC(),
	}
	if err := store.NewDeckStore(db).Create(context.Background(), &d); err != nil {
		t.Fatalf("create deck: %v", err)
	}
	return &d
}

// safeName 把子测试名转成可作为用户名/邮箱前缀的字符串。
func safeName(s string) string {
	return strings.NewReplacer(" ", "_", "-", "_", "/", "_").Replace(s)
}
