package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"example.com/engram/internal/schedule"
	"example.com/engram/internal/store"
)

// postProfile 提交个人设置页的资料表单（M1-8）。所有字段都给全，避免用零值掩盖校验路径。
func postProfile(t *testing.T, srv *Server, cookies []*http.Cookie, csrf, display, locale, tz string, cutoff int) *httptest.ResponseRecorder {
	t.Helper()
	return postForm(t, srv, "/settings/profile", url.Values{
		"csrf_token":      {csrf},
		"display_name":    {display},
		"locale":          {locale},
		"timezone":        {tz},
		"day_cutoff_hour": {strconv.Itoa(cutoff)},
	}, cookies)
}

// rateNextCard 走真实的复习页流程给下一张到期卡评 Good，并返回刚写入的 reviews 行。
// 它复用页面上的隐藏字段，不手拼 expected_version，因此与真实浏览器路径一致。
func rateNextCard(t *testing.T, srv *Server, db *gorm.DB, cookies []*http.Cookie, csrf string, deckID uint64) store.Review {
	t.Helper()
	page := getWithCookies(t, srv, "/review?deck="+u64str(deckID), cookies)
	if page.Code != http.StatusOK {
		t.Fatalf("GET /review status = %d, want 200 (body %s)", page.Code, snippet(page.Body.String()))
	}
	body := page.Body.String()
	cardID := attrValue(body, "card_id")
	expected := attrValue(body, "expected_version")
	done := attrValue(body, "done")
	if cardID == "" {
		t.Fatalf("review page has no card to rate: %s", snippet(body))
	}
	rec := postForm(t, srv, "/review/answer", url.Values{
		"csrf_token":       {csrf},
		"card_id":          {cardID},
		"deck":             {u64str(deckID)},
		"rating":           {"3"},
		"expected_version": {expected},
		"done":             {done},
		"elapsed_ms":       {"120"},
	}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /review/answer status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var row store.Review
	if err := db.Order("id desc").First(&row).Error; err != nil {
		t.Fatalf("load review row: %v", err)
	}
	return row
}

// TestSettingsLocaleChangeSwitchesPageLanguage 是 M1-8 的第一条验收：
// 把界面语言从 zh-CN 改成 en 后，返回的设置页本身用英文渲染（<html lang="en">）。
func TestSettingsLocaleChangeSwitchesPageLanguage(t *testing.T) {
	srv, _, _, cookies, csrf := newNotesServer(t)

	// 先固定成中文，证明「改之前」确实是中文页面。
	if rec := postProfile(t, srv, cookies, csrf, "Owner", "zh-CN", "UTC", 4); rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /settings/profile (zh-CN) status = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	before := getWithCookies(t, srv, "/settings", cookies)
	if before.Code != http.StatusOK {
		t.Fatalf("GET /settings status = %d, want 200 (body %s)", before.Code, snippet(before.Body.String()))
	}
	if !strings.Contains(before.Body.String(), `<html lang="zh-CN">`) {
		t.Fatalf("page before the switch is not Chinese: %s", snippet(before.Body.String()))
	}

	// 改语言为 en：响应页面（以及随后的页面）都应是英文。
	if rec := postProfile(t, srv, cookies, csrf, "Owner", "en", "UTC", 4); rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /settings/profile (en) status = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	after := getWithCookies(t, srv, "/settings", cookies)
	if after.Code != http.StatusOK {
		t.Fatalf("GET /settings status = %d, want 200 (body %s)", after.Code, snippet(after.Body.String()))
	}
	if !strings.Contains(after.Body.String(), `<html lang="en">`) {
		t.Errorf("page after the switch is not English: %s", snippet(after.Body.String()))
	}
	if strings.Contains(after.Body.String(), "个人设置") {
		t.Errorf("page after the switch still renders Chinese catalog text")
	}
	t.Logf("GET /settings after setting locale=en renders <html lang=\"en\"> (was zh-CN before)")
}

// TestSettingsProfileRejectsInvalidTimezoneAndCutoff 是必测负例：
// 非法时区与越界切点都被拒绝并回填提示，绝不静默落库。
func TestSettingsProfileRejectsInvalidTimezoneAndCutoff(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)

	badTZ := postProfile(t, srv, cookies, csrf, "Owner", "en", "Not/AZone", 4)
	if badTZ.Code != http.StatusBadRequest {
		t.Fatalf("invalid timezone status = %d, want 400 (body %s)", badTZ.Code, snippet(badTZ.Body.String()))
	}
	badCutoff := postProfile(t, srv, cookies, csrf, "Owner", "en", "UTC", 99)
	if badCutoff.Code != http.StatusBadRequest {
		t.Fatalf("invalid cutoff status = %d, want 400 (body %s)", badCutoff.Code, snippet(badCutoff.Body.String()))
	}

	u, err := store.NewUserStore(db).ByID(t.Context(), ownerID)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if u.Timezone == "Not/AZone" || u.DayCutoffHour == 99 {
		t.Errorf("invalid values were persisted: timezone=%q cutoff=%d", u.Timezone, u.DayCutoffHour)
	}
}

// TestSettingsCutoffMovesReviewDayBoundary 是 M1-8 的第二条验收：
// 改复习日切点后，下一次真实评分写下的 reviews.review_day 按新切点划分。
//
// 为了让结果与真实时钟无关（半夜跑测试也不会翻车），时区选成一个 IANA 固定偏移区，
// 使用户本地时间落在中午 12 点：此时切点 4 与 13 必然分属不同复习日。
func TestSettingsCutoffMovesReviewDayBoundary(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Cutoff deck")
	seedBasic(t, db, deck.ID, "Q1", "A1")
	seedBasic(t, db, deck.ID, "Q2", "A2")

	offset := 12 - time.Now().UTC().Hour()
	zone := fmt.Sprintf("Etc/GMT%+d", -offset) // Etc/GMT-8 = UTC+8
	if _, err := time.LoadLocation(zone); err != nil {
		t.Fatalf("test timezone %q unavailable: %v", zone, err)
	}
	loc, _ := time.LoadLocation(zone)

	// 基准：切点 4。
	if rec := postProfile(t, srv, cookies, csrf, "Owner", "en", zone, 4); rec.Code != http.StatusSeeOther {
		t.Fatalf("set cutoff=4 status = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	first := rateNextCard(t, srv, db, cookies, csrf, deck.ID)
	if want := schedule.ReviewDay(first.ReviewedAt, loc, 4); first.ReviewDay != want {
		t.Fatalf("review_day with cutoff 4 = %q, want %q", first.ReviewDay, want)
	}

	// 改切点到 13：下一次评分必须用新切点。
	if rec := postProfile(t, srv, cookies, csrf, "Owner", "en", zone, 13); rec.Code != http.StatusSeeOther {
		t.Fatalf("set cutoff=13 status = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	u, err := store.NewUserStore(db).ByID(t.Context(), ownerID)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if u.DayCutoffHour != 13 || u.Timezone != zone {
		t.Fatalf("stored settings = (cutoff %d, tz %q), want (13, %q)", u.DayCutoffHour, u.Timezone, zone)
	}

	second := rateNextCard(t, srv, db, cookies, csrf, deck.ID)
	wantNew := schedule.ReviewDay(second.ReviewedAt, loc, 13)
	if second.ReviewDay != wantNew {
		t.Errorf("review_day after the cutoff change = %q, want %q", second.ReviewDay, wantNew)
	}
	if second.ReviewDay == schedule.ReviewDay(second.ReviewedAt, loc, 4) {
		t.Errorf("review_day %q did not move with the cutoff; the boundary is still 4", second.ReviewDay)
	}
	if first.ReviewDay == second.ReviewDay {
		t.Errorf("review_day stayed %q across the cutoff change", second.ReviewDay)
	}
	t.Logf("cutoff 4 -> review_day %s; cutoff 13 -> review_day %s (zone %s, reviewed_at %s)",
		first.ReviewDay, second.ReviewDay, zone, second.ReviewedAt.UTC().Format(time.RFC3339))
}

// TestSettingsPasswordChangeRevokesOtherSessions 是改密码的验收：
// 验旧密码、写新哈希、作废本人其它会话，同时保留当前会话。
func TestSettingsPasswordChangeRevokesOtherSessions(t *testing.T) {
	srv, db, ownerID, cookiesA, csrfA := newNotesServer(t)

	// 第二个会话：模拟另一台设备登录。
	loginB := postForm(t, srv, "/login", url.Values{
		"username": {"owner"}, "password": {"Sup3rSecret!"},
	}, nil)
	if loginB.Code != http.StatusSeeOther {
		t.Fatalf("second login status = %d, want 303 (body %s)", loginB.Code, snippet(loginB.Body.String()))
	}
	cookiesB := loginB.Result().Cookies()
	var sessionB store.Session
	if err := db.Order("created_at desc").First(&sessionB).Error; err != nil {
		t.Fatalf("load second session: %v", err)
	}

	// 错误旧密码：拒绝且不作废任何会话。
	wrong := postForm(t, srv, "/settings/password", url.Values{
		"csrf_token": {csrfA}, "old_password": {"WrongPass1!"}, "new_password": {"An0therSecret!"},
	}, cookiesA)
	if wrong.Code != http.StatusBadRequest {
		t.Fatalf("wrong old password status = %d, want 400 (body %s)", wrong.Code, snippet(wrong.Body.String()))
	}
	if err := db.First(&sessionB, "id = ?", sessionB.ID).Error; err != nil {
		t.Fatalf("reload session B: %v", err)
	}
	if sessionB.RevokedAt != nil {
		t.Errorf("a rejected password change revoked the other session")
	}

	// 正确旧密码：303 回设置页，其它会话作废，当前会话保留。
	ok := postForm(t, srv, "/settings/password", url.Values{
		"csrf_token": {csrfA}, "old_password": {"Sup3rSecret!"}, "new_password": {"An0therSecret!"},
	}, cookiesA)
	if ok.Code != http.StatusSeeOther {
		t.Fatalf("password change status = %d, want 303 (body %s)", ok.Code, snippet(ok.Body.String()))
	}

	var sessions []store.Session
	if err := db.Where("user_id = ?", ownerID).Find(&sessions).Error; err != nil {
		t.Fatalf("load sessions: %v", err)
	}
	var kept, revoked int
	for _, s := range sessions {
		if s.RevokedAt == nil {
			kept++
		} else {
			revoked++
		}
	}
	if kept != 1 || revoked != 1 {
		t.Errorf("sessions after the change: %d live, %d revoked; want 1 and 1", kept, revoked)
	}
	t.Logf("sessions after the change: %d live (current), %d revoked (other device)", kept, revoked)

	// 其它设备的下一个请求被拒（重定向到登录页），当前设备仍然可用。
	if rec := getWithCookies(t, srv, "/settings", cookiesB); rec.Code != http.StatusSeeOther {
		t.Errorf("revoked session GET /settings status = %d, want 303", rec.Code)
	}
	if rec := getWithCookies(t, srv, "/settings", cookiesA); rec.Code != http.StatusOK {
		t.Errorf("current session GET /settings status = %d, want 200", rec.Code)
	}

	// 新密码生效：旧密码不再能登录，新密码可以。
	if rec := postForm(t, srv, "/login", url.Values{"username": {"owner"}, "password": {"Sup3rSecret!"}}, nil); rec.Code == http.StatusSeeOther {
		t.Errorf("the old password still logs in after the change")
	}
	if rec := postForm(t, srv, "/login", url.Values{"username": {"owner"}, "password": {"An0therSecret!"}}, nil); rec.Code != http.StatusSeeOther {
		t.Errorf("the new password does not log in: status %d", rec.Code)
	}

	// 改密码写审计（AGENTS.md §2.5）。
	n, err := store.NewAuditStore(db).CountByAction(t.Context(), store.ActionUserPasswordChange)
	if err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if n != 1 {
		t.Errorf("audit rows for %s = %d, want 1", store.ActionUserPasswordChange, n)
	}
}

// TestPresetsNavLinkVisibleToSignedInUsers 是 M9-9 的验收：
// 已登录用户的页面上能找到 /presets 链接；匿名页面不暴露它。
func TestPresetsNavLinkVisibleToSignedInUsers(t *testing.T) {
	srv, _, _, cookies, _ := newNotesServer(t)

	for _, path := range []string{"/", "/settings"} {
		page := getWithCookies(t, srv, path, cookies)
		if page.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200 (body %s)", path, page.Code, snippet(page.Body.String()))
		}
		if !strings.Contains(page.Body.String(), `href="/presets"`) {
			t.Errorf("GET %s for a signed-in user does not link to /presets: %s", path, snippet(page.Body.String()))
		}
		t.Logf("GET %s for a signed-in user contains href=\"/presets\"", path)
	}

	anon := getWithCookies(t, srv, "/", nil)
	if anon.Code != http.StatusOK {
		t.Fatalf("GET / (anonymous) status = %d, want 200", anon.Code)
	}
	if strings.Contains(anon.Body.String(), `href="/presets"`) {
		t.Errorf("anonymous page advertises /presets: %s", snippet(anon.Body.String()))
	}
}
