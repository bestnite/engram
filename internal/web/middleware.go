package web

import (
	"github.com/gin-gonic/gin"

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
		tag := s.i18n.Pick(c.Query("lang"), userLocale, c.GetHeader("Accept-Language"))
		c.Request = c.Request.WithContext(
			i18n.WithLocalizer(c.Request.Context(), s.i18n.Localizer(tag)),
		)
		c.Next()
	}
}
