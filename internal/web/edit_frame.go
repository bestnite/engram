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

// editFrameCSP builds the editing frame's policy. It is the card frame's policy with a narrower script-src: the origin
// only. The runtime is a same-origin ES module, so no inline script is needed; the runtime writes the field value with
// innerHTML, so an inline event handler attribute in a shared deck's field (for example an img onerror) would run if
// 'unsafe-inline' were allowed, and could then post upload messages the parent uploads under the editing user's
// session. The editing frame only inserts MathJax delimiters and never typesets them, so 'unsafe-eval' is unnecessary
// too. Every other directive comes from frameCSP and is identical to the card frame's.
func editFrameCSP(origin string) string {
	return frameCSP(origin, origin)
}

// editFrame serves the rich-text editing frame document.
//
// The editing area has the card frame's sandbox isolation: the field HTML is untrusted note content,
// so it is parsed only inside an opaque-origin sandbox. The route carries the editing frame's own CSP,
// which is the card frame's policy with a narrower script-src, replaces the site policy, sets
// Referrer-Policy to no-referrer and removes X-Frame-Options, which the global DENY would otherwise use
// to block the iframe. The document carries the request's resolved language like the SPA entry, although
// its own copy is only used by the frame's built-in text.
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
	h.Set(cspHeader, editFrameCSP(origin))
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
