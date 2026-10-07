package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// 本文件是管理面板「语言包完整度」页的 SPA JSON 端点（ROADMAP.md M8-4）。
//
// 口径与 adminI18nPage 相同：基准 key 集合 = 各语言包 key 集合的并集（见 i18n.Coverage 注释），
// 覆盖率 < 100% 时逐条列出缺失 key。纯读页，无写操作。

// spaAdminLocaleCoverage 是一种语言的覆盖率。
type spaAdminLocaleCoverage struct {
	Code     string   `json:"code"`
	Percent  int      `json:"percent"`
	Present  int      `json:"present"`
	Total    int      `json:"total"`
	Complete bool     `json:"complete"`
	Missing  []string `json:"missing"`
}

// spaAdminI18nResponse 是语言包完整度报告。
type spaAdminI18nResponse struct {
	Locales     []spaAdminLocaleCoverage `json:"locales"`
	AllComplete bool                     `json:"all_complete"`
}

// spaAdminI18n 返回各语言的翻译覆盖率。
func (s *Server) spaAdminI18n(c *gin.Context) {
	coverage := s.i18nCoverage()
	locales := make([]spaAdminLocaleCoverage, 0, len(coverage))
	allComplete := len(coverage) > 0
	for _, cov := range coverage {
		complete := cov.Complete()
		if !complete {
			allComplete = false
		}
		missing := cov.Missing
		if missing == nil {
			missing = []string{}
		}
		locales = append(locales, spaAdminLocaleCoverage{
			Code: cov.Code, Percent: cov.Percent(), Present: cov.Present, Total: cov.Total,
			Complete: complete, Missing: missing,
		})
	}
	c.JSON(http.StatusOK, spaAdminI18nResponse{Locales: locales, AllComplete: allComplete})
}
