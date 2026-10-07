package web

import (
	"git.nite07.com/nite/engram/internal/i18n"
)

// 管理面板的语言包完整度报告（ROADMAP.md M8-4）。
//
// 口径：基准 key 集合 = 各语言包 key 集合的并集（见 i18n.Coverage 的注释）。
// SSR 报告页删除后，报告数据由 /api/v1/admin/i18n 的 JSON 端点（spa_admin_i18n.go）输出。

// i18nCoverage 返回覆盖率数据。默认读已加载语言包；测试可注入一份「缺 key」的
// 语言包集合。
func (s *Server) i18nCoverage() []i18n.LocaleCoverage {
	if s.coverageOverride != nil {
		return s.coverageOverride()
	}
	if s.i18n == nil {
		return nil
	}
	return s.i18n.Coverage()
}
