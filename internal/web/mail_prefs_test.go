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
		"mail.prefs.reminder_time.heading",
		"mail.prefs.reminder_time.label",
		"mail.prefs.reminder_time.hint",
		"mail.prefs.reminder_time.default",
		"mail.prefs.error.reminder_hour_invalid",
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
