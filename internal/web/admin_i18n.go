package web

import (
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/web/views"
)

// 管理面板的语言包完整度报告（DESIGN.md §8.3；ROADMAP.md M8-4）。
//
// 口径：基准 key 集合 = 各语言包 key 集合的并集（见 i18n.Coverage 的注释）。覆盖率 < 100%
// 时逐条列出缺失的 key，让「哪个语言少了哪条」一眼可见。报告页本身也走语言包。
// 纯读页，无写操作，因此不需要 CSRF；路由在 adminRoutes() 清单里，非 admin 一律 403。

// i18nCoverage 返回覆盖率数据。默认读已加载语言包；测试可注入一份「缺 key」的
// 语言包集合，以验证页面真的会把 <100% 渲染出来并点名缺失的 key。
func (s *Server) i18nCoverage() []i18n.LocaleCoverage {
	if s.coverageOverride != nil {
		return s.coverageOverride()
	}
	if s.i18n == nil {
		return nil
	}
	return s.i18n.Coverage()
}

// adminI18nPage 渲染各语言的翻译覆盖率。
func (s *Server) adminI18nPage(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	coverage := s.i18nCoverage()
	rows := make([]views.AdminI18nRow, 0, len(coverage))
	allComplete := len(coverage) > 0
	for _, cov := range coverage {
		status := loc.T("admin.i18n.status.complete")
		missing := ""
		if !cov.Complete() {
			allComplete = false
			status = loc.Tf("admin.i18n.status.incomplete", map[string]any{"count": len(cov.Missing)})
			missing = strings.Join(cov.Missing, ", ")
		}
		rows = append(rows, views.AdminI18nRow{
			Code:        cov.Code,
			Percent:     loc.Tf("admin.i18n.percent", map[string]any{"percent": cov.Percent()}),
			Counts:      loc.Tf("admin.i18n.counts", map[string]any{"present": cov.Present, "total": cov.Total}),
			Complete:    cov.Complete(),
			StatusLabel: status,
			MissingKeys: missing,
		})
	}

	renderHTML(c, views.AdminPage(views.AdminPageData{
		Layout:     s.adminLayout(c, loc, "admin.i18n.title", "/admin/i18n"),
		Heading:    loc.T("admin.i18n.heading"),
		Intro:      loc.T("admin.i18n.intro"),
		NavHeading: loc.T("admin.nav.heading"),
		Nav:        s.adminNav(loc, "/admin/i18n"),

		I18nPage: true,
		I18nRows: rows,

		ColI18nLocale:   loc.T("admin.i18n.col.locale"),
		ColI18nCoverage: loc.T("admin.i18n.col.coverage"),
		ColI18nStatus:   loc.T("admin.i18n.col.status"),
		ColI18nMissing:  loc.T("admin.i18n.col.missing"),

		I18nAllComplete:      allComplete,
		I18nAllCompleteLabel: loc.T("admin.i18n.all_complete"),
	}))
}
