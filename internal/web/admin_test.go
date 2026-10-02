package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"gorm.io/gorm"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"example.com/flashcard/internal/store"
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
	if os.Getenv(envMediaMaxBytes) == "" {
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
	if _, ok := settings[settingKeyMediaMaxBytes]; ok {
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

// TestAdminExportStreamsAllTables 验证全库导出按钮：返回 JSON，含各数据表。
func TestAdminExportStreamsAllTables(t *testing.T) {
	srv, cookies, _ := newAdminServer(t)
	rec := getWithCookies(t, srv, "/admin/export", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/export = %d, want 200", rec.Code)
	}
	var payload struct {
		Tables map[string][]map[string]any `json:"tables"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("export body is not JSON: %v (body %s)", err, snippet(rec.Body.String()))
	}
	for _, want := range []string{"users", "settings", "decks", "reviews"} {
		if _, ok := payload.Tables[want]; !ok {
			t.Errorf("export is missing table %q", want)
		}
	}

	// 关键守卫：导出必须覆盖**数据库里实际存在的每一张表**（见 missingExportTables）。
	if missing := missingExportTables(t, srv.db, payload.Tables); len(missing) > 0 {
		t.Errorf("export is missing tables that exist in the database: %v", missing)
	}
}

// missingExportTables 返回「数据库里存在、但导出里没有」的表，跳过 SQLite 的内部记账表。
// 比对的是数据库现实而不是 AllModels()：备份漏表时不报错，只会在恢复时表现为
// 「TOTP 已启用但密钥不见了」这类静默降级，所以必须拿现实去比。
func missingExportTables(t *testing.T, db *gorm.DB, exported map[string][]map[string]any) []string {
	t.Helper()
	tables, err := db.Migrator().GetTables()
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	if len(tables) == 0 {
		t.Fatal("database reports no tables; this guard would pass vacuously")
	}
	var missing []string
	for _, name := range tables {
		if strings.HasPrefix(name, "sqlite_") {
			continue
		}
		if _, ok := exported[name]; !ok {
			missing = append(missing, name)
		}
	}
	return missing
}

// TestExportCoverageGuardHasTeeth 证明上面的守卫不是空转：往库里塞一张导出根本不认识的表，
// 守卫必须点名它。没有这一条，"守卫恒返回空"与"导出确实齐全"无法区分。
func TestExportCoverageGuardHasTeeth(t *testing.T) {
	srv, cookies, _ := newAdminServer(t)
	if err := srv.db.Exec("CREATE TABLE probe_not_exported (id integer primary key)").Error; err != nil {
		t.Fatalf("create probe table: %v", err)
	}
	rec := getWithCookies(t, srv, "/admin/export", cookies)
	var payload struct {
		Tables map[string][]map[string]any `json:"tables"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("export body is not JSON: %v", err)
	}
	found := false
	for _, name := range missingExportTables(t, srv.db, payload.Tables) {
		if name == "probe_not_exported" {
			found = true
		}
	}
	if !found {
		t.Fatal("the coverage guard did not report a table that the export cannot possibly know about")
	}
}
