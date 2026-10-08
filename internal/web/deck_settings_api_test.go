package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// deckSettingsPath 返回 SPA 卡组设置接口的路径。
func deckSettingsPath(deckID uint64) string {
	return "/api/v1/decks/" + u64str(deckID) + "/settings"
}

// decodeDeckSettings 解析响应体；失败即终止测试。
func decodeDeckSettings(t *testing.T, raw []byte) deckSettingsResponse {
	t.Helper()
	var body deckSettingsResponse
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode SPA deck settings: %v", err)
	}
	return body
}

// seedQueueCards 写入 n 张新卡与 m 张到期的 review 卡，供队列/额度断言使用。
func seedQueueCards(t *testing.T, db *gorm.DB, ownerID, deckID uint64, newCards, dueCards int) {
	t.Helper()
	now := time.Now().UTC()
	for i := 0; i < newCards; i++ {
		seedCardRow(t, db, deckID, "new", now.Add(time.Duration(i)*time.Minute))
	}
	stability, difficulty := 5.0, 5.0
	last := now.Add(-48 * time.Hour)
	for i := 0; i < dueCards; i++ {
		id := seedCardRow(t, db, deckID, "due", now)
		due := now.Add(-time.Hour)
		if err := db.Create(&store.CardState{CardID: id, UserID: ownerID, State: "review",
			DueAt: &due, Stability: &stability, Difficulty: &difficulty, LastReviewAt: &last}).Error; err != nil {
			t.Fatalf("create card state: %v", err)
		}
	}
}

// TestDeckSettingsOwnerViewReportsCapsAndUsage 是设置接口的读验收：owner 拿到的
// new_per_day / reviews_per_day 是库里的原值，今日已用/剩余来自 schedule.DeckBudgets。
func TestDeckSettingsOwnerViewReportsCapsAndUsage(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "SPA settings deck")
	if err := store.NewDeckStore(db).SetCaps(context.Background(), ownerID, deck.ID,
		store.DeckCaps{NewPerDay: 5, ReviewsPerDay: 10}); err != nil {
		t.Fatalf("SetCaps: %v", err)
	}
	// 今日已引入 2 张新卡、复习 3 张 → 新卡剩余 3、复习剩余 7。
	seedTodayUsage(t, db, ownerID, deck.ID, 2, 3)

	rec := getWithCookies(t, srv, deckSettingsPath(deck.ID), cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200 (body %s)", deckSettingsPath(deck.ID), rec.Code, snippet(rec.Body.String()))
	}
	body := decodeDeckSettings(t, rec.Body.Bytes())
	if body.DeckID != deck.ID || body.DeckName != "SPA settings deck" {
		t.Errorf("deck identity = %d/%q, want %d/%q", body.DeckID, body.DeckName, deck.ID, deck.Name)
	}
	if body.NewPerDay != 5 || body.ReviewsPerDay != 10 {
		t.Errorf("caps = %d/%d, want 5/10 (verbatim column values)", body.NewPerDay, body.ReviewsPerDay)
	}
	if body.NewUsed != 2 || body.ReviewUsed != 3 {
		t.Errorf("used = %d/%d, want 2/3", body.NewUsed, body.ReviewUsed)
	}
	if body.NewLeft != 3 || body.ReviewLeft != 7 {
		t.Errorf("left = %d/%d, want 3/7 (cap minus used)", body.NewLeft, body.ReviewLeft)
	}
	if body.NewUnlimited || body.ReviewUnlimited {
		t.Errorf("unlimited = %v/%v, want false/false for non-zero caps", body.NewUnlimited, body.ReviewUnlimited)
	}
}

// TestDeckSettingsUpdateTakesEffectAndAudits 是写验收：PATCH 改小上限后库里的两列
// 立刻变成提交值，队列随之变小，并留一条 deck.caps_change 审计。
func TestDeckSettingsUpdateTakesEffectAndAudits(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "SPA capped deck")
	if err := store.NewDeckStore(db).SetCaps(context.Background(), ownerID, deck.ID,
		store.DeckCaps{NewPerDay: 5, ReviewsPerDay: 5}); err != nil {
		t.Fatalf("SetCaps: %v", err)
	}
	seedQueueCards(t, db, ownerID, deck.ID, 5, 3)
	if n, r := deckQueueCountsNow(t, srv, db, ownerID, deck.ID); n != 5 || r != 3 {
		t.Fatalf("before update queue = %d new / %d review, want 5/3", n, r)
	}

	rec := jsonRequest(t, srv, http.MethodPatch, deckSettingsPath(deck.ID),
		`{"new_per_day":2,"reviews_per_day":1}`, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if caps := deckCapsFromDB(t, db, deck.ID); caps.NewPerDay != 2 || caps.ReviewsPerDay != 1 {
		t.Fatalf("stored caps = %d/%d, want 2/1", caps.NewPerDay, caps.ReviewsPerDay)
	}
	if n, r := deckQueueCountsNow(t, srv, db, ownerID, deck.ID); n != 2 || r != 1 {
		t.Errorf("after update queue = %d new / %d review, want 2/1 (effective immediately)", n, r)
	}
	if n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionDeckCaps); err != nil {
		t.Fatalf("count audit rows: %v", err)
	} else if n != 1 {
		t.Errorf("audit rows for %s = %d, want 1", store.ActionDeckCaps, n)
	}
	body := decodeDeckSettings(t, rec.Body.Bytes())
	if body.NewPerDay != 2 || body.ReviewsPerDay != 1 {
		t.Errorf("PATCH response caps = %d/%d, want 2/1", body.NewPerDay, body.ReviewsPerDay)
	}
}

// TestDeckSettingsZeroRoundTripsAsUnlimited 断言 0 原样落库并显式表达「不限」：
// 响应里 new_per_day=0 且 unlimited=true，队列不再封顶。
func TestDeckSettingsZeroRoundTripsAsUnlimited(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "SPA unlimited deck")
	if err := store.NewDeckStore(db).SetCaps(context.Background(), ownerID, deck.ID,
		store.DeckCaps{NewPerDay: 1, ReviewsPerDay: 1}); err != nil {
		t.Fatalf("SetCaps: %v", err)
	}
	seedQueueCards(t, db, ownerID, deck.ID, 5, 3)
	if n, r := deckQueueCountsNow(t, srv, db, ownerID, deck.ID); n != 1 || r != 1 {
		t.Fatalf("before update queue = %d new / %d review, want 1/1", n, r)
	}

	rec := jsonRequest(t, srv, http.MethodPatch, deckSettingsPath(deck.ID),
		`{"new_per_day":0,"reviews_per_day":0}`, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH 0/0 = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	// 0 必须原样存成 0，不能被 store 默认值（20/200）覆盖。
	if caps := deckCapsFromDB(t, db, deck.ID); caps.NewPerDay != 0 || caps.ReviewsPerDay != 0 {
		t.Fatalf("stored caps = %d/%d, want 0/0 (0 means unlimited, not the default)", caps.NewPerDay, caps.ReviewsPerDay)
	}
	body := decodeDeckSettings(t, rec.Body.Bytes())
	if body.NewPerDay != 0 || body.ReviewsPerDay != 0 {
		t.Errorf("response caps = %d/%d, want 0/0", body.NewPerDay, body.ReviewsPerDay)
	}
	if !body.NewUnlimited || !body.ReviewUnlimited {
		t.Errorf("unlimited = %v/%v, want true/true (0 must be expressed explicitly)", body.NewUnlimited, body.ReviewUnlimited)
	}
	if body.NewLeft != 0 || body.ReviewLeft != 0 {
		t.Errorf("left = %d/%d, want 0/0 with unlimited set", body.NewLeft, body.ReviewLeft)
	}
	if n, r := deckQueueCountsNow(t, srv, db, ownerID, deck.ID); n != 5 || r != 3 {
		t.Errorf("after update queue = %d new / %d review, want 5/3 (unlimited)", n, r)
	}
}

// TestDeckSettingsRejectsNonOwner 是必测负例：非 owner 读不了也改不了，且列值不变、
// 不写 deck.caps_change 审计。
func TestDeckSettingsRejectsNonOwner(t *testing.T) {
	srv, db, ownerID, _, _ := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "SPA owned deck")
	if err := store.NewDeckStore(db).SetCaps(context.Background(), ownerID, deck.ID,
		store.DeckCaps{NewPerDay: 7, ReviewsPerDay: 8}); err != nil {
		t.Fatalf("SetCaps: %v", err)
	}
	_, u2Cookies, u2CSRF := createUserAndLogin(t, srv, db, "settings_intruder")

	if rec := getWithCookies(t, srv, deckSettingsPath(deck.ID), u2Cookies); rec.Code != http.StatusForbidden {
		t.Errorf("non-owner GET = %d, want 403 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	rec := jsonRequest(t, srv, http.MethodPatch, deckSettingsPath(deck.ID),
		`{"new_per_day":0,"reviews_per_day":0}`, u2Cookies, u2CSRF)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-owner PATCH = %d, want 403 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if caps := deckCapsFromDB(t, db, deck.ID); caps.NewPerDay != 7 || caps.ReviewsPerDay != 8 {
		t.Errorf("caps changed by a non-owner: got %d/%d, want 7/8", caps.NewPerDay, caps.ReviewsPerDay)
	}
	if n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionDeckCaps); err != nil {
		t.Fatalf("count audit rows: %v", err)
	} else if n != 0 {
		t.Errorf("deck.caps_change audit rows = %d, want 0 after a denied write", n)
	}
}

// TestDeckSettingsRequiresCSRF 是必测负例：缺 CSRF 的 PATCH 被中间件挡下，列值不变。
func TestDeckSettingsRequiresCSRF(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "SPA csrf deck")
	if err := store.NewDeckStore(db).SetCaps(context.Background(), ownerID, deck.ID,
		store.DeckCaps{NewPerDay: 4, ReviewsPerDay: 4}); err != nil {
		t.Fatalf("SetCaps: %v", err)
	}
	rec := jsonRequest(t, srv, http.MethodPatch, deckSettingsPath(deck.ID),
		`{"new_per_day":0,"reviews_per_day":0}`, cookies, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("PATCH without CSRF = %d, want 403 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if caps := deckCapsFromDB(t, db, deck.ID); caps.NewPerDay != 4 || caps.ReviewsPerDay != 4 {
		t.Errorf("caps changed despite missing CSRF: got %d/%d, want 4/4", caps.NewPerDay, caps.ReviewsPerDay)
	}
	if n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionDeckCaps); err != nil {
		t.Fatalf("count audit rows: %v", err)
	} else if n != 0 {
		t.Errorf("deck.caps_change audit rows = %d, want 0 when CSRF is missing", n)
	}
}

// TestDeckSettingsRejectsInvalidInput 是必测负例：非数字 / 负数 / 缺字段 / 空体一律
// 400、不写库、不写审计。
func TestDeckSettingsRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "negative new", body: `{"new_per_day":-1,"reviews_per_day":5}`},
		{name: "negative reviews", body: `{"new_per_day":5,"reviews_per_day":-2}`},
		{name: "not a number", body: `{"new_per_day":"abc","reviews_per_day":5}`},
		{name: "missing new", body: `{"reviews_per_day":5}`},
		{name: "missing both", body: `{}`},
		{name: "empty body", body: ``},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, db, ownerID, cookies, csrf := newNotesServer(t)
			deck := seedReviewDeck(t, db, ownerID, "SPA invalid deck")
			if err := store.NewDeckStore(db).SetCaps(context.Background(), ownerID, deck.ID,
				store.DeckCaps{NewPerDay: 6, ReviewsPerDay: 6}); err != nil {
				t.Fatalf("SetCaps: %v", err)
			}
			rec := jsonRequest(t, srv, http.MethodPatch, deckSettingsPath(deck.ID), tc.body, cookies, csrf)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("PATCH %s = %d, want 400 (body %s)", tc.body, rec.Code, snippet(rec.Body.String()))
			}
			if caps := deckCapsFromDB(t, db, deck.ID); caps.NewPerDay != 6 || caps.ReviewsPerDay != 6 {
				t.Errorf("caps changed on invalid input: got %d/%d, want 6/6", caps.NewPerDay, caps.ReviewsPerDay)
			}
			// 被拒的请求零副作用：连审计都不该写。
			if n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionDeckCaps); err != nil {
				t.Fatalf("count audit rows: %v", err)
			} else if n != 0 {
				t.Errorf("deck.caps_change audit rows = %d, want 0 on a rejected update", n)
			}
		})
	}
}

// TestDeckSettingsRequiresSession 断言匿名访问拿到 401，而不是 SSR 页面的 303 跳转。
func TestDeckSettingsRequiresSession(t *testing.T) {
	srv, db, ownerID, _, _ := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "SPA anon deck")
	rec := getWithCookies(t, srv, deckSettingsPath(deck.ID), nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous GET = %d, want 401 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error.Code != "unauthorized" {
		t.Errorf("anonymous error code = %q, want unauthorized", body.Error.Code)
	}
}

// TestDeckSettingsSwitchesPreset 是卡组切换调度的写验收：PATCH 带 preset_id 时卡组改按
// 新预设排程，响应回显新值，并且留下一条 deck.preset_change 审计。
func TestDeckSettingsSwitchesPreset(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "SPA switchable deck")
	second := store.NewPreset(ownerID, "second preset")
	if err := store.NewPresetStore(db).Create(context.Background(), &second); err != nil {
		t.Fatalf("create second preset: %v", err)
	}

	rec := jsonRequest(t, srv, http.MethodPatch, deckSettingsPath(deck.ID),
		fmt.Sprintf(`{"new_per_day":2,"reviews_per_day":3,"preset_id":%d}`, second.ID), cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	body := decodeDeckSettings(t, rec.Body.Bytes())
	if body.PresetID != second.ID {
		t.Errorf("response preset_id = %d, want %d", body.PresetID, second.ID)
	}
	stored, err := store.NewDeckStore(db).ByID(context.Background(), deck.ID)
	if err != nil {
		t.Fatalf("read deck: %v", err)
	}
	if stored.PresetID != second.ID {
		t.Errorf("stored preset_id = %d, want %d", stored.PresetID, second.ID)
	}
	if n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionDeckPreset); err != nil {
		t.Fatalf("count audit rows: %v", err)
	} else if n != 1 {
		t.Errorf("audit rows for %s = %d, want 1", store.ActionDeckPreset, n)
	}
}

// TestDeckSettingsRejectsForeignPreset 是必测负例：挂别人的预设会连带把他人调好的
// 排程参数读出来，必须 400 拒绝，且卡组的 preset_id 一个字节都不动、不写审计。
func TestDeckSettingsRejectsForeignPreset(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "SPA foreign preset deck")
	intruder := store.User{Username: "preset_intruder", Email: "preset_intruder@example.com",
		DisplayName: "intruder", Role: store.RoleUser, Status: store.StatusActive,
		Locale: "zh-CN", Timezone: "Asia/Shanghai", CreatedAt: time.Now().UTC()}
	if err := db.Create(&intruder).Error; err != nil {
		t.Fatalf("create intruder: %v", err)
	}
	foreign := store.NewPreset(intruder.ID, "foreign preset")
	if err := store.NewPresetStore(db).Create(context.Background(), &foreign); err != nil {
		t.Fatalf("create foreign preset: %v", err)
	}

	rec := jsonRequest(t, srv, http.MethodPatch, deckSettingsPath(deck.ID),
		fmt.Sprintf(`{"new_per_day":2,"reviews_per_day":3,"preset_id":%d}`, foreign.ID), cookies, csrf)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH foreign preset = %d, want 400 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if envelope.Error.Code != "invalid_request" {
		t.Errorf("error code = %q, want invalid_request", envelope.Error.Code)
	}
	stored, err := store.NewDeckStore(db).ByID(context.Background(), deck.ID)
	if err != nil {
		t.Fatalf("read deck: %v", err)
	}
	if stored.PresetID != deck.PresetID {
		t.Errorf("stored preset_id = %d, want %d (unchanged after rejection)", stored.PresetID, deck.PresetID)
	}
	if n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionDeckPreset); err != nil {
		t.Fatalf("count audit rows: %v", err)
	} else if n != 0 {
		t.Errorf("audit rows for %s = %d, want 0 (no write happened)", store.ActionDeckPreset, n)
	}
}
