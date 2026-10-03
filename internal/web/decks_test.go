package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

// TestDeckCreateAppearsInList 是 M2-11 的主验收：登录用户在 /decks 建卡组后能在列表中看到它。
func TestDeckCreateAppearsInList(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)

	// 无预设时，创建卡组应自动补一个默认预设，保证新用户也能建组。
	rec := postForm(t, srv, "/decks", url.Values{
		"csrf_token":  {csrf},
		"name":        {"My first deck"},
		"description": {"created through the browser"},
	}, cookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /decks status = %d, want 303 (body %s)", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/decks" {
		t.Fatalf("POST /decks Location = %q, want /decks", loc)
	}

	var deck store.Deck
	if err := db.Where("owner_user_id = ? AND name = ?", ownerID, "My first deck").First(&deck).Error; err != nil {
		t.Fatalf("deck was not persisted: %v", err)
	}
	if deck.PresetID == 0 {
		t.Errorf("created deck has no preset")
	}

	page := getWithCookies(t, srv, "/decks", cookies)
	if page.Code != http.StatusOK {
		t.Fatalf("GET /decks status = %d, want 200 (body %s)", page.Code, page.Body.String())
	}
	if !strings.Contains(page.Body.String(), "My first deck") {
		t.Errorf("GET /decks body does not list the created deck: %s", page.Body.String())
	}

	// 写操作必须落一行审计（ROADMAP.md M2-11）。
	n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionDeckCreate)
	if err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if n != 1 {
		t.Errorf("audit rows for %s = %d, want 1", store.ActionDeckCreate, n)
	}
}

// TestDeckListRedirectsAnonymousToLogin 覆盖匿名访问被重定向到登录页的验收点。
func TestDeckListRedirectsAnonymousToLogin(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	rec := getWithCookies(t, srv, "/decks", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("GET /decks (anonymous) status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Fatalf("GET /decks (anonymous) Location = %q, want /login", loc)
	}
}

// TestDeckCreateRequiresCSRF 是必测的负例：缺少 CSRF token 的写请求被拒，且不落库。
func TestDeckCreateRequiresCSRF(t *testing.T) {
	srv, db, _, cookies, _ := newNotesServer(t)
	rec := postForm(t, srv, "/decks", url.Values{"name": {"no csrf"}}, cookies)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST /decks without CSRF status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	var n int64
	if err := db.Model(&store.Deck{}).Where("name = ?", "no csrf").Count(&n).Error; err != nil {
		t.Fatalf("count decks: %v", err)
	}
	if n != 0 {
		t.Errorf("deck created despite missing CSRF: count = %d", n)
	}
}

// TestDeckCreateRejectsEmptyName 覆盖表单校验：空名称回显 400，且不落库。
func TestDeckCreateRejectsEmptyName(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	rec := postForm(t, srv, "/decks", url.Values{"csrf_token": {csrf}, "name": {"   "}}, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /decks with empty name status = %d, want 400", rec.Code)
	}
	var n int64
	if err := db.Model(&store.Deck{}).Count(&n).Error; err != nil {
		t.Fatalf("count decks: %v", err)
	}
	if n != 0 {
		t.Errorf("deck count = %d, want 0", n)
	}
}

// TestDeckListRendersReviewScopeControls 是 M3-13 的列表页验收：每行一个 name="deck" 的
// 复选框与一个「复习」链接，页头是提交给 /review 的「复习所选」按钮（GET 表单，无 JS）。
func TestDeckListRendersReviewScopeControls(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deckA := seedReviewDeck(t, db, ownerID, "Alpha deck")
	deckB := seedReviewDeck(t, db, ownerID, "Beta deck")

	rec := getWithCookies(t, srv, "/decks", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /decks status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	body := rec.Body.String()

	if !strings.Contains(body, `method="get"`) || !strings.Contains(body, `action="/review"`) {
		t.Errorf("deck list has no GET form targeting /review: %s", snippet(body))
	}
	for _, id := range []uint64{deckA.ID, deckB.ID} {
		if !strings.Contains(body, `name="deck" value="`+u64str(id)+`"`) {
			t.Errorf("deck list is missing the review checkbox for deck %d: %s", id, snippet(body))
		}
		if !strings.Contains(body, `href="/review?deck=`+u64str(id)+`"`) {
			t.Errorf("deck list is missing the per-row review link for deck %d: %s", id, snippet(body))
		}
	}
	// 默认语言 zh-CN：页头「复习所选」提交按钮必须存在（文案来自语言包）。
	if !strings.Contains(body, "复习所选") {
		t.Errorf("deck list has no review-selected submit button: %s", snippet(body))
	}
}

// ---- 今日可刷的两个数（额度口径重构的列表侧验收）----

// seedCardRow 建一张 basic note + forward card，返回 card id（列表计数用，不渲染卡片内容）。
func seedCardRow(t *testing.T, db *gorm.DB, deckID uint64, front string, createdAt time.Time) uint64 {
	t.Helper()
	note := store.Note{DeckID: deckID, Kind: "basic",
		FieldsJSON: `{"front":"` + front + `","back":"b"}`, TagsJSON: "[]",
		CreatedAt: createdAt, UpdatedAt: createdAt}
	if err := db.Create(&note).Error; err != nil {
		t.Fatalf("create note: %v", err)
	}
	card := store.Card{NoteID: note.ID, Template: "forward", CreatedAt: createdAt}
	if err := db.Create(&card).Error; err != nil {
		t.Fatalf("create card: %v", err)
	}
	return card.ID
}

// deckRowHTML 取列表页里某个卡组的 <tr> 片段（按复选框 value 定位）。
func deckRowHTML(body string, deckID uint64) string {
	marker := `name="deck" value="` + u64str(deckID) + `"`
	idx := strings.Index(body, marker)
	if idx < 0 {
		return ""
	}
	start := strings.LastIndex(body[:idx], "<tr")
	end := strings.Index(body[idx:], "</tr>")
	if start < 0 || end < 0 {
		return ""
	}
	return body[start : idx+end+len("</tr>")]
}

// TestDeckListShowsPerDeckTodayCounts 断言列表行渲染「新 X · 复习 Y」，且 X/Y 与该卡组单卡组
// 口径下队列（Build(DeckID=d)）的条数一致 —— 两者相加就是点进去能刷的张数。
// 含"新卡额度用尽 → 新 0"的夹具，直接对应线上「到期数不为 0、点进去却说没有可复习」的症状。
func TestDeckListShowsPerDeckTodayCounts(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	ctx := context.Background()
	now := time.Now().UTC()
	day := schedule.ReviewDay(now, time.UTC, schedule.DefaultDayCutoffHour)
	stability, difficulty := 5.0, 5.0
	last := now.Add(-48 * time.Hour)

	// 卡组 A：3 张新卡 + 2 张到期复习卡 + 1 张到期的学习卡 → 新 3、复习 3。
	deckA := seedReviewDeck(t, db, ownerID, "Counts deck")
	for i := 0; i < 3; i++ {
		seedCardRow(t, db, deckA.ID, "new", now.Add(time.Duration(i)*time.Minute))
	}
	for i := 0; i < 2; i++ {
		id := seedCardRow(t, db, deckA.ID, "due", now)
		due := now.Add(-time.Hour)
		if err := db.Create(&store.CardState{CardID: id, UserID: ownerID, State: "review",
			DueAt: &due, Stability: &stability, Difficulty: &difficulty, LastReviewAt: &last}).Error; err != nil {
			t.Fatalf("create card state: %v", err)
		}
	}
	// 一张到期的学习卡：不占复习额度，但排在队列最前面，因此必须计入「复习」数。
	learning := seedCardRow(t, db, deckA.ID, "learning", now)
	learnDue := now.Add(-time.Minute)
	if err := db.Create(&store.CardState{CardID: learning, UserID: ownerID, State: "learning",
		DueAt: &learnDue}).Error; err != nil {
		t.Fatalf("create learning card state: %v", err)
	}

	// 卡组 B：new_per_day=1，今日已引入 1 张、另有 2 张新卡 → 新 0（额度用尽）。
	deckB := seedReviewDeck(t, db, ownerID, "Quota deck")
	if err := store.NewDeckStore(db).SetCaps(ctx, ownerID, deckB.ID,
		store.DeckCaps{NewPerDay: 1, ReviewsPerDay: 200}); err != nil {
		t.Fatalf("SetCaps: %v", err)
	}
	used := seedCardRow(t, db, deckB.ID, "used", now)
	if err := db.Create(&store.Review{CardID: used, UserID: ownerID, Rating: 3, GradeSource: "self",
		ReviewedAt: now.Add(-time.Hour), ReviewDay: day, StateBefore: 0}).Error; err != nil {
		t.Fatalf("create review: %v", err)
	}
	future := now.Add(48 * time.Hour)
	if err := db.Create(&store.CardState{CardID: used, UserID: ownerID, State: "review",
		DueAt: &future, Stability: &stability, Difficulty: &difficulty}).Error; err != nil {
		t.Fatalf("create card state: %v", err)
	}
	seedCardRow(t, db, deckB.ID, "fresh1", now.Add(time.Minute))
	seedCardRow(t, db, deckB.ID, "fresh2", now.Add(2*time.Minute))

	// 期望值取自真正的队列路径（Build(DeckID=d)），不是列表自己的算法。
	preset := store.NewPreset(ownerID, "counts preset")
	sched, err := schedule.NewScheduler(&preset)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	builder := schedule.NewQueueBuilder(db, srv.decks, sched)
	counts := func(deckID uint64) (int, int) {
		items, err := builder.Build(ctx, ownerID, schedule.QueueOptions{DeckID: deckID, Now: now, Location: time.UTC})
		if err != nil {
			t.Fatalf("Build(DeckID=%d): %v", deckID, err)
		}
		newN, reviewN := 0, 0
		for _, it := range items {
			switch it.Kind {
			case schedule.QueueNew:
				newN++
			case schedule.QueueReview, schedule.QueueLearning:
				// 「复习」数含学习/再学习到期卡：它们排在队列最前面，也是点进去能刷的一部分。
				reviewN++
			}
		}
		return newN, reviewN
	}
	wantANew, wantAReview := counts(deckA.ID)
	wantBNew, wantBReview := counts(deckB.ID)
	if wantANew != 3 || wantAReview != 3 {
		t.Fatalf("deck A build counts = %d/%d, want 3/3 (2 due reviews + 1 due learning card)", wantANew, wantAReview)
	}
	if wantBNew != 0 || wantBReview != 0 {
		t.Fatalf("deck B build counts = %d/%d, want 0/0 (new quota used up)", wantBNew, wantBReview)
	}

	page := getWithCookies(t, srv, "/decks", cookies)
	if page.Code != http.StatusOK {
		t.Fatalf("GET /decks status = %d, want 200 (body %s)", page.Code, snippet(page.Body.String()))
	}
	body := page.Body.String()
	if !strings.Contains(body, "新") || !strings.Contains(body, "复习") {
		t.Fatalf("deck list does not render the new/review labels: %s", snippet(body))
	}

	rowA := deckRowHTML(body, deckA.ID)
	if rowA == "" {
		t.Fatalf("no row for deck A: %s", snippet(body))
	}
	// 两个数都是 3（3 张新卡、3 张到期卡）；逐个数一遍，确保渲染的是两个独立的数。
	if n := strings.Count(rowA, ">3</span>"); n != 2 {
		t.Errorf("deck A row should show 3 in both numbers, got %d occurrence(s): %s", n, rowA)
	}
	// 卡组 B：新 0、复习 0；两张新卡不得被算进「新」（额度已用尽）。
	rowB := deckRowHTML(body, deckB.ID)
	if rowB == "" {
		t.Fatalf("no row for deck B: %s", snippet(body))
	}
	if !strings.Contains(rowB, ">0</span>") {
		t.Errorf("deck B row should show 0 for the used-up new quota: %s", rowB)
	}
	if strings.Contains(rowB, ">2</span>") {
		t.Errorf("deck B row counted the two blocked new cards: %s", rowB)
	}
}

// TestDeckListSettingsLinkIsOwnerOnly 断言 owner 行渲染 /decks/{id}/settings 链接，
// 非 owner 行（别人的 public 卡组）不渲染。
func TestDeckListSettingsLinkIsOwnerOnly(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	ctx := context.Background()
	now := time.Now().UTC()

	own := seedReviewDeck(t, db, ownerID, "Own deck")

	// 另一个用户拥有的 public 卡组：owner 在列表里能看到，但不是它的 owner。
	other := store.User{Username: "stranger", Email: "stranger@example.com", DisplayName: "Stranger",
		Role: store.RoleUser, Status: store.StatusActive, Locale: "zh-CN", Timezone: "UTC", CreatedAt: now}
	if err := db.Create(&other).Error; err != nil {
		t.Fatalf("create other user: %v", err)
	}
	preset := store.Preset{OwnerUserID: other.ID, Name: "p", LearningSteps: "1m,10m",
		RelearningSteps: "10m", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&preset).Error; err != nil {
		t.Fatalf("create preset: %v", err)
	}
	foreign := store.Deck{OwnerUserID: other.ID, Name: "Public deck",
		Visibility: store.DeckVisibilityPublic, PresetID: preset.ID, CreatedAt: now}
	if err := store.NewDeckStore(db).Create(ctx, &foreign); err != nil {
		t.Fatalf("create deck: %v", err)
	}

	page := getWithCookies(t, srv, "/decks", cookies)
	if page.Code != http.StatusOK {
		t.Fatalf("GET /decks status = %d, want 200 (body %s)", page.Code, snippet(page.Body.String()))
	}
	body := page.Body.String()

	ownRow := deckRowHTML(body, own.ID)
	if !strings.Contains(ownRow, `href="/decks/`+u64str(own.ID)+`/settings"`) {
		t.Errorf("owner row is missing the settings link: %s", ownRow)
	}
	foreignRow := deckRowHTML(body, foreign.ID)
	if foreignRow == "" {
		t.Fatalf("public deck row was not rendered: %s", snippet(body))
	}
	if strings.Contains(foreignRow, "/settings") {
		t.Errorf("non-owner row rendered a settings link: %s", foreignRow)
	}
	if strings.Contains(body, `href="/decks/`+u64str(foreign.ID)+`/settings"`) {
		t.Errorf("settings link for a non-owned deck leaked: %s", snippet(body))
	}
}
