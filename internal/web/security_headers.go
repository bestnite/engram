package web

import (
	"crypto/sha256"
	"encoding/base64"

	"github.com/gin-gonic/gin"
)

// This file is one fixed set of security response headers. The CSP ships in enforcing mode (a violation is blocked), not Report-Only:
// the policy is narrowed directive by directive to "the resources the pages actually use" (the rationale is in the cspPolicy comment), and it is
// statically guarded by TestSecurityHeadersGuardCoversServedShellAndScripts — a newly appearing inline script or external
// source that the policy does not cover turns the test red.
const (
	// cspHeader is the CSP header name for enforcing mode. One word different (Content-Security-Policy-Report-Only) would mean
	// report-only with no blocking, hence a separate constant: switching between enforce and observe changes only this one spot. Enforce and Report-Only must not be
	// sent together — with two CSP headers the browser honours only the last one, and mixing them makes the policy unpredictable.
	cspHeader = "Content-Security-Policy"
	// referrerPolicy sends only the origin cross-site and keeps the full path same-site. The credential of a share link (/s/:token) lives in the
	// path, so the path is never sent back cross-site, keeping the access credential from leaking to third parties via Referer.
	referrerPolicy = "strict-origin-when-cross-origin"
	// permissionsPolicy turns off browser capabilities this site never uses, keeping the minimal set.
	permissionsPolicy = "accelerometer=(), autoplay=(), camera=(), display-capture=(), encrypted-media=(), fullscreen=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), midi=(), payment=(), picture-in-picture=(), publickey-credentials-get=(), screen-wake-lock=(), serial=(), usb=(), xr-spatial-tracking=()"
)

// cspScriptHash is the SHA-256 allowlist token of the inline theme bootstrap script (themeBootstrapJS, a compile-time constant).
// The whole site has this single inline <script>, and its content does not vary with the request, so a hash is used instead of a nonce: there is no need to thread a
// per-request value through the code path that assembles the app shell, and computing one per request would be meaningless anyway.
var cspScriptHash = func() string {
	sum := sha256.Sum256([]byte(themeBootstrapJS))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}()

// cspPolicy is the policy narrowed directive by directive to "the resources the pages actually use", with the rationale for each value (checked one by one against the source and the build output):
//
//   - default-src 'self': the whole site is self-hosted, no CDN. Static assets (icons, /pwa.js, self-hosted MathJax) are
//     go:embed'ed into the binary; the frontend output (the /assets/index-*.js and index-*.css Vite produces) is likewise
//     embedded and served from same-origin paths.
//   - script-src 'self' <theme bootstrap hash>: external scripts are only the three same-origin kinds — bundle output
//     (/assets/index-*.js), static scripts (/pwa.js, tex-svg.js of the self-hosted MathJax), and the service worker Go generates
//     on the fly (/sw.js), so 'self' is enough. The only inline <script> block on a page is the first-paint
//     theme bootstrap (themeBootstrapJS), allowed precisely by its SHA-256, which is why script-src does not carry
//     'unsafe-inline'. Nor 'unsafe-eval': none of the shipped scripts compiles JS at runtime, and the guard test
//     asserts exactly that for our own static scripts and /sw.js, turning red on a hit.
//   - style-src 'self' 'unsafe-inline': the stylesheet is the bundled /assets/index-*.css (same origin).
//     'unsafe-inline' is needed because the SPA renders inline style attributes: the bar widths on the stats page
//     (StatsView.svelte) and the placeholder widths of the skeleton screens (Skeleton.svelte) are both written in that attribute, and CSP's
//     hash/nonce cannot cover style attributes, so 'unsafe-inline' is the only workable value.
//   - img-src 'self' https:: images come from /media (self); the card-face sanitizing allowlist permits https external images
//     (imgSrcPattern in internal/render), hence https is allowed; otherwise external images in card faces would be blocked.
//   - connect-src 'self': the SPA's XHR/fetch (API, uploads, service worker registration) are all same-origin.
//   - object-src 'none' / frame-src 'none': no <object>/<embed>/<iframe> in the shipped pages.
//   - base-uri 'self' / form-action 'self': no <base> injection surface; forms are submitted by the SPA
//     (onsubmit prevents the default and goes to the same-origin JSON endpoints instead), with no third-party submit target.
//   - frame-ancestors 'none': the share page (/s/:token) is a standalone page with no need to be embedded by third parties,
//     so it may not be nested by any origin (consistent with X-Frame-Options: DENY below).
var cspPolicy = "default-src 'self'; " +
	"script-src 'self' " + cspScriptHash + "; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' https:; " +
	"connect-src 'self'; " +
	"object-src 'none'; " +
	"frame-src 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'"

// securityHeaders adds the five security header groups to every response. It is mounted as global middleware, so /media, /api/v1, /mcp,
// static assets and the 404 fallback are all covered; static assets are self-hosted anyway ('self'), so the same policy needs no special-case widening.
// The CSP is an enforcing header: a violation is blocked (already switched over from Report-Only).
func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set(cspHeader, cspPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", referrerPolicy)
		h.Set("X-Frame-Options", "DENY")
		h.Set("Permissions-Policy", permissionsPolicy)
		c.Next()
	}
}
