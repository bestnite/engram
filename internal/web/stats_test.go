package web

import (
	"context"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// newStatsServer 装配一个带会话的测试服务，登录一个 owner，并种入最小统计夹具。
//
// 夹具覆盖每个 §9 指标至少一个非零值，这样页面渲染的是真实聚合结果，而不是一排空柱：
// 两条今日复习 + 一条昨日复习（复习量与学习曲线）、一个带稳定性与到期日的状态行
// （到期预测与留存）、一条带标签的笔记（标签维度）、一个卡组（卡组维度）。
func newStatsServer(t *testing.T) (srv *Server, db *gorm.DB, ownerID uint64, cookies []*http.Cookie) {
	t.Helper()
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "stats.db"))
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
	srv, err = New("127.0.0.1:0", Deps{
		DB:            db,
		Logger:        discardLogger(),
		SchemaVersion: func(ctx context.Context) (int, error) { return store.CurrentVersion(ctx, db) },
		Accounts:      accounts,
		Sessions:      mgr,
		Users:         users,
		Decks:         store.NewDeckStore(db),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	owner, err := accounts.CreateLocalUser(context.Background(), auth.CreateUserInput{
		Username: "owner", Email: "owner@example.com", Password: "Sup3rSecret!", Role: store.RoleAdmin,
		// 与 seedStatsFixture 的复习日口径对齐（UTC、午夜切点），否则「今日」会随
		// 默认时区 Asia/Shanghai + 04:00 切点在一天中的时段不同而错位。
		Timezone: "UTC", DayCutoffHour: store.Ptr(0),
	})
	if err != nil {
		t.Fatalf("CreateLocalUser() error = %v", err)
	}
	seedStatsFixture(t, db, owner.ID)

	login := postForm(t, srv, "/login", url.Values{
		"username": {"owner"}, "password": {"Sup3rSecret!"},
	}, nil)
	if login.Code != http.StatusSeeOther {
		t.Fatalf("POST /login status = %d, want 303", login.Code)
	}
	return srv, db, owner.ID, login.Result().Cookies()
}

// seedStatsFixture 写入一组可预测的统计夹具。
func seedStatsFixture(t *testing.T, db *gorm.DB, userID uint64) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	today := store.ReviewDayString(now, time.UTC, 0)
	yesterday := store.ShiftReviewDay(today, -1)
	dueSoon := now.Add(-1 * time.Hour)

	preset := store.NewPreset(userID, "Stats preset")
	if err := store.NewPresetStore(db).Create(ctx, &preset); err != nil {
		t.Fatalf("create preset: %v", err)
	}
	deck := store.Deck{
		OwnerUserID: userID, Name: "Stats deck", Description: "", Visibility: "private",
		PresetID: preset.ID, CreatedAt: now,
	}
	if err := db.Create(&deck).Error; err != nil {
		t.Fatalf("create deck: %v", err)
	}
	note := store.Note{
		DeckID: deck.ID, Kind: "basic", FieldsJSON: `{"front":"f","back":"b"}`,
		TagsJSON: `["algebra"]`, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&note).Error; err != nil {
		t.Fatalf("create note: %v", err)
	}
	card := store.Card{NoteID: note.ID, Template: "forward", CreatedAt: now}
	if err := db.Create(&card).Error; err != nil {
		t.Fatalf("create card: %v", err)
	}
	stability := 5.0
	state := store.CardState{
		CardID: card.ID, UserID: userID, State: "review", DueAt: &dueSoon, Stability: &stability,
	}
	if err := db.Create(&state).Error; err != nil {
		t.Fatalf("create card state: %v", err)
	}
	// 三条复习：今日两条（一条自评、一条机器判分），昨日一条；稳定性非空才能进留存桶。
	rows := []store.Review{
		{CardID: card.ID, UserID: userID, Rating: 3, GradeSource: "self", ReviewedAt: now, ReviewDay: today, StateBefore: 2, Stability: &stability},
		{CardID: card.ID, UserID: userID, Rating: 4, GradeSource: "typed", ReviewedAt: now, ReviewDay: today, StateBefore: 2, Stability: &stability},
		{CardID: card.ID, UserID: userID, Rating: 1, GradeSource: "self", ReviewedAt: now.Add(-24 * time.Hour), ReviewDay: yesterday, StateBefore: 0},
	}
	ms := 1500
	rows[0].ElapsedMS = &ms
	rows[1].ElapsedMS = &ms
	for i := range rows {
		if err := db.Create(&rows[i]).Error; err != nil {
			t.Fatalf("create review: %v", err)
		}
	}
}

// TestStatsPageRendersStoreNumbers 断言页面展示的数字来自既有聚合查询：
// 今日复习量、卡组名、标签与留存率都出现在渲染结果里。
func TestStatsPageRendersStoreNumbers(t *testing.T) {
	srv, _, _, cookies := newStatsServer(t)
	rec := getWithCookies(t, srv, "/stats", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /stats status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	body := rec.Body.String()
	for _, want := range []string{
		"统计与洞察",      // 页标题（语言包）
		"复习量",        // 复习量区块
		"到期预测",       // 到期预测区块
		"留存率",        // 留存率区块
		"Stats deck", // 卡组维度来自 store.DeckBreakdown
		"algebra",    // 标签维度来自 store.TagBreakdown
		"自评",         // 判分来源分布
		"机器判分",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /stats body is missing %q: %s", want, snippet(body))
		}
	}
	// 柱状条是纯 CSS：宽度由 handler 算好后写进 style 属性。
	if !strings.Contains(body, `style="width:`) {
		t.Errorf("GET /stats body has no inline CSS bar widths: %s", snippet(body))
	}
}

// TestStatsPageLocalizes 断言整页文案走语言包：?lang=en 返回英文，不残留中文。
func TestStatsPageLocalizes(t *testing.T) {
	srv, _, _, cookies := newStatsServer(t)
	body := getWithCookies(t, srv, "/stats?lang=en", cookies).Body.String()
	if !strings.Contains(body, "Statistics and insights") {
		t.Errorf("?lang=en did not localize the stats page: %s", snippet(body))
	}
	if strings.Contains(body, "统计与洞察") {
		t.Errorf("en page still contains the Chinese heading: %s", snippet(body))
	}
}

// TestStatsPageHasNoExternalRequests 是 M7-3 的核心验收：渲染出的 HTML 里出现任何
// 第三方主机就失败。图表用 HTML + CSS 柱状条，因此页面渲染时零外部网络请求。
func TestStatsPageHasNoExternalRequests(t *testing.T) {
	srv, _, _, cookies := newStatsServer(t)
	body := getWithCookies(t, srv, "/stats", cookies).Body.String()
	if hosts := thirdPartyHosts(body); len(hosts) > 0 {
		t.Fatalf("stats page references third-party hosts %v; the page must render with zero external network requests", hosts)
	}
}

// TestStatsPageRedirectsAnonymous 是负例：匿名访问被重定向到登录页。
func TestStatsPageRedirectsAnonymous(t *testing.T) {
	srv, _, _, _ := newStatsServer(t)
	rec := getWithCookies(t, srv, "/stats", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("GET /stats (anonymous) status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Fatalf("GET /stats (anonymous) Location = %q, want /login", loc)
	}
}

// TestThirdPartyHostsDetectsPlantedHost 自测扫描器：种一个 CDN 引用必须被抓出来，
// 否则 TestStatsPageHasNoExternalRequests 可能因为正则失效而假绿。
func TestThirdPartyHostsDetectsPlantedHost(t *testing.T) {
	planted := []string{
		`<script src="https://cdn.example.org/chart.js"></script>`,
		`<link rel="stylesheet" href="https://fonts.example.net/css"/>`,
		`<img src="//tracker.example.com/pixel.gif"/>`,
		`<iframe src="https://ads.example.com/frame"></iframe>`,
	}
	for _, html := range planted {
		if hosts := thirdPartyHosts(html); len(hosts) == 0 {
			t.Errorf("thirdPartyHosts() missed the planted host in %q", html)
		}
	}
	if hosts := thirdPartyHosts(`<link rel="stylesheet" href="/static/v/abc/css/tailwind.css"/>`); len(hosts) != 0 {
		t.Errorf("thirdPartyHosts() flagged a relative path as external: %v", hosts)
	}
	// 普通导航链接（如页脚源码仓库）不触发渲染期请求，必须放行。
	if hosts := thirdPartyHosts(`<a href="https://git.nite07.com/nite/engram">source</a>`); len(hosts) != 0 {
		t.Errorf("thirdPartyHosts() flagged a plain navigation link as an external request: %v", hosts)
	}
}

// thirdPartyHosts 返回 HTML 里会导致外部网络请求的第三方主机。
// 只统计会触发加载的属性（script/link/img/iframe/source/video/audio/embed/object/track
// 的 src/href/data）以及 CSS 的 url(...)。<a href> 是用户点击后才发生的导航，
// 不是渲染期的外部请求，因此不计入——页脚的源码仓库链接正属于这一类。
func thirdPartyHosts(html string) []string {
	var hosts []string
	for _, m := range resourceHostRe.FindAllStringSubmatch(html, -1) {
		hosts = append(hosts, m[1])
	}
	for _, m := range resourceRelativeHostRe.FindAllStringSubmatch(html, -1) {
		hosts = append(hosts, m[1])
	}
	for _, m := range cssURLHostRe.FindAllStringSubmatch(html, -1) {
		hosts = append(hosts, m[1])
	}
	return hosts
}

var (
	// 资源加载标签的属性里出现绝对 URL：会在渲染期发起外部请求。
	resourceHostRe = regexp.MustCompile(`(?i)<(?:script|link|img|iframe|source|video|audio|embed|object|track)\b[^>]*\b(?:src|href|data)\s*=\s*"https?://([A-Za-z0-9._-]+)`)
	// 同上，但为协议相对形式（//host/...）。
	resourceRelativeHostRe = regexp.MustCompile(`(?i)<(?:script|link|img|iframe|source|video|audio|embed|object|track)\b[^>]*\b(?:src|href|data)\s*=\s*"//([A-Za-z0-9._-]+)`)
	// CSS 的 url(...) 指向的外部主机。
	cssURLHostRe = regexp.MustCompile(`(?i)url\(\s*["']?https?://([A-Za-z0-9._-]+)`)
)
