package web

import (
	"compress/gzip"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// acceptEncodingHeader is both the request header the negotiation reads and the field name Vary reports.
const acceptEncodingHeader = "Accept-Encoding"

// gzipEncoding is the content coding this middleware applies.
const gzipEncoding = "gzip"

// gzipJSON compresses JSON response bodies with gzip when the client accepts gzip.
//
// It is mounted globally, after the security headers and before any route is registered, so every handler passes
// through it. The decision for one response is taken by gzipResponseWriter at the moment the response headers are
// committed, because that is the first moment the final Content-Type and Content-Encoding are known: a handler may
// set either header after judging the request, and a decision taken at request time would have to guess.
//
// gzip.BestCompression is deliberate. JSON is this server's largest repeated payload (list, review and statistics
// responses, all of them re-fetched as the user works), so the smallest body is worth the extra CPU on the encode
// path; these bodies are small enough that the slower level costs no perceptible time per response.
func gzipJSON() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !pathAllowsCompression(c.Request.URL.Path) || c.Request.Method == http.MethodHead {
			c.Next()
			return
		}
		w := &gzipResponseWriter{
			ResponseWriter:    c.Writer,
			clientAcceptsGzip: acceptsGzip(c.GetHeader(acceptEncodingHeader)),
		}
		c.Writer = w
		// The gzip stream is closed once the handler has finished, so the final block and the checksum reach the
		// client even when the handler never calls Flush. A response that never committed its headers created no
		// stream to close.
		defer w.close()
		c.Next()
	}
}

// gzipResponseWriter decides whether this response is compressed and routes the body writes through the compressor
// when it is. It is installed for every request that may be compressed, not only for requests that accept gzip: the
// Vary header has to be added to the uncompressed variant too, or a shared cache can hand the compressed body to a
// client that never asked for it.
type gzipResponseWriter struct {
	gin.ResponseWriter
	// clientAcceptsGzip is the Accept-Encoding decision, parsed once per request.
	clientAcceptsGzip bool
	// decided is set when the header set has been examined. It is examined once: the first examination either
	// commits the response or leaves it alone for its whole life.
	decided bool
	// gz is the compressor; it is non-nil only while this response is being compressed.
	gz *gzip.Writer
}

// decide inspects the response headers and switches the body writes over to gzip. It runs before the first header
// commit and does nothing on later calls.
func (w *gzipResponseWriter) decide() {
	if w.decided {
		return
	}
	w.decided = true
	// Once the response is committed the header map no longer reaches the client, so neither Vary nor
	// Content-Encoding can be added any more.
	if w.ResponseWriter.Written() {
		return
	}
	h := w.Header()
	if !isJSONContentType(h.Get("Content-Type")) {
		return
	}
	// Vary goes on both variants of a JSON response, compressed or not, so a cache keys the entry on the request
	// header instead of serving whichever variant it stored first.
	addVaryAcceptEncoding(h)
	if !w.clientAcceptsGzip || h.Get("Content-Encoding") != "" || !contentCodingAllowed(w.ResponseWriter.Status()) {
		return
	}
	gz, err := gzip.NewWriterLevel(w.ResponseWriter, gzip.BestCompression)
	if err != nil {
		// gzip.BestCompression is a valid level, so this cannot happen; if it ever does, serving the body
		// uncompressed is better than failing a request over a response header.
		return
	}
	// The compressed length is not known when the headers go out, so the declared length must be removed: a stale
	// Content-Length would make the client stop reading at the old, now wrong, size.
	h.Del("Content-Length")
	h.Set("Content-Encoding", gzipEncoding)
	w.gz = gz
}

// close flushes the gzip trailer. It is a no-op when the response was not compressed.
func (w *gzipResponseWriter) close() {
	if w.gz == nil {
		return
	}
	// A failure here can only leave the trailer short, and the response is already being written, so there is no
	// channel left to report it through.
	_ = w.gz.Close()
}

// Unwrap exposes the wrapped writer to http.ResponseController. Middleware and handlers reach the connection through
// a ResponseController (for example the request-body limiter sets a read deadline on it), and unwrapping keeps those
// calls working through this wrapper; Flush is the one method the controller does not unwrap for, because this
// wrapper answers it itself.
func (w *gzipResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// WriteHeaderNow commits the headers, deciding about compression first.
func (w *gzipResponseWriter) WriteHeaderNow() {
	w.decide()
	w.ResponseWriter.WriteHeaderNow()
}

// Write writes the body through the compressor when the response is compressed.
func (w *gzipResponseWriter) Write(p []byte) (int, error) {
	w.decide()
	if w.gz == nil {
		return w.ResponseWriter.Write(p)
	}
	if _, err := w.gz.Write(p); err != nil {
		return 0, err
	}
	// The compressor accepted the whole slice; it buffers whatever does not fit one output block yet.
	return len(p), nil
}

// WriteString is Write for the string form, which handlers that render templates use.
func (w *gzipResponseWriter) WriteString(s string) (int, error) {
	w.decide()
	if w.gz == nil {
		return w.ResponseWriter.WriteString(s)
	}
	if _, err := w.gz.Write([]byte(s)); err != nil {
		return 0, err
	}
	return len(s), nil
}

// Flush pushes whatever the compressor still buffers down to the connection, so a flushing handler keeps streaming: a
// flush that only reached the compressor's internal buffer would hold the bytes back until that buffer fills or the
// response ends.
func (w *gzipResponseWriter) Flush() {
	w.decide()
	if w.gz != nil {
		// Flush has no error channel; a failed flush surfaces as a failed following write or a dropped client.
		_ = w.gz.Flush()
	}
	w.ResponseWriter.Flush()
}

// pathAllowsCompression reports whether responses for a request path may be compressed. Two paths never are:
// /mcp streams Server-Sent Events, where a content coding would hold events back until the compressor flushes, and
// /media serves stored binary bodies with Range requests, where a content coding would break byte-range addressing.
func pathAllowsCompression(path string) bool {
	if path == "/mcp" || strings.HasPrefix(path, "/mcp/") {
		return false
	}
	if path == "/media" || strings.HasPrefix(path, "/media/") {
		return false
	}
	return true
}

// acceptsGzip reports whether an Accept-Encoding header value accepts the gzip coding. A gzip entry wins over a
// wildcard entry because it is the more specific one; q=0 refuses a coding, and an entry without a q parameter is
// acceptable at q=1. An empty header accepts nothing.
func acceptsGzip(header string) bool {
	gzipQuality, hasGzip := 0.0, false
	wildcardQuality, hasWildcard := 0.0, false
	for _, entry := range strings.Split(header, ",") {
		fields := strings.Split(entry, ";")
		switch strings.ToLower(strings.TrimSpace(fields[0])) {
		case gzipEncoding:
			gzipQuality, hasGzip = qualityValue(fields[1:]), true
		case "*":
			wildcardQuality, hasWildcard = qualityValue(fields[1:]), true
		}
	}
	if hasGzip {
		return gzipQuality > 0
	}
	if hasWildcard {
		return wildcardQuality > 0
	}
	return false
}

// qualityValue returns the q value carried in the parameters of one Accept-Encoding entry. A missing or unparsable q
// parameter counts as q=1: the coding was named, and only an explicit q=0 refuses it.
func qualityValue(params []string) float64 {
	for _, param := range params {
		key, value, ok := strings.Cut(param, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "q") {
			continue
		}
		q, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return 1
		}
		return q
	}
	return 1
}

// isJSONContentType reports whether a Content-Type header value names JSON. The media type is parsed and compared
// case-insensitively, so "application/json; charset=utf-8" qualifies; "application/manifest+json" and
// "text/json" do not, because only the media type this middleware covers is compressed.
func isJSONContentType(contentType string) bool {
	if contentType == "" {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return strings.EqualFold(mediaType, "application/json")
}

// addVaryAcceptEncoding appends Accept-Encoding to the Vary header. Existing entries keep their place, and an
// existing Accept-Encoding entry in any letter case is not repeated. Vary: * is left untouched, because it already
// says the whole response varies.
func addVaryAcceptEncoding(h http.Header) {
	values := h.Values("Vary")
	for _, value := range values {
		for _, entry := range strings.Split(value, ",") {
			entry = strings.TrimSpace(entry)
			if entry == "*" || strings.EqualFold(entry, acceptEncodingHeader) {
				return
			}
		}
	}
	if len(values) == 0 {
		h.Set("Vary", acceptEncodingHeader)
		return
	}
	h.Set("Vary", strings.Join(values, ", ")+", "+acceptEncodingHeader)
}

// contentCodingAllowed reports whether a response with this status may carry a content coding. Responses with no
// body (1xx, 204, 304) carry no body to code, and a 206 partial response addresses bytes of the uncompressed
// representation, so coding it would make Content-Range describe bytes the client never receives.
func contentCodingAllowed(status int) bool {
	switch {
	case status >= 100 && status <= 199:
		return false
	case status == http.StatusNoContent:
		return false
	case status == http.StatusNotModified:
		return false
	case status == http.StatusPartialContent:
		return false
	}
	return true
}
