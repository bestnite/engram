package auth

import (
	"crypto/subtle"
	"net/http"

	"github.com/gin-gonic/gin"
)

// CSRFFieldName is the form field name that carries the token; the header equivalent is X-CSRF-Token.
const CSRFFieldName = "csrf_token"

// CSRFHeaderName is the header that carries the token for non-form requests (htmx / fetch).
const CSRFHeaderName = "X-CSRF-Token"

// CSRFMiddleware validates the CSRF token on every non-GET request, forming a double safeguard with SameSite=Lax.
// The token is bound to the server-side session, so Manager.Middleware must run first; anything without a valid session is 403.
func (m *Manager) CSRFMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}
		sess, ok := CurrentSession(c)
		if !ok {
			// No session means no token source, so reject immediately instead of degrading the CSRF check into "no session, therefore pass".
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": gin.H{"code": "csrf_no_session", "message": "no active session for CSRF validation"},
			})
			return
		}
		token := c.GetHeader(CSRFHeaderName)
		if token == "" {
			// PostForm parses and caches the form body and does not conflict with later reads by the handler.
			token = c.PostForm(CSRFFieldName)
		}
		if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(sess.CSRFToken)) != 1 {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": gin.H{"code": "csrf_failed", "message": "invalid or missing CSRF token"},
			})
			return
		}
		c.Next()
	}
}
