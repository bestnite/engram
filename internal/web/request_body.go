package web

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
)

const (
	// maxJSONBodyBytes bounds the JSON request body of one request. 32 MiB is far above the largest JSON document the
	// REST API and the SPA endpoints accept (a deck package travels as a multipart upload with its own package cap),
	// while still keeping a single unauthenticated request from making the process allocate an arbitrarily large body.
	maxJSONBodyBytes int64 = 32 << 20
	// jsonBodyReadTimeout bounds how long reading a JSON body may take, so a client that trickles a body cannot hold
	// the connection open indefinitely. Uploads are multipart and never pass through this middleware, so their own
	// (longer) transfer time is not affected.
	jsonBodyReadTimeout = 30 * time.Second
)

// jsonBodyLimit caps and time-bounds JSON request bodies before any handler sees them. It is mounted globally, so
// both the SPA/API routes and the internal/api routes registered on the same router are covered.
//
// Only JSON media types are handled: multipart uploads keep their existing per-endpoint caps, and form requests are
// left untouched. A declared Content-Length over the cap is refused before the body is allocated. A body without a
// declared length is read through http.MaxBytesReader, so the handler only ever receives a complete body within the
// cap; an over-cap or otherwise failed read is answered with the shared error envelope instead of a partial body.
func jsonBodyLimit(maxBytes int64, timeout time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isJSONMediaType(c.GetHeader("Content-Type")) {
			c.Next()
			return
		}
		if c.Request.ContentLength > maxBytes {
			abortJSONBody(c, http.StatusRequestEntityTooLarge, api.CodeRequestTooLarge)
			return
		}
		if c.Request.Body == nil {
			c.Next()
			return
		}

		rc := http.NewResponseController(c.Writer)
		deadlineSet := false
		if err := rc.SetReadDeadline(time.Now().Add(timeout)); err == nil {
			deadlineSet = true
		}
		// ErrNotSupported is expected under httptest.ResponseRecorder, which has no connection to carry a deadline.
		// Any other failure still leaves the size cap in place, so reading continues bounded rather than failing the request.

		data, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes))
		if deadlineSet {
			// Clear the deadline before writing the response: the response may legitimately take longer than the
			// remaining body-read budget.
			_ = rc.SetReadDeadline(time.Time{})
		}
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				abortJSONBody(c, http.StatusRequestEntityTooLarge, api.CodeRequestTooLarge)
				return
			}
			// A non-cap read failure includes the deadline firing while a client trickles the body; the handler must
			// never see a half-read body, so the request is answered here.
			abortJSONBody(c, http.StatusRequestTimeout, api.CodeInvalidRequest)
			return
		}

		c.Request.Body = io.NopCloser(bytes.NewReader(data))
		c.Next()
	}
}

// isJSONMediaType reports whether the request media type is JSON, with or without parameters, or carries a +json
// structured suffix. Everything else, including multipart uploads and form posts, is outside the JSON body cap.
func isJSONMediaType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}

// abortJSONBody writes the shared error envelope; the message comes from the registered English text for the code.
func abortJSONBody(c *gin.Context, status int, code string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{
		"code":    code,
		"message": api.ErrorMessage(c.Request.Context(), code),
	}})
}
