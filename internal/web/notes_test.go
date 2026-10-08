package web

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/media"
	"git.nite07.com/nite/engram/internal/store"
)

// newNotesServer 装配一个具备账号、会话、卡组与笔记存储的测试服务，并登录一个 owner。
// 返回的 csrf 是服务端会话绑定的 token，供写操作表单使用。
func newNotesServer(t *testing.T) (srv *Server, db *gorm.DB, ownerID uint64, cookies []*http.Cookie, csrf string) {
	t.Helper()
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "notes.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	users := store.NewUserStore(db)
	sessions := store.NewSessionStore(db)
	accounts, err := auth.NewAccountService(users, sessions, store.NewAPIKeyStore(db), auth.NewPasswordHasher(auth.Params{
		Memory: 8 * 1024, Time: 1, Threads: 1, SaltLength: 16, KeyLength: 32,
	}))
	if err != nil {
		t.Fatalf("NewAccountService() error = %v", err)
	}
	mgr, err := auth.NewSessionManager(users, sessions, auth.SessionConfig{Secret: testSessionSecret})
	if err != nil {
		t.Fatalf("NewSessionManager() error = %v", err)
	}
	auditor, err := auth.NewAuditor(store.NewAuditStore(db))
	if err != nil {
		t.Fatalf("NewAuditor() error = %v", err)
	}
	mediaStore, err := media.New(filepath.Join(t.TempDir(), "media"), db)
	if err != nil {
		t.Fatalf("media.New() error = %v", err)
	}
	// 导入等写操作走 api service（REST/MCP/CLI/web 同一入口），因此测试服务也要装配 API；
	// MediaRoot 与媒体存储同一个，保证 web 导入的媒体落盘路径与生产一致。
	apiSrv, err := api.New(api.Deps{
		DB:        db,
		Logger:    discardLogger(),
		Keys:      store.NewAPIKeyStore(db),
		Users:     users,
		Decks:     store.NewDeckStore(db),
		Notes:     store.NewNoteStore(db),
		Presets:   store.NewPresetStore(db),
		Cards:     store.NewCardStore(db),
		Auditor:   auditor,
		MediaRoot: mediaStore.Root(),
	})
	if err != nil {
		t.Fatalf("api.New() error = %v", err)
	}
	srv, err = New("127.0.0.1:0", Deps{
		DB:            db,
		Logger:        discardLogger(),
		SchemaVersion: func(ctx context.Context) (int, error) { return store.CurrentVersion(ctx, db) },
		Accounts:      accounts,
		Sessions:      mgr,
		Users:         users,
		// 会话中间件先于语言中间件运行，用户设置里的语言才会生效。
		UserLocale: SessionUserLocale,
		Decks:      store.NewDeckStore(db),
		Notes:      store.NewNoteStore(db),
		Cards:      store.NewCardStore(db),
		Presets:    store.NewPresetStore(db),
		Auditor:    auditor,
		Media:      mediaStore,
		API:        apiSrv,
		LoginLimiter: auth.NewLoginLimiter(auth.LimiterConfig{
			Sleep: func(context.Context, time.Duration) error { return nil },
		}),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	owner, err := accounts.CreateLocalUser(context.Background(), auth.CreateUserInput{
		Username: "owner", Email: "owner@example.com", Password: "Sup3rSecret!", Role: store.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("CreateLocalUser() error = %v", err)
	}
	// 登录走 SPA 的同源 JSON 端点：失败即 Fatal，成功返回会话 cookie 与会话绑定的 CSRF token。
	cookies, csrf = loginJSON(t, srv, db, "owner", "Sup3rSecret!")
	return srv, db, owner.ID, cookies, csrf
}

// seedDeck 建一个属于 owner 的卡组（含一个调度预设，满足外键列）。
func seedDeck(t *testing.T, db *gorm.DB, ownerID uint64, name string) *store.Deck {
	t.Helper()
	now := time.Now().UTC()
	preset := store.Preset{OwnerUserID: ownerID, Name: "preset", LearningSteps: "1m,10m",
		RelearningSteps: "10m", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&preset).Error; err != nil {
		t.Fatalf("create preset: %v", err)
	}
	deck := store.Deck{OwnerUserID: ownerID, Name: name, Visibility: store.DeckVisibilityPrivate,
		PresetID: preset.ID, CreatedAt: now}
	if err := store.NewDeckStore(db).Create(context.Background(), &deck); err != nil {
		t.Fatalf("create deck: %v", err)
	}
	return &deck
}

// seedBasic 建一张 basic note；tags 非空时写入 tags_json。
func seedBasic(t *testing.T, db *gorm.DB, deckID uint64, front, back string, tags ...string) *store.Note {
	t.Helper()
	note := &store.Note{DeckID: deckID, Kind: "basic"}
	if len(tags) > 0 {
		raw, _ := jsonMarshalStrings(tags)
		note.TagsJSON = raw
	}
	if _, err := store.NewNoteStore(db).Create(context.Background(), note,
		map[string]any{"front": front, "back": back}); err != nil {
		t.Fatalf("create note %q: %v", front, err)
	}
	return note
}

// jsonMarshalStrings 是测试用的最小 JSON 数组编码，避免测试再引入一组编码逻辑。
func jsonMarshalStrings(xs []string) (string, error) {
	quoted := make([]string, len(xs))
	for i, x := range xs {
		quoted[i] = `"` + strings.ReplaceAll(x, `"`, `\"`) + `"`
	}
	return "[" + strings.Join(quoted, ",") + "]", nil
}

// TestNotePagesRejectAnonymousAndForeign 覆盖页面的鉴权门：未登录重定向、非 owner 403。
// 卡片列表要求 reader、编辑页要求 editor，两条判定与迁移前的 SSR 页面逐项一致。
func TestNotePagesRejectAnonymousAndForeign(t *testing.T) {
	srv, db, ownerID, _, _ := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Private deck")
	note := seedBasic(t, db, deck.ID, "Q001", "A001")

	if code := get(t, srv, "/decks/"+u64str(deck.ID)+"/notes", nil).Code; code != http.StatusSeeOther {
		t.Errorf("anonymous GET list status = %d, want 303 redirect to login", code)
	}

	// 另一个用户（非 owner）访问同一卡组必须被拒。
	users := store.NewUserStore(db)
	sessions := store.NewSessionStore(db)
	accounts, err := auth.NewAccountService(users, sessions, store.NewAPIKeyStore(db), auth.NewPasswordHasher(auth.Params{
		Memory: 8 * 1024, Time: 1, Threads: 1, SaltLength: 16, KeyLength: 32,
	}))
	if err != nil {
		t.Fatalf("NewAccountService() error = %v", err)
	}
	if _, err := accounts.CreateLocalUser(context.Background(), auth.CreateUserInput{
		Username: "stranger", Email: "stranger@example.com", Password: "Sup3rSecret!", Role: store.RoleUser,
	}); err != nil {
		t.Fatalf("create stranger: %v", err)
	}
	strangerCookies, _ := loginJSON(t, srv, db, "stranger", "Sup3rSecret!")
	foreign := getWithCookies(t, srv, "/decks/"+u64str(deck.ID)+"/notes", strangerCookies)
	if foreign.Code != http.StatusForbidden {
		t.Errorf("non-owner GET list status = %d, want 403", foreign.Code)
	}
	if rec := getWithCookies(t, srv, "/decks/"+u64str(deck.ID)+"/notes/"+u64str(note.ID), strangerCookies); rec.Code != http.StatusForbidden {
		t.Errorf("non-owner GET editor status = %d, want 403", rec.Code)
	}
}

func u64str(v uint64) string { return itoa(int(v)) }

// pad3 是测试里把序号补成三位的小工具（front/back 文案生成用）。
func pad3(i int) string {
	s := "000" + itoa(i)
	return s[len(s)-3:]
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
