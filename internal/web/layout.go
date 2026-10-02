package web

import (
	"net/url"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/web/views"
)

// repositoryURL 是页脚源码仓库链接。它是公开托管地址，属模块路径例外（AGENTS.md §2.2），
// 已登记在 scripts/checks/allowed-hosts.txt；绝不写进模板，因为模板禁止硬编码可见文案。
const repositoryURL = "https://git.nite07.com/nite/engram"

// decorateLayout 补齐所有页面外壳共有的字段：页脚仓库链接与页头语言切换下拉。
// 每个 LayoutData 构造完后调用一次，避免四个外壳各自重复同一段代码。
func (s *Server) decorateLayout(c *gin.Context, loc *i18n.Localizer, layout *views.LayoutData) {
	layout.Footer = loc.T("footer.powered_by")
	layout.RepoURL = repositoryURL
	layout.LangOptions = s.languageOptions(c, loc)
	layout.LanguageLabel = loc.T("home.language_label")
	layout.CurrentLanguage = loc.T("language." + loc.Locale())
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
