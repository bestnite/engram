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

// ddAfterLabel 返回页面上某个 <dt>标签</dt> 之后第一个 </dd> 内的文本。
// 设置页的今日额度是 dt/dd 成对渲染的，用它按本地化标签取值，避免按数字猜位置。
func ddAfterLabel(body, label string) string {
	idx := strings.Index(body, ">"+label+"</dt>")
	if idx < 0 {
		return ""
	}
	rest := body[idx:]
	start := strings.Index(rest, "<dd")
	if start < 0 {
		return ""
	}
	rest = rest[start:]
	gt := strings.Index(rest, ">")
	if gt < 0 {
		return ""
	}
	rest = rest[gt+1:]
	end := strings.Index(rest, "</dd>")
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// seedTodayUsage 写入今日的已用记录：newUsed 次「引入新卡」（state_before=0）与
// reviewUsed 次「复习」（state_before<>0）。每张卡都配一行未来的 card_states，
// 避免它们以「新卡」身份再次入队而干扰计数。
func seedTodayUsage(t *testing.T, db *gorm.DB, ownerID, deckID uint64, newUsed, reviewUsed int) {
	t.Helper()
	now := time.Now().UTC()
	day := schedule.ReviewDay(now, time.UTC, schedule.DefaultDayCutoffHour)
	stability, difficulty := 5.0, 5.0
	future := now.Add(48 * time.Hour)
	last := now.Add(-48 * time.Hour)
	seed := func(n int, stateBefore int) {
		for i := 0; i < n; i++ {
			cardID := seedCardRow(t, db, deckID, "used", now)
			if err := db.Create(&store.Review{CardID: cardID, UserID: ownerID, Rating: 3, GradeSource: "self",
				ReviewedAt: now.Add(-time.Hour), ReviewDay: day, StateBefore: stateBefore}).Error; err != nil {
				t.Fatalf("create review: %v", err)
			}
			if err := db.Create(&store.CardState{CardID: cardID, UserID: ownerID, State: "review",
				DueAt: &future, Stability: &stability, Difficulty: &difficulty, LastReviewAt: &last}).Error; err != nil {
				t.Fatalf("create card state: %v", err)
			}
		}
	}
	seed(newUsed, int(schedule.StateNew))
	seed(reviewUsed, int(schedule.StateReview))
}

// deckQueueCountsNow 用真实队列（Build(DeckID=d)）数一个卡组今天能刷的新卡与复习卡
// （复习数含学习/再学习到期卡），与列表页那两个数同源。
func deckQueueCountsNow(t *testing.T, srv *Server, db *gorm.DB, userID, deckID uint64) (newN, reviewN int) {
	t.Helper()
	preset := store.NewPreset(userID, "counts preset")
	sched, err := schedule.NewScheduler(&preset)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	builder := schedule.NewQueueBuilder(db, srv.decks, sched)
	items, err := builder.Build(context.Background(), userID,
		schedule.QueueOptions{DeckID: deckID, Now: time.Now().UTC(), Location: time.UTC, NewOrder: schedule.NewOrderCreated})
	if err != nil {
		t.Fatalf("Build(DeckID=%d): %v", deckID, err)
	}
	for _, it := range items {
		switch it.Kind {
		case schedule.QueueNew:
			newN++
		case schedule.QueueReview, schedule.QueueLearning:
			reviewN++
		}
	}
	return newN, reviewN
}

// deckCapsFromDB 回读卡组的两列上限，用于断言「未写库」或写入值。
func deckCapsFromDB(t *testing.T, db *gorm.DB, deckID uint64) store.DeckCaps {
	t.Helper()
	caps, err := store.NewDeckStore(db).Caps(context.Background(), deckID)
	if err != nil {
		t.Fatalf("read deck caps: %v", err)
	}
	return caps
}

// TestDeckSettingsPageShowsCapsAndTodayUsage 是设置页的渲染验收：两个数字输入显示库里的
// 当前值（含 0），今日已用/剩余来自 schedule.DeckBudgets（与队列同源），表单字段名固定。
func TestDeckSettingsPageShowsCapsAndTodayUsage(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Settings deck")
	if err := store.NewDeckStore(db).SetCaps(context.Background(), ownerID, deck.ID,
		store.DeckCaps{NewPerDay: 5, ReviewsPerDay: 10}); err != nil {
		t.Fatalf("SetCaps: %v", err)
	}
	// 今日已引入 2 张新卡、复习 3 张 → 新卡剩余 3、复习剩余 7。
	seedTodayUsage(t, db, ownerID, deck.ID, 2, 3)

	rec := getWithCookies(t, srv, "/decks/"+u64str(deck.ID)+"/settings", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET settings status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	body := rec.Body.String()

	if got := attrValue(body, "new_per_day"); got != "5" {
		t.Errorf("new_per_day input value = %q, want 5", got)
	}
	if got := attrValue(body, "reviews_per_day"); got != "10" {
		t.Errorf("reviews_per_day input value = %q, want 10", got)
	}
	if !strings.Contains(body, `method="post"`) || !strings.Contains(body, `action="/decks/`+u64str(deck.ID)+`/settings"`) {
		t.Errorf("settings form is missing its POST action: %s", snippet(body))
	}
	if got := attrValue(body, "csrf_token"); got != csrf {
		t.Errorf("csrf_token value = %q, want the session token", got)
	}
	if !strings.Contains(body, "Settings deck") {
		t.Errorf("page does not show the deck name: %s", snippet(body))
	}

	// 今日额度四个格子：已用 / 剩余，取自 DeckBudgets。
	if got := ddAfterLabel(body, "新卡已用"); got != "2" {
		t.Errorf("new-used = %q, want 2", got)
	}
	if got := ddAfterLabel(body, "新卡剩余"); got != "3" {
		t.Errorf("new-left = %q, want 3 (cap 5 minus 2 used)", got)
	}
	if got := ddAfterLabel(body, "复习已用"); got != "3" {
		t.Errorf("review-used = %q, want 3", got)
	}
	if got := ddAfterLabel(body, "复习剩余"); got != "7" {
		t.Errorf("review-left = %q, want 7 (cap 10 minus 3 used)", got)
	}
}

// TestDeckSettingsUpdateTakesEffectImmediately 是主验收：改小上限后，同一 server 实例上
// 该卡组今天能刷的新卡 / 复习卡立刻变少，且库里的两列就是提交值。
func TestDeckSettingsUpdateTakesEffectImmediately(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Capped deck")
	if err := store.NewDeckStore(db).SetCaps(context.Background(), ownerID, deck.ID,
		store.DeckCaps{NewPerDay: 5, ReviewsPerDay: 5}); err != nil {
		t.Fatalf("SetCaps: %v", err)
	}
	// 5 张新卡 + 3 张到期复习卡：上限 5/5 时能刷 5 新 / 3 复习。
	for i := 0; i < 5; i++ {
		seedCardRow(t, db, deck.ID, "new", time.Now().UTC().Add(time.Duration(i)*time.Minute))
	}
	stability, difficulty := 5.0, 5.0
	last := time.Now().UTC().Add(-48 * time.Hour)
	for i := 0; i < 3; i++ {
		id := seedCardRow(t, db, deck.ID, "due", time.Now().UTC())
		due := time.Now().UTC().Add(-time.Hour)
		if err := db.Create(&store.CardState{CardID: id, UserID: ownerID, State: "review",
			DueAt: &due, Stability: &stability, Difficulty: &difficulty, LastReviewAt: &last}).Error; err != nil {
			t.Fatalf("create card state: %v", err)
		}
	}
	if n, r := deckQueueCountsNow(t, srv, db, ownerID, deck.ID); n != 5 || r != 3 {
		t.Fatalf("before update queue = %d new / %d review, want 5/3", n, r)
	}

	rec := postForm(t, srv, "/decks/"+u64str(deck.ID)+"/settings", url.Values{
		"csrf_token":      {csrf},
		"new_per_day":     {"2"},
		"reviews_per_day": {"1"},
	}, cookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST settings status = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if loc := rec.Header().Get("Location"); !strings.HasSuffix(loc, "?notice=saved") {
		t.Errorf("POST settings Location = %q, want a ?notice=saved redirect back to the page", loc)
	}
	if caps := deckCapsFromDB(t, db, deck.ID); caps.NewPerDay != 2 || caps.ReviewsPerDay != 1 {
		t.Fatalf("stored caps = %d/%d, want 2/1", caps.NewPerDay, caps.ReviewsPerDay)
	}

	// 同一 server、无需重启：队列立刻变小。
	if n, r := deckQueueCountsNow(t, srv, db, ownerID, deck.ID); n != 2 || r != 1 {
		t.Errorf("after update queue = %d new / %d review, want 2/1 (effective immediately)", n, r)
	}

	// 改额度与其他卡组变更同口径：留一条审计（谁在什么时候把额度改成了什么）。
	if n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionDeckCaps); err != nil {
		t.Fatalf("count audit rows: %v", err)
	} else if n != 1 {
		t.Errorf("audit rows for %s = %d, want 1", store.ActionDeckCaps, n)
	}

	// 再打开设置页：输入框显示新值，且带成功提示。
	page := getWithCookies(t, srv, "/decks/"+u64str(deck.ID)+"/settings?notice=saved", cookies)
	if page.Code != http.StatusOK {
		t.Fatalf("GET settings after update status = %d, want 200", page.Code)
	}
	if got := attrValue(page.Body.String(), "new_per_day"); got != "2" {
		t.Errorf("new_per_day after update = %q, want 2", got)
	}
	if !strings.Contains(page.Body.String(), "已保存") {
		t.Errorf("page does not show the saved notice: %s", snippet(page.Body.String()))
	}
}

// TestDeckSettingsZeroMeansUnlimited 断言 0 原样落库并表达「不限」：队列不再封顶，
// 页面的「剩余」显示不限文案而不是 0。
func TestDeckSettingsZeroMeansUnlimited(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Unlimited deck")
	if err := store.NewDeckStore(db).SetCaps(context.Background(), ownerID, deck.ID,
		store.DeckCaps{NewPerDay: 1, ReviewsPerDay: 1}); err != nil {
		t.Fatalf("SetCaps: %v", err)
	}
	for i := 0; i < 5; i++ {
		seedCardRow(t, db, deck.ID, "new", time.Now().UTC().Add(time.Duration(i)*time.Minute))
	}
	stability, difficulty := 5.0, 5.0
	last := time.Now().UTC().Add(-48 * time.Hour)
	for i := 0; i < 3; i++ {
		id := seedCardRow(t, db, deck.ID, "due", time.Now().UTC())
		due := time.Now().UTC().Add(-time.Hour)
		if err := db.Create(&store.CardState{CardID: id, UserID: ownerID, State: "review",
			DueAt: &due, Stability: &stability, Difficulty: &difficulty, LastReviewAt: &last}).Error; err != nil {
			t.Fatalf("create card state: %v", err)
		}
	}
	if n, r := deckQueueCountsNow(t, srv, db, ownerID, deck.ID); n != 1 || r != 1 {
		t.Fatalf("before update queue = %d new / %d review, want 1/1", n, r)
	}

	rec := postForm(t, srv, "/decks/"+u64str(deck.ID)+"/settings", url.Values{
		"csrf_token":      {csrf},
		"new_per_day":     {"0"},
		"reviews_per_day": {"0"},
	}, cookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST settings (0/0) status = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	// 0 必须原样存成 0，不能被 store 默认值（20/200）覆盖。
	if caps := deckCapsFromDB(t, db, deck.ID); caps.NewPerDay != 0 || caps.ReviewsPerDay != 0 {
		t.Fatalf("stored caps = %d/%d, want 0/0 (0 means unlimited, not the default)", caps.NewPerDay, caps.ReviewsPerDay)
	}
	if n, r := deckQueueCountsNow(t, srv, db, ownerID, deck.ID); n != 5 || r != 3 {
		t.Errorf("after update queue = %d new / %d review, want 5/3 (unlimited)", n, r)
	}

	page := getWithCookies(t, srv, "/decks/"+u64str(deck.ID)+"/settings", cookies)
	if page.Code != http.StatusOK {
		t.Fatalf("GET settings status = %d, want 200", page.Code)
	}
	body := page.Body.String()
	if got := attrValue(body, "new_per_day"); got != "0" {
		t.Errorf("new_per_day input value = %q, want 0", got)
	}
	if got := ddAfterLabel(body, "新卡剩余"); got != "不限" {
		t.Errorf("new-left = %q, want the unlimited label, not 0", got)
	}
	if got := ddAfterLabel(body, "复习剩余"); got != "不限" {
		t.Errorf("review-left = %q, want the unlimited label, not 0", got)
	}
}

// TestDeckSettingsRejectsNonOwner 是必测负例：非 owner 既打不开也改不了，且列值不变。
func TestDeckSettingsRejectsNonOwner(t *testing.T) {
	srv, db, ownerID, _, _ := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Owned deck")
	if err := store.NewDeckStore(db).SetCaps(context.Background(), ownerID, deck.ID,
		store.DeckCaps{NewPerDay: 7, ReviewsPerDay: 8}); err != nil {
		t.Fatalf("SetCaps: %v", err)
	}
	path := "/decks/" + u64str(deck.ID) + "/settings"
	_, u2Cookies, u2CSRF := createUserAndLogin(t, srv, db, "intruder")

	if rec := getWithCookies(t, srv, path, u2Cookies); rec.Code != http.StatusForbidden {
		t.Errorf("GET settings as non-owner status = %d, want 403 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	rec := postForm(t, srv, path, url.Values{
		"csrf_token":      {u2CSRF},
		"new_per_day":     {"0"},
		"reviews_per_day": {"0"},
	}, u2Cookies)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST settings as non-owner status = %d, want 403 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if caps := deckCapsFromDB(t, db, deck.ID); caps.NewPerDay != 7 || caps.ReviewsPerDay != 8 {
		t.Errorf("caps changed by a non-owner: got %d/%d, want 7/8", caps.NewPerDay, caps.ReviewsPerDay)
	}
}

// TestDeckSettingsRequiresCSRF 是必测负例：缺 CSRF 的写请求被中间件挡下，且列值不变。
func TestDeckSettingsRequiresCSRF(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "CSRF deck")
	if err := store.NewDeckStore(db).SetCaps(context.Background(), ownerID, deck.ID,
		store.DeckCaps{NewPerDay: 4, ReviewsPerDay: 4}); err != nil {
		t.Fatalf("SetCaps: %v", err)
	}
	rec := postForm(t, srv, "/decks/"+u64str(deck.ID)+"/settings", url.Values{
		"new_per_day": {"0"}, "reviews_per_day": {"0"},
	}, cookies)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST settings without CSRF status = %d, want 403 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if caps := deckCapsFromDB(t, db, deck.ID); caps.NewPerDay != 4 || caps.ReviewsPerDay != 4 {
		t.Errorf("caps changed despite missing CSRF: got %d/%d, want 4/4", caps.NewPerDay, caps.ReviewsPerDay)
	}
}

// TestDeckSettingsRejectsInvalidInput 是必测负例：非数字 / 负数 / 空一律 400、不写库，
// 并给出局部化错误文案。
func TestDeckSettingsRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name string
		form url.Values
	}{
		{name: "not a number", form: url.Values{"new_per_day": {"abc"}, "reviews_per_day": {"5"}}},
		{name: "negative", form: url.Values{"new_per_day": {"-1"}, "reviews_per_day": {"5"}}},
		{name: "empty new", form: url.Values{"new_per_day": {""}, "reviews_per_day": {"5"}}},
		{name: "missing both", form: url.Values{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, db, ownerID, cookies, csrf := newNotesServer(t)
			deck := seedReviewDeck(t, db, ownerID, "Invalid deck")
			if err := store.NewDeckStore(db).SetCaps(context.Background(), ownerID, deck.ID,
				store.DeckCaps{NewPerDay: 6, ReviewsPerDay: 6}); err != nil {
				t.Fatalf("SetCaps: %v", err)
			}
			form := url.Values{"csrf_token": {csrf}}
			for k, v := range tc.form {
				form[k] = v
			}
			rec := postForm(t, srv, "/decks/"+u64str(deck.ID)+"/settings", form, cookies)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("POST settings status = %d, want 400 (body %s)", rec.Code, snippet(rec.Body.String()))
			}
			if !strings.Contains(rec.Body.String(), "每日上限") {
				t.Errorf("invalid input did not render the localised error: %s", snippet(rec.Body.String()))
			}
			if caps := deckCapsFromDB(t, db, deck.ID); caps.NewPerDay != 6 || caps.ReviewsPerDay != 6 {
				t.Errorf("caps changed on invalid input: got %d/%d, want 6/6", caps.NewPerDay, caps.ReviewsPerDay)
			}
			// 被拒的请求零副作用：连审计都不该写。
			if n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionDeckCaps); err != nil {
				t.Fatalf("count audit rows: %v", err)
			} else if n != 0 {
				t.Errorf("audit rows for %s = %d, want 0 on a rejected update", store.ActionDeckCaps, n)
			}
		})
	}
}

// TestDeckSettingsInvalidDeckID 断言非法 / 不存在的 deck id 落到 404。
func TestDeckSettingsInvalidDeckID(t *testing.T) {
	srv, _, _, cookies, _ := newNotesServer(t)
	for _, path := range []string{"/decks/not-a-number/settings", "/decks/999999/settings"} {
		if rec := getWithCookies(t, srv, path, cookies); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404", path, rec.Code)
		}
	}
}
