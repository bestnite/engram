package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// This file is the SPA JSON endpoint for the admin panel's "language pack completeness" page.
//
// Same convention as adminI18nPage: the baseline key set = the union of the key sets of all language packs (see the i18n.Coverage comment),
// and when coverage < 100% the missing keys are listed one by one. Read-only page, no write operations.

// adminLocaleCoverage is the coverage of one locale.
type adminLocaleCoverage struct {
	Code     string   `json:"code"`
	Percent  int      `json:"percent"`
	Present  int      `json:"present"`
	Total    int      `json:"total"`
	Complete bool     `json:"complete"`
	Missing  []string `json:"missing"`
}

// adminI18nResponse is the language pack completeness report.
type adminI18nResponse struct {
	Locales     []adminLocaleCoverage `json:"locales"`
	AllComplete bool                  `json:"all_complete"`
}

// adminI18n returns the translation coverage per locale.
func (s *Server) adminI18n(c *gin.Context) {
	coverage := s.i18nCoverage()
	locales := make([]adminLocaleCoverage, 0, len(coverage))
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
		locales = append(locales, adminLocaleCoverage{
			Code: cov.Code, Percent: cov.Percent(), Present: cov.Present, Total: cov.Total,
			Complete: complete, Missing: missing,
		})
	}
	c.JSON(http.StatusOK, adminI18nResponse{Locales: locales, AllComplete: allComplete})
}
