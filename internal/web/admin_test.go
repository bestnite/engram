package web

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/language"

	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/media"
	"git.nite07.com/nite/engram/internal/store"
)

// testSecretKey 是测试用的 ENCRYPTION_KEY：base64(32 字节)，与任何真实密钥无关。
var testSecretKey = base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))

// TestAdminSettingsChangeAppliesWithoutRestart 是 M6-5 的核心验收：改一项设置后
// 不重启即生效，且页面标出来源。这里同时验证上传上限的生效值跟上（上传接口共用的解析函数）。
func TestAdminSettingsChangeAppliesWithoutRestart(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)

	before := getWithCookies(t, srv, "/admin/settings", cookies)
	if before.Code != http.StatusOK {
		t.Fatalf("GET /admin/settings = %d, want 200", before.Code)
	}
	if !strings.Contains(before.Body.String(), "内置默认值") {
		t.Errorf("settings page does not label the default source; body = %s", snippet(before.Body.String()))
	}

	rec := postForm(t, srv, "/admin/settings", url.Values{
		"csrf_token":      {csrf},
		"site.name":       {"Example Deck Site"},
		"media_max_bytes": {"2048"},
	}, cookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /admin/settings = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	// 同一个进程、不重启：下一次 GET 必须看到新值与来源「数据库」。
	after := getWithCookies(t, srv, "/admin/settings", cookies)
	body := after.Body.String()
	if !strings.Contains(body, "Example Deck Site") {
		t.Errorf("new site name is not shown after save; body = %s", snippet(body))
	}
	if !strings.Contains(body, "2048") {
		t.Errorf("new upload limit is not shown after save; body = %s", snippet(body))
	}
	if !strings.Contains(body, "数据库") {
		t.Errorf("settings page does not label the database source after save")
	}

	// 上传接口共用的解析函数也要立即读到新值（环境变量未设置时）。
	if os.Getenv(media.EnvMediaMaxBytes) == "" {
		if got := srv.uploadLimit(context.Background()); got != 2048 {
			t.Errorf("uploadLimit() = %d, want 2048 without restart", got)
		}
	}

	// 设置写入必须留痕。
	n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionSettingUpdate)
	if err != nil {
		t.Fatalf("count setting.update audits: %v", err)
	}
	if n != 1 {
		t.Errorf("setting.update audit rows = %d, want 1", n)
	}
}

// TestAdminSettingsRejectInvalidValues 覆盖反面用例：非法语言与非法数字不落库。
func TestAdminSettingsRejectInvalidValues(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)

	for _, tc := range []struct {
		name string
		form url.Values
		want string
	}{
		{"locale", url.Values{"csrf_token": {csrf}, "site.default_locale": {"de"}}, "invalid_locale"},
		{"number", url.Values{"csrf_token": {csrf}, "media_max_bytes": {"-5"}}, "invalid_number"},
	} {
		rec := postForm(t, srv, "/admin/settings", tc.form, cookies)
		if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), tc.want) {
			t.Errorf("%s: POST status=%d location=%q, want 303 with notice=%s", tc.name, rec.Code, rec.Header().Get("Location"), tc.want)
		}
	}
	settings, err := store.LoadSettings(context.Background(), db)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if _, ok := settings["site.default_locale"]; ok {
		t.Error("invalid locale was persisted")
	}
	if _, ok := settings[media.SettingKeyMediaMaxBytes]; ok {
		t.Error("invalid upload limit was persisted")
	}
}

// TestAdminDefaultLocaleAppliesWithoutRestart 证明站点默认语言设置免重启生效，
// 且只在没有 ?lang / 用户设置 / Accept-Language 时作为最后一级回退。
func TestAdminDefaultLocaleAppliesWithoutRestart(t *testing.T) {
	srv, _, _, cookies, csrf := newNotesServer(t)
	if rec := postForm(t, srv, "/admin/settings", url.Values{
		"csrf_token": {csrf}, "site.default_locale": {"en"},
	}, cookies); rec.Code != http.StatusSeeOther {
		t.Fatalf("POST default locale = %d, want 303", rec.Code)
	}
	rec := getWithCookies(t, srv, "/", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `lang="en"`) {
		t.Errorf("site default locale did not take effect without restart; body = %s", snippet(rec.Body.String()))
	}
}

// TestAdminSensitiveSettingNeverRevealsPlaintext 覆盖 M6-10 的 UI 后端：
// 敏感项只显示「已配置/未配置」，页面上绝不出现明文，库里存的是密文。
func TestAdminSensitiveSettingNeverRevealsPlaintext(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	codec, err := store.NewSecretCodec(testSecretKey)
	if err != nil {
		t.Fatalf("NewSecretCodec() error = %v", err)
	}
	srv.secrets = codec

	const key = "oidc_client_secret"
	const first = "first-plaintext-secret-abc"
	if err := store.PutSecret(context.Background(), db, codec, key, first, nil, time.Now().UTC()); err != nil {
		t.Fatalf("PutSecret() error = %v", err)
	}

	rec := getWithCookies(t, srv, "/admin/settings", cookies)
	body := rec.Body.String()
	if !strings.Contains(body, "已配置") {
		t.Errorf("sensitive section does not show the configured state; body = %s", snippet(body))
	}
	if strings.Contains(body, first) {
		t.Fatal("settings page leaked the sensitive plaintext")
	}

	// 通过表单写入新值：库里不得出现明文，页面依旧只显示状态。
	const second = "second-plaintext-secret-xyz"
	if rec := postForm(t, srv, "/admin/settings", url.Values{
		"csrf_token": {csrf}, key: {second},
	}, cookies); rec.Code != http.StatusSeeOther {
		t.Fatalf("POST sensitive setting = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var row store.Setting
	if err := db.First(&row, "key = ?", key).Error; err != nil {
		t.Fatalf("load stored secret: %v", err)
	}
	if strings.Contains(row.Value, second) || strings.Contains(row.Value, first) {
		t.Fatalf("stored secret value contains plaintext: %q", row.Value)
	}
	if rec := getWithCookies(t, srv, "/admin/settings", cookies); strings.Contains(rec.Body.String(), second) {
		t.Fatal("settings page leaked the newly saved plaintext")
	}
}

// TestAdminOptimizeMinReviewsRejectsBelowFloor 是 M9-12 验收 2：门槛表单拒绝低于下限的
// 提交，重定向回设置页并给出提示，且什么都没存——存量值原样保留、生效值不变。
func TestAdminOptimizeMinReviewsRejectsBelowFloor(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	ctx := context.Background()

	tr, err := i18n.New()
	if err != nil {
		t.Fatalf("i18n.New: %v", err)
	}
	loc := tr.Localizer(language.Chinese)

	// 先放一个合法的存量值，验证被拒的提交不会把它改掉。
	if err := store.PutSetting(ctx, db, store.SettingKeyOptimizeMinReviews, "400", nil, time.Now().UTC()); err != nil {
		t.Fatalf("PutSetting: %v", err)
	}

	rec := postForm(t, srv, "/admin/settings", url.Values{
		"csrf_token":                       {csrf},
		store.SettingKeyOptimizeMinReviews: {"100"},
	}, cookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST below-floor threshold = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	location := rec.Header().Get("Location")
	if !strings.Contains(location, "notice=optimize_min_reviews_too_low") {
		t.Fatalf("redirect target %q does not carry the below-floor notice code", location)
	}
	t.Logf("below-floor POST -> %d %s", rec.Code, location)

	// 库里的值必须没变，生效值也必须没变。
	settings, err := store.LoadSettings(ctx, db)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if got := settings[store.SettingKeyOptimizeMinReviews]; got != "400" {
		t.Fatalf("stored threshold = %q after a rejected post, want %q (nothing must be written)", got, "400")
	}
	effective, err := store.OptimizeMinReviews(ctx, db)
	if err != nil {
		t.Fatalf("OptimizeMinReviews: %v", err)
	}
	if effective != 400 {
		t.Fatalf("effective threshold = %d after a rejected post, want 400", effective)
	}
	t.Logf("after rejected POST: stored=%q effective=%d", settings[store.SettingKeyOptimizeMinReviews], effective)

	// 设置页确实列出了这个旋钮，并把提示文案渲染出来。
	page := getWithCookies(t, srv, "/admin/settings?notice=optimize_min_reviews_too_low", cookies)
	if page.Code != http.StatusOK {
		t.Fatalf("GET settings with notice = %d, want 200", page.Code)
	}
	body := page.Body.String()
	if !strings.Contains(body, loc.T("admin.setting.optimize_min_reviews")) {
		t.Errorf("settings page does not list the optimizer threshold setting; body = %s", snippet(body))
	}
	if !strings.Contains(body, loc.T("admin.notice.optimize_min_reviews_too_low")) {
		t.Errorf("settings page does not render the below-floor notice; body = %s", snippet(body))
	}
	t.Logf("notice rendered: %q", loc.T("admin.notice.optimize_min_reviews_too_low"))

	// 反向守卫：一个合法值必须真的被写入，否则「全部拒绝」也会让上面通过。
	okRec := postForm(t, srv, "/admin/settings", url.Values{
		"csrf_token":                       {csrf},
		store.SettingKeyOptimizeMinReviews: {"450"},
	}, cookies)
	if okRec.Code != http.StatusSeeOther || !strings.Contains(okRec.Header().Get("Location"), "notice=saved") {
		t.Fatalf("POST valid threshold = %d %q, want 303 notice=saved", okRec.Code, okRec.Header().Get("Location"))
	}
	after, err := store.OptimizeMinReviews(ctx, db)
	if err != nil {
		t.Fatalf("OptimizeMinReviews after valid post: %v", err)
	}
	if after != 450 {
		t.Fatalf("effective threshold = %d after posting 450, want 450", after)
	}
	t.Logf("valid POST 450 -> effective %d", after)
}
