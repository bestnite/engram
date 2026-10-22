package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"

	"github.com/gin-gonic/gin"
)

// CSRFDoubleSubmitCookieName is the name of the double-submit cookie for pre-session forms (/setup, /login, /register).
// These forms are submitted before any server-side session exists, so a session-bound CSRF token cannot be produced; instead we use
// a double-submit cookie: the GET hands out a cookie with a random token and mirrors the same token into a hidden form field,
// and the submission compares the two.
const CSRFDoubleSubmitCookieName = "csrf_double"

// doubleSubmitMinLen is the minimum length required to accept a cookie token; anything shorter is treated as invalid and regenerated.
// A generated token is base64url of 32 random bytes (43 characters); this lower bound only rejects obviously garbage values.
const doubleSubmitMinLen = 16

// newDoubleSubmitToken generates a new pre-session CSRF token (32 random bytes, URL-safe encoding).
func newDoubleSubmitToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// validDoubleSubmitToken decides whether the value in the cookie is usable; empty and too-short values are not accepted.
func validDoubleSubmitToken(v string) bool {
	return len(v) >= doubleSubmitMinLen
}

// EnsureDoubleSubmitToken returns the pre-session CSRF token that this request should mirror into the form.
//
// When a valid csrf_double cookie already exists it is reused as is (and the response does not hand it out again);
// otherwise a new token is generated and written to the cookie.
//
// The cookie is set HttpOnly: when the form is server-rendered the same token is written into a hidden field, so frontend scripts need not read it.
// SameSite=Lax and Secure follow the same policy as the session cookie: Secure is passed in by the caller (secure) based on the scheme of
// BASE_URL, and must not look at the request's own TLS — production terminates TLS at the reverse proxy, so the app only receives plain http,
// and deciding per request would drop Secure from the form cookie in production.
func EnsureDoubleSubmitToken(c *gin.Context, secure bool) string {
	if v, err := c.Cookie(CSRFDoubleSubmitCookieName); err == nil && validDoubleSubmitToken(v) {
		return v
	}
	tok, err := newDoubleSubmitToken()
	if err != nil {
		// Returns an empty string when generation fails: the form carries no token and the submission is rejected by the middleware (fail closed, never allow).
		return ""
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     CSRFDoubleSubmitCookieName,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   24 * 60 * 60,
	})
	return tok
}

// PreSessionCSRFMiddleware protects write endpoints callable before login (login, register, onboarding, forgot/reset password,
// email verification and email change confirmation, unsubscribe, TOTP second step).
//
// Why session-bound validation cannot be used everywhere: the contract of these endpoints is "anonymous callers allowed" (forgot password presupposes you cannot log in),
// and while anonymous there is no server-side session in which to store a value or against which to compare one.
//
// Why double submit cannot be used everywhere: the request may come from an **already logged-in** browser (for example a logged-in user clicking a verification/unsubscribe link from mail).
// The browser then holds only a session-bound token, which by nature differs from the double-submit cookie, so such requests would necessarily 403 — this is a
// failure that really happened. Which kind of value you get while logged in is decided by /api/v1/auth/session, and the client has no choice.
//
// So we tier by **whether this request has a valid session**, and reserve the weaker tier for anonymous requests only:
//
//   - valid session (the session middleware's verdict, see Manager.Middleware) → the request value must equal the CSRF token in the
//     session row. This tier is stronger than double submit: even an attacker able to write cookies into the victim's browser cannot read the value in the session row.
//   - anonymous → double submit: the csrf_double cookie must equal the mirrored value the request carries. This is the only approach needing no
//     server state, at the cost that an attacker who can write cookies for the target domain can bypass it (hence only for anonymous scenarios).
//
// Note: do **not** mount this middleware on post-login endpoints: those must require a session (no session means 403 csrf_no_session),
// otherwise an anonymous request could pass validation on the cookie alone and then continue in the handler in a "no user" state.
func PreSessionCSRFMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}
		if sess, ok := CurrentSession(c); ok {
			submitted := submittedCSRFToken(c)
			if !validDoubleSubmitToken(submitted) ||
				subtle.ConstantTimeCompare([]byte(submitted), []byte(sess.CSRFToken)) != 1 {
				abortCSRF(c)
				return
			}
			c.Next()
			return
		}
		cookie, err := c.Cookie(CSRFDoubleSubmitCookieName)
		if err != nil || !validDoubleSubmitToken(cookie) ||
			subtle.ConstantTimeCompare([]byte(cookie), []byte(submittedCSRFToken(c))) != 1 {
			abortCSRF(c)
			return
		}
		c.Next()
	}
}

// submittedCSRFToken gets the mirrored token the request carries: the header first (the SPA's JSON requests),
// falling back to the form field (no-script forms). PostForm parses and caches the form body and does not conflict with later reads by the handler.
func submittedCSRFToken(c *gin.Context) string {
	if token := c.GetHeader(CSRFHeaderName); token != "" {
		return token
	}
	return c.PostForm(CSRFFieldName)
}

// abortCSRF rejects a write request that fails validation in a uniform shape (stable code + English text; the frontend maps the code to a message).
func abortCSRF(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
		"error": gin.H{"code": "csrf_failed", "message": "invalid or missing CSRF token"},
	})
}
