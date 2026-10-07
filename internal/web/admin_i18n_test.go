package web

import (
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"git.nite07.com/nite/engram/internal/i18n"
)

// TestAdminI18nReportsIncompleteCatalog 是 M8-4 的核心验收：真的种一个缺 key 的语言包，
// 报告页必须给出 <100% 并点名那个 key。
//
// 做法：用 fstest.MapFS 构造两份语言包（en 三个 key、zh-CN 少一个），经
// i18n.CatalogSets + i18n.Coverage 算出覆盖率后注入页面，再请求 /admin/i18n，
// 断言 zh-CN 行显示 66%、状态「缺失 1 条」、并列出缺的那条 key；en 行仍是 100%。
func TestAdminI18nReportsIncompleteCatalog(t *testing.T) {
	srv, _, _, cookies, _ := newNotesServer(t)
	// GET /admin/i18n 已切到 SPA 应用壳；禁用 SPA 以覆盖 SSR 覆盖度报告渲染。
	srv.spa = nil

	sets, err := i18n.CatalogSets(fstest.MapFS{
		"locales/en.yaml": &fstest.MapFile{Data: []byte(
			"- id: a.one\n  translation: \"one\"\n" +
				"- id: a.two\n  translation: \"two\"\n" +
				"- id: a.three\n  translation: \"three\"\n")},
		"locales/zh-CN.yaml": &fstest.MapFile{Data: []byte(
			"- id: a.one\n  translation: \"一\"\n" +
				"- id: a.two\n  translation: \"二\"\n")},
	}, "locales")
	if err != nil {
		t.Fatalf("CatalogSets() error = %v", err)
	}
	coverage := i18n.Coverage(sets)
	for _, c := range coverage {
		t.Logf("seeded coverage: locale=%s percent=%d%% present=%d/%d missing=%v",
			c.Code, c.Percent(), c.Present, c.Total, c.Missing)
	}
	srv.coverageOverride = func() []i18n.LocaleCoverage { return coverage }

	rec := getWithCookies(t, srv, "/admin/i18n", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/i18n = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-coverage="zh-CN">66%<`) {
		t.Errorf("zh-CN coverage is not reported as 66%%; body = %s", snippet(body))
	}
	if !strings.Contains(body, "缺失 1 条") {
		t.Errorf("zh-CN is not reported as missing one key; body = %s", snippet(body))
	}
	if !strings.Contains(body, "a.three") {
		t.Errorf("the missing key a.three is not named; body = %s", snippet(body))
	}
	if !strings.Contains(body, `data-coverage="en">100%<`) {
		t.Errorf("en coverage is not reported as 100%%; body = %s", snippet(body))
	}
}

// TestAdminI18nRealCatalogsAreComplete 是同一页面的正例：真实语言包（加载期已过 parity）
// 覆盖率必须为 100%，并显示「全部语言包完整」。
func TestAdminI18nRealCatalogsAreComplete(t *testing.T) {
	srv, _, _, cookies, _ := newNotesServer(t)
	// GET /admin/i18n 已切到 SPA 应用壳；禁用 SPA 以覆盖 SSR 报告页渲染。
	srv.spa = nil
	rec := getWithCookies(t, srv, "/admin/i18n", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/i18n = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "全部语言包完整") {
		t.Errorf("complete catalogs are not reported as complete; body = %s", snippet(body))
	}
	for _, code := range []string{"zh-CN", "en"} {
		if !strings.Contains(body, `data-coverage="`+code+`">100%<`) {
			t.Errorf("locale %s coverage is not 100%%; body = %s", code, snippet(body))
		}
	}
}
