package web

import (
	"net/url"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/web/views"
)

// repositoryURL 是页脚源码仓库链接。它是公开托管地址，属模块路径例外（AGENTS.md §2.2），
// 已登记在 scripts/checks/allowed-hosts.txt；绝不写进模板，因为模板禁止硬编码可见文案。
const repositoryURL = "https://git.nite07.com/nite/engram"

// decorateLayout 补齐所有页面外壳共有的字段：页脚项目名与仓库链接、页头语言切换下拉、
// 以及哈希化图标路径（M8-7）。每个 LayoutData 构造完后调用一次，避免多个外壳各自重复。
func (s *Server) decorateLayout(c *gin.Context, loc *i18n.Localizer, layout *views.LayoutData) {
	// 页脚固定显示项目名 Engram（不取站点名覆盖）：页脚表达的是项目身份而非站点配置（M8-7）。
	layout.Footer = loc.T("app.name")
	layout.RepoURL = repositoryURL
	// 图标走内容哈希路径（DESIGN.md §8.5）；缺资源时为空串，模板跳过引用。
	layout.IconURL = s.assets.URL("icons/icon.svg")
	layout.AppleTouchIconURL = s.assets.URL("icons/apple-touch-icon.png")
	layout.LangOptions = s.languageOptions(c, loc)
	layout.LanguageLabel = loc.T("home.language_label")
	layout.CurrentLanguage = loc.T("language." + loc.Locale())
}

// mainNav 构造顶部导航项，是全站唯一的导航来源（M8-7）。
//
// 顺序固定：今日 / 卡组 / 统计；管理仅管理员可见；调度预设与设置仅登录用户可见。
// active 是当前页面的路径，命中者标记 Active，由模板服务端渲染高亮——不再由前端脚本
// 按 href 猜测（旧做法会把品牌链接也算进导航，见 M8-7 缺陷 (a)）。
func (s *Server) mainNav(c *gin.Context, loc *i18n.Localizer, active string) []views.NavItem {
	items := []views.NavItem{
		{Label: loc.T("nav.today"), Href: "/"},
		{Label: loc.T("nav.decks"), Href: "/decks"},
		{Label: loc.T("nav.stats"), Href: "/stats"},
	}
	if u, ok := auth.CurrentUser(c); ok {
		// 管理入口只给管理员，避免普通用户点进去吃 403（M6-1）。
		if u.Role == store.RoleAdmin {
			items = append(items, views.NavItem{Label: loc.T("nav.admin"), Href: "/admin"})
		}
		// 预设与个人设置对每个已登录用户可见（M9-9）。
		items = append(items,
			views.NavItem{Label: loc.T("nav.presets"), Href: "/presets"},
			views.NavItem{Label: loc.T("nav.settings"), Href: "/settings"},
		)
	}
	for i := range items {
		items[i].Active = items[i].Href == active
	}
	return items
}

// languageOptions 生成指向当前页面的语言切换入口：覆盖 lang 参数，保留路径与其余查询参数。
// 不直接拼接原始 query，否则在 /decks?page=2 这类页面上切换语言会丢掉参数。
func (s *Server) languageOptions(c *gin.Context, loc *i18n.Localizer) []views.LanguageOption {
	codes := s.i18n.SupportedCodes()
	out := make([]views.LanguageOption, 0, len(codes))
	for _, code := range codes {
		out = append(out, views.LanguageOption{
			Label:  loc.T("language." + code),
			Href:   localeURL(c.Request.URL, code),
			Active: loc.Locale() == code,
		})
	}
	return out
}

// localeURL 复制 u 的路径与查询参数并覆盖 lang 参数；用 url.Values 编码，避免重复或注入。
func localeURL(u *url.URL, code string) string {
	path := u.Path
	if path == "" {
		path = "/"
	}
	q := u.Query()
	q.Set("lang", code)
	return path + "?" + q.Encode()
}
