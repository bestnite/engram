package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

// setOwnerLocale 直接把 owner 的界面语言写成 code，让偏好页以可断言的文案渲染。
func setOwnerLocale(t *testing.T, db *gorm.DB, userID uint64, code string) {
	t.Helper()
	users := store.NewUserStore(db)
	u, err := users.ByID(context.Background(), userID)
	if err != nil {
		t.Fatalf("load owner: %v", err)
	}
	u.Locale = code
	if err := users.Update(context.Background(), u); err != nil {
		t.Fatalf("update owner locale: %v", err)
	}
}

// postMailPrefs 提交邮件偏好表单；choices 的键是 mail.Type，值 true 表示勾选（开启）。
// 未列出的可关闭类型按「复选框缺席 = 关闭」处理，与服务端一致。
func postMailPrefs(t *testing.T, srv *Server, cookies []*http.Cookie, csrf string, choices map[mail.Type]bool) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"csrf_token": {csrf}}
	for typ, on := range choices {
		if on {
			form.Set("mail_pref."+string(typ), "on")
		}
	}
	return postForm(t, srv, "/settings/notifications", form, cookies)
}

// checkboxState 报告页面里名为 name 的复选框是否存在、是否勾选。
// 只看该标签自身（到第一个 "/>" 为止），避免被后面的 "checked" 串味。
func checkboxState(body, name string) (found, checked bool) {
	marker := `name="` + name + `"`
	i := strings.Index(body, marker)
	if i < 0 {
		return false, false
	}
	rest := body[i:]
	// 只看该标签自身：到标签结束的 ">" 为止（templ 渲染 void 元素用 ">"，不是 "/>"）。
	if end := strings.Index(rest, ">"); end >= 0 {
		rest = rest[:end]
	}
	return true, strings.Contains(rest, "checked")
}

// TestMailPrefsPageShowsAllClassesAndLocksClassA 是偏好页的读取验收：
// 四类都在页面上；A 类渲染成禁用复选框且没有可提交的输入名；B 默认勾选、C 默认不勾选。
func TestMailPrefsPageShowsAllClassesAndLocksClassA(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	setOwnerLocale(t, db, ownerID, "en")

	page := getWithCookies(t, srv, "/settings/notifications", cookies)
	if page.Code != http.StatusOK {
		t.Fatalf("GET /settings/notifications status = %d, want 200 (body %s)", page.Code, snippet(page.Body.String()))
	}
	body := page.Body.String()

	for _, want := range []string{
		"Security and transactional",
		"Collaboration and permissions",
		"Study and operations",
		"Administrator notices",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("preferences page is missing class heading %q", want)
		}
	}

	// A 类：禁用复选框（checked disabled），且绝不出现可提交的输入名。
	if !strings.Contains(body, "checked disabled") {
		t.Error("class A types are not rendered as disabled checkboxes")
	}
	for _, typ := range []mail.Type{
		mail.TypePasswordReset, mail.TypeEmailVerification, mail.TypeNewDeviceLogin,
		mail.TypeCredentialChanged, mail.TypeAccountStatus,
	} {
		if strings.Contains(body, `name="mail_pref.`+string(typ)+`"`) {
			t.Errorf("class A type %q is rendered as a submittable input", typ)
		}
	}

	// 默认值：B 默认开 → 勾选；C 默认关 → 未勾选。
	if found, checked := checkboxState(body, "mail_pref."+string(mail.TypeDeckShared)); !found || !checked {
		t.Errorf("B type deck_shared: found=%v checked=%v, want found and checked (default on)", found, checked)
	}
	if found, checked := checkboxState(body, "mail_pref."+string(mail.TypeReviewReminder)); !found || checked {
		t.Errorf("C type review_reminder: found=%v checked=%v, want found and unchecked (default off)", found, checked)
	}
	t.Logf("page renders 4 class groups; class A disabled, B on, C off")
}

// TestMailPrefsOptionalChoiceRoundTrips 是验收项「用户对可选类型的选择重启后仍生效」的页面侧证据：
// 提交后立即生效（页面与存储都反映新选择），且读取走的是新构造的存储实例（等价于重启后的读取）。
// 真正的「关闭数据库再打开」在 store 层 TestEmailPrefsPersistAcrossReopen 里验证。
func TestMailPrefsOptionalChoiceRoundTrips(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	setOwnerLocale(t, db, ownerID, "en")

	rec := postMailPrefs(t, srv, cookies, csrf, map[mail.Type]bool{
		mail.TypeReviewReminder: true,  // C：打开
		mail.TypeDeckShared:     false, // B：关闭
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /settings/notifications status = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	page := getWithCookies(t, srv, "/settings/notifications", cookies)
	if page.Code != http.StatusOK {
		t.Fatalf("GET after save status = %d, want 200", page.Code)
	}
	body := page.Body.String()
	if _, checked := checkboxState(body, "mail_pref."+string(mail.TypeReviewReminder)); !checked {
		t.Error("review_reminder is not checked after the user turned it on")
	}
	if _, checked := checkboxState(body, "mail_pref."+string(mail.TypeDeckShared)); checked {
		t.Error("deck_shared is still checked after the user turned it off")
	}

	// 新存储实例读回：等价于进程重启后从库里重建读取路径。
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
	t.Logf("stored choices after save: %v", choices)
}

// TestMailPrefsRejectsDisablingClassA 是「拒绝关闭 A 类」的服务端负例：
// 伪造一个指向 A 类的偏好键（真实 UI 从不渲染它）→ 400，且不产生任何写入，A 类仍然开启。
func TestMailPrefsRejectsDisablingClassA(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	setOwnerLocale(t, db, ownerID, "en")

	rec := postForm(t, srv, "/settings/notifications", url.Values{
		"csrf_token":               {csrf},
		"mail_pref.password_reset": {"on"},
	}, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("disabling a class A type status = %d, want 400 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if !strings.Contains(rec.Body.String(), "cannot be turned off") {
		t.Errorf("rejection page does not explain why class A is locked: %s", snippet(rec.Body.String()))
	}

	choices, err := store.NewEmailPrefStore(db).Choices(context.Background(), ownerID)
	if err != nil {
		t.Fatalf("Choices() error = %v", err)
	}
	if len(choices) != 0 {
		t.Errorf("a rejected class A submission wrote choices: %v", choices)
	}
	if !mail.ResolveEnabled(choices, mail.TypePasswordReset) {
		t.Error("class A password_reset is not enabled after a rejected disable attempt")
	}
	t.Logf("class A disable attempt rejected with 400 and no rows written")
}

// TestMailPrefsDisabledTypeIsNotSent 是验收项「某个类型被关后即使触发条件发生也不发」：
// 发信方门控 = 读存储选择 + mail.ResolveEnabled；关掉的 B 类返回 false，打开的 C 类返回 true，
// A 类恒为 true。这是 wave 2 发信方将要调用的同一条判定路径。
func TestMailPrefsDisabledTypeIsNotSent(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	rec := postMailPrefs(t, srv, cookies, csrf, map[mail.Type]bool{
		mail.TypeReviewReminder: true,  // C：打开
		mail.TypeDeckShared:     false, // B：关闭
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST status = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	choices, err := store.NewEmailPrefStore(db).Choices(context.Background(), ownerID)
	if err != nil {
		t.Fatalf("Choices() error = %v", err)
	}
	if mail.ResolveEnabled(choices, mail.TypeDeckShared) {
		t.Error("deck_shared is off but the sender gate says it would be sent")
	}
	if !mail.ResolveEnabled(choices, mail.TypeReviewReminder) {
		t.Error("review_reminder is on but the sender gate says it would not be sent")
	}
	if !mail.ResolveEnabled(choices, mail.TypePasswordReset) {
		t.Error("class A password_reset must always pass the sender gate")
	}
	t.Logf("sender gate with choices %v: deck_shared=false review_reminder=true password_reset=true", choices)
}

// TestMailPrefsRejectsMissingCSRF 断言保存必须带会话绑定的 CSRF token，缺失即被拒且不写入。
func TestMailPrefsRejectsMissingCSRF(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	rec := postForm(t, srv, "/settings/notifications", url.Values{
		"mail_pref." + string(mail.TypeInvite): {"on"},
	}, cookies)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST without csrf status = %d, want 403 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	choices, err := store.NewEmailPrefStore(db).Choices(context.Background(), ownerID)
	if err != nil {
		t.Fatalf("Choices() error = %v", err)
	}
	if len(choices) != 0 {
		t.Errorf("a CSRF-less request wrote choices: %v", choices)
	}
}

// TestMailPrefsCatalogKeysExistInBothCatalogs 断言目录与页面引用的每个语言包键在中英两套里都存在：
// 目录标签是动态拼接的（loc.T(def.LabelKey)），静态 key 扫描器看不见它们，必须在这里兜底。
func TestMailPrefsCatalogKeysExistInBothCatalogs(t *testing.T) {
	zh := catalogKeys(t, filepath.Join("..", "i18n", "locales", "zh-CN.yaml"))
	en := catalogKeys(t, filepath.Join("..", "i18n", "locales", "en.yaml"))

	keys := []string{
		"settings.mail_prefs.heading",
		"settings.mail_prefs.hint",
		"settings.mail_prefs.link",
		"mail.prefs.title",
		"mail.prefs.heading",
		"mail.prefs.intro",
		"mail.prefs.submit",
		"mail.prefs.saved",
		"mail.prefs.locked",
		"mail.prefs.error.unknown_type",
		"mail.prefs.error.class_locked",
	}
	for _, class := range mail.ClassOrder() {
		// 只断言标题键：分组说明（.note）已按「文案从简」移除，页面上不再有那几行。
		keys = append(keys, "mail.prefs.class."+string(class)+".heading")
	}
	for _, def := range mail.Catalog() {
		keys = append(keys, def.LabelKey)
	}
	for _, key := range keys {
		if !zh[key] {
			t.Errorf("zh-CN.yaml is missing the mail preference key %q", key)
		}
		if !en[key] {
			t.Errorf("en.yaml is missing the mail preference key %q", key)
		}
	}
}
