package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/reminder"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是 SPA 邮件通知偏好接口（internal/web/spa_mail_prefs.go）的端到端证据：
// 走真实路由 + 真实 SQLite，复用 store.EmailPrefStore 与 users 行，断言与 SSR 页一致的行为。
//
// 覆盖的重点：
//   - GET 的分组与开关严格来自 internal/mail 目录（既不多也不少，不暴露目录外类型）；
//   - PATCH 为每个可关闭类型写显式选择，A 类 / 未知类型被拒且不产生半截保存；
//   - reminder_hour 的 null 与 0（午夜）可区分，越界被拒；
//   - 写入过会话 CSRF，且只接受浏览器会话。

// spaNotificationPatch 带会话 cookie 与 X-CSRF-Token 头提交 PATCH JSON。
// 与 postSPAJSON 同形，只是方法为 PATCH（邮件偏好接口用 PATCH 保存）。
func spaNotificationPatch(t *testing.T, srv *Server, path string, body any, cookies []*http.Cookie, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPatch, path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		req.Header.Set(auth.CSRFHeaderName, csrf)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// loadSPANotificationPrefs 读取接口响应并解码；状态码不是 200 直接失败。
func loadSPANotificationPrefs(t *testing.T, rec *httptest.ResponseRecorder) spaNotificationPrefsResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("request status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var got spaNotificationPrefsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v (body %s)", err, snippet(rec.Body.String()))
	}
	return got
}

// TestSPAMailPrefsGetMatchesCatalog 是读取侧的目录一致性验收：
// 分组顺序等于 ClassOrder，类型集合与顺序等于 mail.Catalog，locked/enabled 由 CanDisable/ResolveEnabled 推导。
func TestSPAMailPrefsGetMatchesCatalog(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)

	got := loadSPANotificationPrefs(t, getWithCookies(t, srv, "/api/v1/settings/notifications", cookies))

	classes := mail.ClassOrder()
	if len(got.Groups) != len(classes) {
		t.Fatalf("group count = %d, want %d (one per class)", len(got.Groups), len(classes))
	}
	catalog := mail.Catalog()
	index := 0
	for i, class := range classes {
		if got.Groups[i].Class != string(class) {
			t.Errorf("group %d class = %q, want %q", i, got.Groups[i].Class, class)
		}
		for _, item := range got.Groups[i].Types {
			if index >= len(catalog) {
				t.Fatalf("response has more types than the catalog: extra %q", item.Type)
			}
			def := catalog[index]
			index++
			if item.Type != string(def.Type) {
				t.Errorf("type %d = %q, want %q (catalog order)", index-1, item.Type, def.Type)
			}
			if want := mail.ResolveEnabled(map[string]bool{}, def.Type); item.Enabled != want {
				t.Errorf("type %q enabled = %v, want %v (directory default)", item.Type, item.Enabled, want)
			}
			if want := !mail.CanDisable(def.Class); item.Locked != want {
				t.Errorf("type %q locked = %v, want %v", item.Type, item.Locked, want)
			}
		}
	}
	if index != len(catalog) {
		t.Fatalf("response covered %d types, want the whole catalog of %d", index, len(catalog))
	}

	if got.ReminderHour != nil {
		t.Errorf("initial reminder_hour = %d, want null (unset)", *got.ReminderHour)
	}
	if got.DefaultReminderHour != reminder.DefaultSendHour {
		t.Errorf("default_reminder_hour = %d, want %d", got.DefaultReminderHour, reminder.DefaultSendHour)
	}
	fresh, err := store.NewUserStore(db).ByID(context.Background(), ownerID)
	if err != nil {
		t.Fatalf("load owner: %v", err)
	}
	if got.Timezone != fresh.Timezone {
		t.Errorf("timezone = %q, want the user's %q", got.Timezone, fresh.Timezone)
	}
}

// TestSPAMailPrefsPatchRoundTrips 验收「保存后立即生效、显式选择覆盖每个可关闭类型」：
// 打开一个默认关的 C 类、关闭一个默认开的 B 类，其余可关闭类型写成关闭；重启读取路径仍一致。
func TestSPAMailPrefsPatchRoundTrips(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)

	rec := spaNotificationPatch(t, srv, "/api/v1/settings/notifications", map[string]any{
		"choices": map[string]bool{
			string(mail.TypeReviewReminder): true,  // C：打开
			string(mail.TypeDeckShared):     false, // B：关闭
		},
		"reminder_hour": 7,
	}, cookies, csrf)
	got := loadSPANotificationPrefs(t, rec)

	// 响应立刻反映新状态：C 开、B 关，A 仍恒开。
	if !enabledOf(got, mail.TypeReviewReminder) {
		t.Error("response says review_reminder is off after it was turned on")
	}
	if enabledOf(got, mail.TypeDeckShared) {
		t.Error("response says deck_shared is on after it was turned off")
	}
	if !enabledOf(got, mail.TypePasswordReset) || !lockedOf(got, mail.TypePasswordReset) {
		t.Error("class A password_reset must stay enabled and locked")
	}
	if got.ReminderHour == nil || *got.ReminderHour != 7 {
		t.Fatalf("response reminder_hour = %v, want 7", got.ReminderHour)
	}

	// 存储侧：新实例读回（等价于进程重启后的读取路径）。
	choices, err := store.NewEmailPrefStore(db).Choices(context.Background(), ownerID)
	if err != nil {
		t.Fatalf("Choices() error = %v", err)
	}
	if !choices[string(mail.TypeReviewReminder)] {
		t.Errorf("stored choices = %v, want review_reminder=true", choices)
	}
	if choices[string(mail.TypeDeckShared)] {
		t.Errorf("stored choices = %v, want deck_shared=false", choices)
	}
	// 每个可关闭类型都必须有一条显式选择（缺席 = 关闭），数量与目录里可关闭类型一致。
	wantDisableable := 0
	for _, def := range mail.Catalog() {
		if mail.CanDisable(def.Class) {
			wantDisableable++
		}
	}
	if len(choices) != wantDisableable {
		t.Errorf("stored choice count = %d, want %d (one per disableable type)", len(choices), wantDisableable)
	}
	u, err := store.NewUserStore(db).ByID(context.Background(), ownerID)
	if err != nil {
		t.Fatalf("load owner: %v", err)
	}
	if u.ReminderHour == nil || *u.ReminderHour != 7 {
		t.Fatalf("stored reminder_hour = %v, want 7", u.ReminderHour)
	}

	if n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionUserEmailPrefsUpdate); err != nil || n != 1 {
		t.Fatalf("user.email_prefs_update audit = (%d, %v), want 1", n, err)
	}
}

// TestSPAMailPrefsReminderHourNullAndMidnight 验收发送小时的三个关键点：
// 0 存成 0（午夜，绝不是 NULL）、null 存成 NULL、越界以 400 拒绝且不改动已存值。
func TestSPAMailPrefsReminderHourNullAndMidnight(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	users := store.NewUserStore(db)
	load := func() *store.User {
		t.Helper()
		u, err := users.ByID(context.Background(), ownerID)
		if err != nil {
			t.Fatalf("load owner: %v", err)
		}
		return u
	}

	// 0 点：必须存成 0，不能和「未设置」混在一起。
	if rec := spaNotificationPatch(t, srv, "/api/v1/settings/notifications", map[string]any{"reminder_hour": 0}, cookies, csrf); rec.Code != http.StatusOK {
		t.Fatalf("saving hour 0 status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if u := load(); u.ReminderHour == nil {
		t.Fatal("explicit 0 was stored as NULL: midnight and unset are conflated")
	} else if *u.ReminderHour != 0 {
		t.Fatalf("stored reminder_hour = %d, want 0", *u.ReminderHour)
	}

	// null：回到站点默认（NULL）。
	if rec := spaNotificationPatch(t, srv, "/api/v1/settings/notifications", map[string]any{"reminder_hour": nil}, cookies, csrf); rec.Code != http.StatusOK {
		t.Fatalf("saving the site default status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if u := load(); u.ReminderHour != nil {
		t.Fatalf("after choosing the site default reminder_hour = %d, want NULL", *u.ReminderHour)
	}

	// 越界：400，且不改动已存值（仍为 NULL）。
	rec := spaNotificationPatch(t, srv, "/api/v1/settings/notifications", map[string]any{"reminder_hour": 24}, cookies, csrf)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("saving hour 24 status = %d, want 400 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if !strings.Contains(rec.Body.String(), "reminder_hour_invalid") {
		t.Errorf("rejection does not carry the reminder_hour_invalid code: %s", snippet(rec.Body.String()))
	}
	if u := load(); u.ReminderHour != nil {
		t.Fatalf("a rejected reminder_hour wrote %d, want no change (NULL)", *u.ReminderHour)
	}
}

// TestSPAMailPrefsRejectsClassA 是「拒绝关闭 A 类」的负例：指向 A 类的提交 400，且不写任何行。
func TestSPAMailPrefsRejectsClassA(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)

	rec := spaNotificationPatch(t, srv, "/api/v1/settings/notifications", map[string]any{
		"choices": map[string]bool{string(mail.TypePasswordReset): true},
	}, cookies, csrf)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("disabling a class A type status = %d, want 400 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if !strings.Contains(rec.Body.String(), "class_locked") {
		t.Errorf("rejection does not carry the class_locked code: %s", snippet(rec.Body.String()))
	}
	choices, err := store.NewEmailPrefStore(db).Choices(context.Background(), ownerID)
	if err != nil {
		t.Fatalf("Choices() error = %v", err)
	}
	if len(choices) != 0 {
		t.Errorf("a rejected class A submission wrote choices: %v", choices)
	}
	if n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionUserEmailPrefsUpdate); err != nil || n != 0 {
		t.Fatalf("audit after a rejected submission = (%d, %v), want 0", n, err)
	}
}

// TestSPAMailPrefsRejectsUnknownType 断言目录外的类型被拒：不暴露、不接受、不写库。
func TestSPAMailPrefsRejectsUnknownType(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)

	rec := spaNotificationPatch(t, srv, "/api/v1/settings/notifications", map[string]any{
		"choices": map[string]bool{"totally_made_up": true},
	}, cookies, csrf)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown type status = %d, want 400 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if !strings.Contains(rec.Body.String(), "unknown_type") {
		t.Errorf("rejection does not carry the unknown_type code: %s", snippet(rec.Body.String()))
	}
	choices, err := store.NewEmailPrefStore(db).Choices(context.Background(), ownerID)
	if err != nil {
		t.Fatalf("Choices() error = %v", err)
	}
	if len(choices) != 0 {
		t.Errorf("a rejected unknown-type submission wrote choices: %v", choices)
	}
}

// TestSPAMailPrefsRejectsMissingCSRF 断言保存必须带会话绑定的 CSRF token，缺失即被拒且不写入。
func TestSPAMailPrefsRejectsMissingCSRF(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)

	rec := spaNotificationPatch(t, srv, "/api/v1/settings/notifications", map[string]any{
		"choices": map[string]bool{string(mail.TypeInvite): true},
	}, cookies, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("PATCH without csrf status = %d, want 403 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	choices, err := store.NewEmailPrefStore(db).Choices(context.Background(), ownerID)
	if err != nil {
		t.Fatalf("Choices() error = %v", err)
	}
	if len(choices) != 0 {
		t.Errorf("a CSRF-less request wrote choices: %v", choices)
	}
}

// TestSPAMailPrefsRejectsAnonymousAndBearer 断言接口只接受浏览器会话，读写都不接受 bearer 凭据。
func TestSPAMailPrefsRejectsAnonymousAndBearer(t *testing.T) {
	srv, db, ownerID, _, _ := newNotesServer(t)

	if rec := getWithCookies(t, srv, "/api/v1/settings/notifications", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous GET = %d, want 401", rec.Code)
	}
	if rec := spaNotificationPatch(t, srv, "/api/v1/settings/notifications", map[string]any{}, nil, ""); rec.Code != http.StatusForbidden {
		t.Errorf("anonymous PATCH = %d, want 403 CSRF denial", rec.Code)
	}

	created, err := store.NewAPIKeyStore(db).Create(context.Background(), store.CreateAPIKeyParams{
		UserID: ownerID, Name: "spa mail prefs test", Scopes: []string{store.ScopeRead},
	})
	if err != nil {
		t.Fatal(err)
	}
	bearer := httptest.NewRequest(http.MethodGet, "/api/v1/settings/notifications", nil)
	bearer.Header.Set("Authorization", "Bearer "+created.Plaintext)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, bearer)
	if rec.Code != http.StatusForbidden {
		t.Errorf("bearer GET = %d, want 403", rec.Code)
	}
}

// TestSPAMailPrefsFrontendCatalogsCoverCatalog 是跨端目录一致性验收：
// 目录里每个类型与大类在 SPA 的两套语言包里都必须有对应标签键，
// 否则前端会渲染出裸 key。目录是唯一来源，前端不得另列一份类型清单。
func TestSPAMailPrefsFrontendCatalogsCoverCatalog(t *testing.T) {
	files := map[string]string{
		"en":    filepath.Join("..", "..", "frontend", "src", "lib", "i18n", "locales", "en.ts"),
		"zh-CN": filepath.Join("..", "..", "frontend", "src", "lib", "i18n", "locales", "zh-CN.ts"),
	}
	bodies := make(map[string]string, len(files))
	for locale, path := range files {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s catalog %s: %v", locale, path, err)
		}
		bodies[locale] = string(b)
	}
	keys := []string{"settings.notifications.heading", "settings.notifications.locked", "settings.notifications.submit"}
	for _, class := range mail.ClassOrder() {
		keys = append(keys, "settings.notifications.class."+string(class)+".heading")
	}
	for _, def := range mail.Catalog() {
		keys = append(keys, "settings.notifications.type."+string(def.Type))
	}
	for locale, body := range bodies {
		for _, key := range keys {
			if !strings.Contains(body, "'"+key+"'") {
				t.Errorf("%s.ts is missing the SPA notification key %q", locale, key)
			}
		}
	}
}

// enabledOf 报告响应里某类型的有效开关；类型缺失时直接失败。
func enabledOf(resp spaNotificationPrefsResponse, typ mail.Type) bool {
	for _, group := range resp.Groups {
		for _, item := range group.Types {
			if item.Type == string(typ) {
				return item.Enabled
			}
		}
	}
	return false
}

// lockedOf 报告响应里某类型是否被锁；类型缺失时直接失败。
func lockedOf(resp spaNotificationPrefsResponse, typ mail.Type) bool {
	for _, group := range resp.Groups {
		for _, item := range group.Types {
			if item.Type == string(typ) {
				return item.Locked
			}
		}
	}
	return false
}
