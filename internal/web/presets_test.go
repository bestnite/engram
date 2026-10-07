package web

import (
	"context"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/jobs"
	"git.nite07.com/nite/engram/internal/store"
)

// newPresetsServer 装配一个带作业执行器的测试服务并登录 owner。
// runner 不调用 Start：作业会停在内存队列里保持 queued，测试因此可以确定性地模拟
// 「适配器跑完并写回权重」，而不用真的跑外部优化器（M9-4 允许注入假命令/直接驱动状态）。
func newPresetsServer(t *testing.T) (srv *Server, db *gorm.DB, ownerID uint64, cookies []*http.Cookie, csrf string, runner *jobs.Runner) {
	t.Helper()
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "presets.db"))
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
	runner, err = jobs.New(jobs.Deps{DB: db, Logger: discardLogger()})
	if err != nil {
		t.Fatalf("jobs.New() error = %v", err)
	}
	srv, err = New("127.0.0.1:0", Deps{
		DB:            db,
		Logger:        discardLogger(),
		SchemaVersion: func(ctx context.Context) (int, error) { return store.CurrentVersion(ctx, db) },
		Accounts:      accounts,
		Sessions:      mgr,
		Users:         users,
		Decks:         store.NewDeckStore(db),
		Presets:       store.NewPresetStore(db),
		Auditor:       auditor,
		Jobs:          runner,
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
	return srv, db, owner.ID, cookies, csrf, runner
}

// seedPreset 建一个属于 owner 的**默认**预设：名用稳定字面量 store.DefaultPresetName，
// 这样页面渲染时的 store.EnsureDefaultPreset 会认它、不再补一条（M3-14）。
func seedPreset(t *testing.T, db *gorm.DB, ownerID uint64) *store.Preset {
	t.Helper()
	p := store.NewPreset(ownerID, store.DefaultPresetName)
	if err := store.NewPresetStore(db).Create(context.Background(), &p); err != nil {
		t.Fatalf("create preset: %v", err)
	}
	return &p
}

// seedReviews 插入 n 条属于 userID 的复习日志；优化门槛只按 user_id 计数。
func seedReviews(t *testing.T, db *gorm.DB, userID uint64, n int) {
	t.Helper()
	now := time.Now().UTC()
	for i := 0; i < n; i++ {
		row := store.Review{
			CardID:      uint64(i + 1),
			UserID:      userID,
			Rating:      3,
			GradeSource: "self",
			ReviewedAt:  now,
			ReviewDay:   now.Format("2006-01-02"),
			StateBefore: 2,
		}
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("seed review: %v", err)
		}
	}
}

// setMinReviews 覆盖优化门槛；put 的值是 JSON 编码字符串，optimize 读取端再解码。
func setMinReviews(t *testing.T, db *gorm.DB, n int) {
	t.Helper()
	if err := store.PutSetting(context.Background(), db, store.SettingKeyOptimizeMinReviews,
		strconv.Itoa(n), nil, time.Now().UTC()); err != nil {
		t.Fatalf("PutSetting(%s): %v", store.SettingKeyOptimizeMinReviews, err)
	}
}

// TestPresetListRedirectsAnonymous 覆盖匿名访问重定向到登录页。
func TestPresetListRedirectsAnonymous(t *testing.T) {
	srv, _, _, _, _, _ := newPresetsServer(t)
	rec := getWithCookies(t, srv, "/presets", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("GET /presets (anonymous) status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Fatalf("GET /presets (anonymous) Location = %q, want /login", loc)
	}
}

// ---- M3-14 预设管理：新建、编辑与默认预设 ------------------------------------
