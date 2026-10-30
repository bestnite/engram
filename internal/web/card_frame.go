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

// cardFrameCSP builds the frame's own policy. The frame runs card content (including the card author's inline scripts) inside
// an opaque-origin sandbox, so it is deliberately wider than the site policy while still naming every source it may use:
//
//   - sandbox allow-scripts: card content may run scripts, but without allow-same-origin it gets an opaque origin and cannot read
//     the parent's cookies or storage. allow-scripts is the only sandbox token.
//   - default-src 'none': nothing is allowed unless a directive below names it.
//   - script-src <origin> 'unsafe-inline' 'unsafe-eval': the runtime bundle is same-origin; card content carries inline scripts and
//     MathJax compiles TeX at runtime.
//   - style-src <origin> 'unsafe-inline': the runtime bundle's stylesheet is same-origin, card CSS is written inline by the runtime.
//   - img-src/media-src/font-src: frame media comes from the same origin (/fm/<token>/...), card content may reference https media,
//     data: images/fonts and blob: media; media only needs the same origin, https and blob.
//   - connect-src <origin>/fm/: the frame itself only fetches media through the /fm/ token paths.
//   - form-action 'none': a card must not submit a form.
//   - frame-ancestors 'self': only this site may embed the frame. X-Frame-Options is removed on this route (see cardFrame), because
//     the global DENY would block the iframe before the sandbox had a chance to restrict it.
func cardFrameCSP(origin string) string {
	return "sandbox allow-scripts; " +
		"default-src 'none'; " +
		"script-src " + origin + " 'unsafe-inline' 'unsafe-eval'; " +
		"style-src " + origin + " 'unsafe-inline'; " +
		"img-src " + origin + " https: data: blob:; " +
		"media-src " + origin + " https: blob:; " +
		"font-src " + origin + " https: data:; " +
		"connect-src " + origin + "/fm/; " +
		"form-action 'none'; " +
		"frame-ancestors 'self'"
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
