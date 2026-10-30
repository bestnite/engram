package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// cardFramePath is the route prefix of the card frame page. The path segment after it is a build version used only for cache
// busting: when the build changes the URL changes, so an already loaded iframe cannot keep pointing at a document whose
// assets were replaced. The handler accepts any non-empty segment and ignores its value, so the server never has to know the
// version string the frontend was built with.
const cardFramePath = "/card-frame/:version"

// registerCardFrameRoutes mounts the card frame page. It is public like the SPA shell: the iframe itself carries no session
// state, its parent page does the authenticated requests, and the runtime reads no cookie.
func (s *Server) registerCardFrameRoutes(router *gin.Engine) {
	router.GET(cardFramePath, s.cardFrame)
}

// frameCSP builds the policy of one sandboxed frame document. Both frame documents render untrusted note content inside an
// opaque-origin sandbox and differ only in how much script they may run, so every directive except script-src is written
// once here: a directive added to one frame cannot silently diverge from the other. scriptSrc is the complete source list
// of the caller's script-src directive.
//
//   - sandbox allow-scripts: the frame's own runtime must run, but without allow-same-origin the document gets an opaque
//     origin and cannot read the parent's cookies or storage. allow-scripts is the only sandbox token.
//   - default-src 'none': nothing is allowed unless a directive below names it.
//   - script-src: supplied by the caller; the two frames enforce different script policies.
//   - style-src <origin> 'unsafe-inline': the runtime bundle's stylesheet is same-origin, and the runtime writes content
//     styles inline.
//   - img-src/media-src/font-src: frame media comes from the same origin (/fm/<token>/...), content may reference https
//     media, data: images/fonts and blob: media; media only needs the same origin, https and blob.
//   - connect-src <origin>/fm/: the frame itself only fetches media through the /fm/ token paths.
//   - form-action 'none': a frame must not submit a form.
//   - frame-ancestors 'self': only this site may embed a frame. X-Frame-Options is removed on both frame routes, because
//     the global DENY would block the iframe before the sandbox had a chance to restrict it.
func frameCSP(origin, scriptSrc string) string {
	return "sandbox allow-scripts; " +
		"default-src 'none'; " +
		"script-src " + scriptSrc + "; " +
		"style-src " + origin + " 'unsafe-inline'; " +
		"img-src " + origin + " https: data: blob:; " +
		"media-src " + origin + " https: blob:; " +
		"font-src " + origin + " https: data:; " +
		"connect-src " + origin + "/fm/; " +
		"form-action 'none'; " +
		"frame-ancestors 'self'"
}

// cardFrameCSP builds the card frame's policy. Card content may carry the author's inline scripts, and MathJax compiles
// TeX at runtime, so script-src allows inline and evaluated script in addition to the same-origin runtime bundle. Every
// other directive comes from frameCSP.
func cardFrameCSP(origin string) string {
	return frameCSP(origin, origin+" 'unsafe-inline' 'unsafe-eval'")
}

// cardFrame serves the card frame document.
//
// The global security middleware has already written the site headers before this handler runs, so the frame overrides them here:
// the exact frame CSP replaces the site CSP, Referrer-Policy becomes no-referrer (a frame URL must not leak media token paths to
// third parties), and X-Frame-Options is deleted because the global DENY would prevent the iframe from embedding the page at all;
// frame-ancestors 'self' in the frame CSP is the control that remains. The other global headers (X-Content-Type-Options,
// Permissions-Policy) stay in place.
func (s *Server) cardFrame(c *gin.Context) {
	body := s.shell.CardFrameHTML()
	if len(body) == 0 {
		c.Status(http.StatusNotFound)
		return
	}
	// The frame attaches the audio and speech controls, whose labels come from the translation catalog, so the document carries the
	// request's resolved language exactly like the SPA entry. The iframe navigation carries the session cookie, so the user's
	// profile setting takes part in the resolution; the runtime reads the language from <html lang> and never reads a cookie.
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

	// The document references content-hashed assets, but it also carries the MathJax meta injected at startup, so it is
	// revalidated instead of cached for a year like the immutable assets it points at.
	h.Set("Cache-Control", "no-cache, must-revalidate")
	h.Set("ETag", etag)
	c.Data(http.StatusOK, "text/html; charset=utf-8", body)
}
