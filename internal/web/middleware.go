package web

import (
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/text/language"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/i18n"
)

// SessionUserLocale is the default implementation of Deps.UserLocale: it takes the UI language from the resolved session user.
// The wiring hands it to web.Deps (the session middleware must run before the language middleware),
// so the language in personal settings really takes part in i18n resolution priority.
func SessionUserLocale(c *gin.Context) string {
	if u, ok := auth.CurrentUser(c); ok {
		return u.Locale
	}
	return ""
}

// localeMiddleware resolves the request language in a fixed priority order and puts the localizer into the request context.
// Priority: explicit ?lang override > user setting (provided by Deps.UserLocale; M0 has no sessions, M1 wires it in)
// > Accept-Language > site default.
func (s *Server) localeMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		userLocale := ""
		if s.userLocale != nil {
			userLocale = s.userLocale(c)
		}
		requested := c.Query("lang")
		accept := c.GetHeader("Accept-Language")
		tag := s.i18n.Pick(requested, userLocale, accept)
		// The site default language setting is the last fallback: it applies only when the three more explicit sources
		// (?lang, the user setting, Accept-Language) are all absent, and never overrides them.
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
