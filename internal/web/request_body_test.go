package web

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
)

// oversizedLoginRequestBody returns a JSON login body that is exactly one byte over the JSON body cap. It is used only
// by the streamed case below; the declared-length case keeps the actual body small.
func oversizedLoginRequestBody() string {
	return `{"username":"` + strings.Repeat("a", int(maxJSONBodyBytes)) + `"}`
}

// TestJSONBodyLimitRejectsOversizedLogin sends an over-cap login body to the assembled server. Both rejection paths are
// covered: a declared Content-Length over the cap is refused before any read, and a body without a declared length is
// refused by http.MaxBytesReader while it is being read.
func TestJSONBodyLimitRejectsOversizedLogin(t *testing.T) {
	srv, _ := newAuthServer(t)

	cases := []struct {
		name          string
		body          string
		contentLength int64
	}{
		{
			name:          "declared content length over the cap",
			body:          `{"username":"nobody"}`,
			contentLength: maxJSONBodyBytes + 1,
		},
		{
			name:          "streamed body over the cap",
			body:          oversizedLoginRequestBody(),
			contentLength: -1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.ContentLength = tc.contentLength
			rec := httptest.NewRecorder()

			srv.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("POST /api/v1/auth/login over the JSON body cap = %d, want 413 (body %s)", rec.Code, snippet(rec.Body.String()))
			}
			if got := apiErrorCode(t, rec); got != api.CodeRequestTooLarge {
				t.Errorf("error code = %q, want %q", got, api.CodeRequestTooLarge)
			}
		})
	}
}

// TestJSONBodyLimitAllowsBodyUnderCap is the control: a JSON body under the cap still reaches the login handler.
// Bad credentials produce 401, which proves the request was not stopped by the body cap.
func TestJSONBodyLimitAllowsBodyUnderCap(t *testing.T) {
	srv, _ := newAuthServer(t)
	cookie, headers := preSessionPair(t, srv, "/login")

	rec := postJSON(srv, "/api/v1/auth/login", map[string]string{
		"username": "nobody", "password": "WrongPassword!",
	}, []*http.Cookie{cookie}, headers)

	if rec.Code == http.StatusRequestEntityTooLarge {
		t.Fatalf("login under the JSON body cap was rejected as too large (body %s)", snippet(rec.Body.String()))
	}
	if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusBadRequest {
		t.Fatalf("login under the JSON body cap = %d, want 401 or 400 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
}

// TestJSONBodyLimitReadDeadlineClosesTrickledBody opens a raw connection, declares a JSON body and sends only part of
// it. The middleware's read deadline must end the read and produce a response well before the test timeout, proving a
// client cannot hold the connection open by trickling a body. The deadline is shortened only for the test.
func TestJSONBodyLimitReadDeadlineClosesTrickledBody(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	const readTimeout = 250 * time.Millisecond
	engine.Use(jsonBodyLimit(maxJSONBodyBytes, readTimeout))
	engine.POST("/api/v1/auth/login", func(c *gin.Context) {
		_, _ = io.ReadAll(c.Request.Body)
		c.Status(http.StatusOK)
	})

	ts := httptest.NewServer(engine)
	t.Cleanup(ts.Close)

	addr := strings.TrimPrefix(ts.URL, "http://")
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	partial := `{"username":"nobody"`
	if _, err := fmt.Fprintf(conn, "POST /api/v1/auth/login HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\nContent-Length: 100\r\nConnection: close\r\n\r\n%s", partial); err != nil {
		t.Fatalf("write partial body: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	start := time.Now()
	raw, err := io.ReadAll(conn)
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatalf("server kept a trickled JSON body open for %v", time.Since(start))
	}
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= 5*time.Second {
		t.Fatalf("response arrived after %v, want less than 5s", elapsed)
	}
	if !strings.Contains(string(raw), " 408 ") {
		t.Fatalf("trickled JSON body response = %q, want status 408", snippet(string(raw)))
	}
	if !strings.Contains(string(raw), api.CodeInvalidRequest) {
		t.Errorf("trickled JSON body response = %q, want code %q", snippet(string(raw)), api.CodeInvalidRequest)
	}
}

// opaqueBody marks the exact body object handed to the handler; a different object means the middleware buffered it.
type opaqueBody struct{ *strings.Reader }

func (b *opaqueBody) Close() error { return nil }

// TestJSONBodyLimitLeavesMultipartUntouched asserts the middleware does not buffer or replace a multipart body: that
// path keeps its own per-endpoint cap and reads its file part from the original stream.
func TestJSONBodyLimitLeavesMultipartUntouched(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	engine.Use(jsonBodyLimit(16, time.Second))

	body := &opaqueBody{strings.NewReader("--boundary\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a\"\r\n\r\nx\r\n--boundary--\r\n")}
	var seen io.ReadCloser
	engine.POST("/upload", func(c *gin.Context) {
		seen = c.Request.Body
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader("ignored"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=boundary")
	req.Body = body
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("multipart request = %d, want 200", rec.Code)
	}
	if seen != body {
		t.Fatalf("multipart body was replaced by the JSON body middleware")
	}
}

// TestJSONBodyLimitOnlyAppliesToJSONMediaTypes checks the media-type gate directly: parameters and the +json suffix are
// JSON, while form and multipart content types are not.
func TestJSONBodyLimitOnlyAppliesToJSONMediaTypes(t *testing.T) {
	cases := []struct {
		contentType string
		want        bool
	}{
		{"application/json", true},
		{"application/json; charset=utf-8", true},
		{"application/vnd.api+json", true},
		{"Application/JSON", true},
		{"application/x-www-form-urlencoded", false},
		{"multipart/form-data; boundary=x", false},
		{"text/plain", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isJSONMediaType(tc.contentType); got != tc.want {
			t.Errorf("isJSONMediaType(%q) = %v, want %v", tc.contentType, got, tc.want)
		}
	}
}
