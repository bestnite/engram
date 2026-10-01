package web

import (
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/text/language"

	"example.com/flashcard/internal/i18n"
)

// localeMiddleware 按固定优先级解析请求语言，并把本地化器放进请求 context（M0-8）。
// 优先级：?lang 显式覆盖 > 用户设置（Deps.UserLocale 提供；M0 无会话，M1 接入）
// > Accept-Language > 站点默认。
func (s *Server) localeMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		userLocale := ""
		if s.userLocale != nil {
			userLocale = s.userLocale(c)
		}
		requested := c.Query("lang")
		accept := c.GetHeader("Accept-Language")
		tag := s.i18n.Pick(requested, userLocale, accept)
		// 站点默认语言设置（M6-5）是最后一级回退：只有当 ?lang、用户设置、Accept-Language
		// 三个更明确的来源都缺席时才应用它，绝不覆盖它们。
		if requested == "" && userLocale == "" && strings.TrimSpace(accept) == "" {
			if code := s.siteDefaultLocale(c.Request.Context()); code != "" {
				if t, err := language.Parse(code); err == nil {
					tag = t
				}
			}
		}
		c.Request = c.Request.WithContext(
			i18n.WithLocalizer(c.Request.Context(), s.i18n.Localizer(tag)),
		)
		c.Next()
	}
}
