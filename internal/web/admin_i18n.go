package web

import (
	"git.nite07.com/nite/engram/internal/i18n"
)

// The admin panel's language pack completeness report.
//
// Convention: the baseline key set = the union of the key sets of all language packs (see the comment on i18n.Coverage).
// After the SSR report page was removed, the report data is served by the /api/v1/admin/i18n JSON endpoint (admin_i18n_api.go).

// i18nCoverage returns the coverage data. By default it reads the loaded language packs; tests can inject a set of
// language packs with missing keys.
func (s *Server) i18nCoverage() []i18n.LocaleCoverage {
	if s.coverageOverride != nil {
		return s.coverageOverride()
	}
	if s.i18n == nil {
		return nil
	}
	return s.i18n.Coverage()
}
