package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// editFramePath is the route prefix of the rich-text editing frame page. Like the card frame's route, the
// path segment after it is a build version used only for cache busting, and the handler ignores its value.
const editFramePath = "/edit-frame/:version"

// registerEditFrameRoutes mounts the rich-text editing frame page. It is public like the SPA shell: the
// iframe carries no session state and reads no cookie; the parent page owns the API client and the CSRF
// token, and hands uploaded files to the frame's upload callback.
func (s *Server) registerEditFrameRoutes(router *gin.Engine) {
	router.GET(editFramePath, s.editFrame)
}

// editFrame serves the rich-text editing frame document.
//
// The editing area has the same isolation as the card frame: the field HTML is untrusted note content,
// so it is parsed only inside an opaque-origin sandbox. The route therefore carries the card frame's exact
// CSP, replaces the site policy, sets Referrer-Policy to no-referrer and removes X-Frame-Options, which the
// global DENY would otherwise use to block the iframe. The document carries the request's resolved
// language like the SPA entry, although its own copy is only used by the frame's built-in text.
func (s *Server) editFrame(c *gin.Context) {
	body := s.shell.EditFrameHTML()
	if len(body) == 0 {
		c.Status(http.StatusNotFound)
		return
	}
	if lang := requestLocale(c); lang != "" {
		body = setHTMLLang(body, lang)
	}

	origin := s.requestBaseURL(c)
	h := c.Writer.Header()
	h.Set(cspHeader, cardFrameCSP(origin))
	h.Set("Referrer-Policy", "no-referrer")
	h.Del("X-Frame-Options")

	etag := `"` + indexHashOf(body) + `"`
	if c.GetHeader("If-None-Match") == etag {
		c.Status(http.StatusNotModified)
		return
	}

	h.Set("Cache-Control", "no-cache, must-revalidate")
	h.Set("ETag", etag)
	c.Data(http.StatusOK, "text/html; charset=utf-8", body)
}
