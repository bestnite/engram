package web

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/store"
)

// gzipTestRouter mounts the middleware under test in front of the routes one case registers, at the position the real
// engine gives it: as global middleware, before the routes.
func gzipTestRouter(register func(router *gin.Engine)) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gzipJSON())
	register(router)
	return router
}

// serveGzipRequest runs one request against a router and returns the recorded response.
func serveGzipRequest(t *testing.T, router *gin.Engine, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// gunzipBody decodes a recorded gzip body, failing the test when the body is not a complete gzip stream.
func gunzipBody(t *testing.T, rec *httptest.ResponseRecorder) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("gzip.NewReader() error = %v (body %q)", err, rec.Body.String())
	}
	defer zr.Close()
	body, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("read gzip body error = %v", err)
	}
	return body
}

// assertJSONEqual compares two JSON documents by decoded value, so formatting and key order do not matter.
func assertJSONEqual(t *testing.T, got, want []byte) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("decoded body is not JSON: %v (%s)", err, got)
	}
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatalf("expected body is not JSON: %v (%s)", err, want)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Errorf("decoded body = %v, want %v", gotValue, wantValue)
	}
}

func TestGzipJSONCompressesJSONWhenClientAcceptsGzip(t *testing.T) {
	payload := map[string]any{
		"deck":  "example",
		"cards": []int{1, 2, 3},
		"note":  strings.Repeat("x", 4096),
	}
	router := gzipTestRouter(func(router *gin.Engine) {
		router.GET("/json", func(c *gin.Context) { c.JSON(http.StatusOK, payload) })
	})

	req := httptest.NewRequest(http.MethodGet, "/json", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := serveGzipRequest(t, router, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Encoding"); got != gzipEncoding {
		t.Fatalf("Content-Encoding = %q, want %q", got, gzipEncoding)
	}
	if got := rec.Header().Get("Content-Length"); got != "" {
		t.Errorf("Content-Length = %q, want it removed for a compressed body", got)
	}
	if got := strings.Join(rec.Header().Values("Vary"), ", "); !strings.Contains(got, acceptEncodingHeader) {
		t.Errorf("Vary = %q, want it to name %s", got, acceptEncodingHeader)
	}
	want, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	assertJSONEqual(t, gunzipBody(t, rec), want)
	if rec.Body.Len() >= len(want) {
		t.Errorf("compressed body is %d bytes, want it smaller than the %d-byte JSON", rec.Body.Len(), len(want))
	}
}

func TestGzipJSONNegotiation(t *testing.T) {
	body := `{"ok":true,"text":"` + strings.Repeat("y", 2048) + `"}`
	cases := []struct {
		name           string
		acceptEncoding string
		contentType    string
		wantCompressed bool
		wantVary       bool
	}{
		{"accepts gzip", "gzip", "application/json; charset=utf-8", true, true},
		{"accepts gzip with a quality value", "gzip;q=0.5", "application/json; charset=utf-8", true, true},
		{"accepts gzip beside another coding", "br, gzip", "application/json; charset=utf-8", true, true},
		{"accepts the wildcard", "*", "application/json; charset=utf-8", true, true},
		{"no accept-encoding header", "", "application/json; charset=utf-8", false, true},
		{"refuses gzip with q=0", "gzip;q=0", "application/json; charset=utf-8", false, true},
		{"refuses the wildcard with q=0", "*;q=0", "application/json; charset=utf-8", false, true},
		{"prefers the gzip entry over a refused wildcard", "gzip, *;q=0", "application/json; charset=utf-8", true, true},
		{"names only another coding", "br", "application/json; charset=utf-8", false, true},
		{"matches the media type case-insensitively", "gzip", "Application/JSON; charset=UTF-8", true, true},
		{"leaves HTML alone", "gzip", "text/html; charset=utf-8", false, false},
		{"leaves a JSON suffix media type alone", "gzip", "application/manifest+json", false, false},
		{"leaves a response without content type alone", "gzip", "", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := gzipTestRouter(func(router *gin.Engine) {
				router.GET("/body", func(c *gin.Context) {
					if tc.contentType != "" {
						c.Header("Content-Type", tc.contentType)
					}
					_, _ = c.Writer.Write([]byte(body))
				})
			})
			req := httptest.NewRequest(http.MethodGet, "/body", nil)
			if tc.acceptEncoding != "" {
				req.Header.Set(acceptEncodingHeader, tc.acceptEncoding)
			}
			rec := serveGzipRequest(t, router, req)

			gotEncoding := rec.Header().Get("Content-Encoding")
			if tc.wantCompressed {
				if gotEncoding != gzipEncoding {
					t.Fatalf("Content-Encoding = %q, want %q", gotEncoding, gzipEncoding)
				}
				if got := string(gunzipBody(t, rec)); got != body {
					t.Errorf("decoded body = %q, want the original JSON", got)
				}
			} else {
				if gotEncoding != "" {
					t.Errorf("Content-Encoding = %q, want no coding", gotEncoding)
				}
				if got := rec.Body.String(); got != body {
					t.Errorf("body was rewritten: got %q, want the original JSON", got)
				}
			}
			gotVary := strings.Join(rec.Header().Values("Vary"), ", ")
			if tc.wantVary && !strings.Contains(gotVary, acceptEncodingHeader) {
				t.Errorf("Vary = %q, want it to name %s", gotVary, acceptEncodingHeader)
			}
			if !tc.wantVary && gotVary != "" {
				t.Errorf("Vary = %q, want it empty for a body that never varies by coding", gotVary)
			}
		})
	}
}

func TestGzipJSONVaryKeepsExistingValues(t *testing.T) {
	cases := []struct {
		name     string
		setVary  string
		wantVary string
	}{
		{"adds the field when absent", "", acceptEncodingHeader},
		{"keeps an existing entry before it", "Origin", "Origin, Accept-Encoding"},
		{"does not repeat an existing field", "Accept-Encoding", "Accept-Encoding"},
		{"does not repeat a lower-case existing field", "accept-encoding", "accept-encoding"},
		{"leaves the wildcard alone", "*", "*"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := gzipTestRouter(func(router *gin.Engine) {
				router.GET("/json", func(c *gin.Context) {
					if tc.setVary != "" {
						c.Header("Vary", tc.setVary)
					}
					c.JSON(http.StatusOK, gin.H{"ok": true})
				})
			})
			req := httptest.NewRequest(http.MethodGet, "/json", nil)
			req.Header.Set(acceptEncodingHeader, "gzip")
			rec := serveGzipRequest(t, router, req)

			if got := rec.Header().Get("Vary"); got != tc.wantVary {
				t.Errorf("Vary = %q, want %q", got, tc.wantVary)
			}
			occurrences := strings.Count(strings.ToLower(strings.Join(rec.Header().Values("Vary"), ", ")), "accept-encoding")
			if occurrences > 1 {
				t.Errorf("Vary lists %s %d times, want at most one", acceptEncodingHeader, occurrences)
			}
		})
	}
}

func TestGzipJSONLeavesExistingContentEncodingAlone(t *testing.T) {
	const body = `{"ok":true}`
	router := gzipTestRouter(func(router *gin.Engine) {
		router.GET("/encoded", func(c *gin.Context) {
			c.Header("Content-Type", "application/json; charset=utf-8")
			c.Header("Content-Encoding", "br")
			_, _ = c.Writer.Write([]byte(body))
		})
	})
	req := httptest.NewRequest(http.MethodGet, "/encoded", nil)
	req.Header.Set(acceptEncodingHeader, "gzip")
	rec := serveGzipRequest(t, router, req)

	if got := rec.Header().Get("Content-Encoding"); got != "br" {
		t.Errorf("Content-Encoding = %q, want the existing %q", got, "br")
	}
	if got := rec.Body.String(); got != body {
		t.Errorf("body = %q, want it untouched", got)
	}
}

func TestGzipJSONLeavesMCPAlone(t *testing.T) {
	payload := `{"jsonrpc":"2.0","result":{"ok":true}}`

	t.Run("event stream keeps streaming instead of compressing", func(t *testing.T) {
		router := gzipTestRouter(func(router *gin.Engine) {
			router.GET("/mcp", func(c *gin.Context) {
				c.Header("Content-Type", "text/event-stream")
				_, _ = c.Writer.WriteString("event: message\ndata: " + payload + "\n\n")
				c.Writer.Flush()
			})
		})
		req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
		req.Header.Set(acceptEncodingHeader, "gzip")
		rec := serveGzipRequest(t, router, req)

		if got := rec.Header().Get("Content-Encoding"); got != "" {
			t.Errorf("Content-Encoding = %q, want no coding on the event stream", got)
		}
		if !rec.Flushed {
			t.Error("event stream was not flushed through the connection")
		}
		if want := "event: message\ndata: " + payload + "\n\n"; rec.Body.String() != want {
			t.Errorf("body = %q, want the uncompressed event stream", rec.Body.String())
		}
	})

	t.Run("JSON body is not compressed on the MCP path", func(t *testing.T) {
		router := gzipTestRouter(func(router *gin.Engine) {
			router.GET("/mcp", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
		})
		req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
		req.Header.Set(acceptEncodingHeader, "gzip")
		rec := serveGzipRequest(t, router, req)

		if got := rec.Header().Get("Content-Encoding"); got != "" {
			t.Errorf("Content-Encoding = %q, want no coding on /mcp", got)
		}
	})
}

func TestGzipJSONLeavesMediaAlone(t *testing.T) {
	payload := `{"media":"a JSON-looking body behind /media"}`
	newMediaRouter := func() *gin.Engine {
		return gzipTestRouter(func(router *gin.Engine) {
			router.GET("/media/:sha", func(c *gin.Context) {
				c.Header("Content-Type", "application/json")
				c.Header("Cache-Control", "private, max-age=31536000, immutable")
				http.ServeContent(c.Writer, c.Request, "", time.Time{}, strings.NewReader(payload))
			})
		})
	}

	t.Run("range request stays byte addressed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/media/"+strings.Repeat("a", 64), nil)
		req.Header.Set(acceptEncodingHeader, "gzip")
		req.Header.Set("Range", "bytes=0-3")
		rec := serveGzipRequest(t, newMediaRouter(), req)

		if rec.Code != http.StatusPartialContent {
			t.Fatalf("status = %d, want 206 (body %q)", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Content-Encoding"); got != "" {
			t.Errorf("Content-Encoding = %q, want no coding on a range response", got)
		}
		if want := "bytes 0-3/" + strconv.Itoa(len(payload)); rec.Header().Get("Content-Range") != want {
			t.Errorf("Content-Range = %q, want %q", rec.Header().Get("Content-Range"), want)
		}
		if got := rec.Header().Get("Content-Length"); got != "4" {
			t.Errorf("Content-Length = %q, want %q", got, "4")
		}
		if got := rec.Body.String(); got != payload[:4] {
			t.Errorf("body = %q, want %q", got, payload[:4])
		}
	})

	t.Run("full body is served uncompressed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/media/"+strings.Repeat("a", 64), nil)
		req.Header.Set(acceptEncodingHeader, "gzip")
		rec := serveGzipRequest(t, newMediaRouter(), req)

		if got := rec.Header().Get("Content-Encoding"); got != "" {
			t.Errorf("Content-Encoding = %q, want no coding on /media", got)
		}
		if got := rec.Body.String(); got != payload {
			t.Errorf("body = %q, want it untouched", got)
		}
	})
}

func TestGzipJSONLeavesBodylessResponsesAlone(t *testing.T) {
	cases := []struct {
		name   string
		status int
	}{
		{"204 No Content", http.StatusNoContent},
		{"304 Not Modified", http.StatusNotModified},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := gzipTestRouter(func(router *gin.Engine) {
				router.GET("/empty", func(c *gin.Context) {
					c.Header("Content-Type", "application/json; charset=utf-8")
					c.Status(tc.status)
					c.Writer.WriteHeaderNow()
				})
			})
			req := httptest.NewRequest(http.MethodGet, "/empty", nil)
			req.Header.Set(acceptEncodingHeader, "gzip")
			rec := serveGzipRequest(t, router, req)

			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}
			if got := rec.Header().Get("Content-Encoding"); got != "" {
				t.Errorf("Content-Encoding = %q, want no coding on a bodyless response", got)
			}
			if rec.Body.Len() != 0 {
				t.Errorf("body = %q, want it empty", rec.Body.String())
			}
		})
	}
}

func TestGzipJSONLeavesHEADAlone(t *testing.T) {
	router := gzipTestRouter(func(router *gin.Engine) {
		handler := func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) }
		router.GET("/json", handler)
		router.HEAD("/json", handler)
	})
	req := httptest.NewRequest(http.MethodHead, "/json", nil)
	req.Header.Set(acceptEncodingHeader, "gzip")
	rec := serveGzipRequest(t, router, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want no coding on a HEAD response", got)
	}
}

func TestGzipJSONLeavesPartialContentAlone(t *testing.T) {
	payload := `{"partial":"bytes"}` + strings.Repeat(" ", 64)
	router := gzipTestRouter(func(router *gin.Engine) {
		router.GET("/json-range", func(c *gin.Context) {
			c.Header("Content-Type", "application/json")
			http.ServeContent(c.Writer, c.Request, "", time.Time{}, strings.NewReader(payload))
		})
	})
	req := httptest.NewRequest(http.MethodGet, "/json-range", nil)
	req.Header.Set(acceptEncodingHeader, "gzip")
	req.Header.Set("Range", "bytes=0-7")
	rec := serveGzipRequest(t, router, req)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206 (body %q)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want no coding on a partial response", got)
	}
	if got := rec.Body.String(); got != payload[:8] {
		t.Errorf("body = %q, want %q", got, payload[:8])
	}
}

func TestGzipJSONLeavesStaticAssetsAlone(t *testing.T) {
	const body = `body{color:#000}`
	router := gzipTestRouter(func(router *gin.Engine) {
		router.GET("/assets/index.css", func(c *gin.Context) {
			c.Header("Content-Type", "text/css; charset=utf-8")
			c.Header("ETag", `"sha256-abc"`)
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
			_, _ = c.Writer.Write([]byte(body))
		})
	})
	req := httptest.NewRequest(http.MethodGet, "/assets/index.css", nil)
	req.Header.Set(acceptEncodingHeader, "gzip")
	rec := serveGzipRequest(t, router, req)

	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want no coding on a static asset", got)
	}
	if got := rec.Header().Get("ETag"); got != `"sha256-abc"` {
		t.Errorf("ETag = %q, want it unchanged", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q, want it unchanged", got)
	}
	if got := rec.Header().Get("Vary"); got != "" {
		t.Errorf("Vary = %q, want no field on a body that never varies by coding", got)
	}
	if got := rec.Body.String(); got != body {
		t.Errorf("body = %q, want it untouched", got)
	}
}

func TestGzipJSONFlushesCompressedBodyToClient(t *testing.T) {
	rec := httptest.NewRecorder()
	var bytesAfterFlush int
	router := gzipTestRouter(func(router *gin.Engine) {
		router.GET("/stream", func(c *gin.Context) {
			c.Header("Content-Type", "application/json; charset=utf-8")
			_, _ = c.Writer.WriteString(`{"first":"one"`)
			c.Writer.Flush()
			bytesAfterFlush = rec.Body.Len()
			_, _ = c.Writer.WriteString(`,"second":"two"}`)
		})
	})
	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	req.Header.Set(acceptEncodingHeader, "gzip")
	router.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != gzipEncoding {
		t.Fatalf("Content-Encoding = %q, want %q", got, gzipEncoding)
	}
	if bytesAfterFlush == 0 {
		t.Error("Flush reached no connection: the gzip buffer held the written bytes back")
	}
	if got, want := string(gunzipBody(t, rec)), `{"first":"one","second":"two"}`; got != want {
		t.Errorf("decoded body = %q, want %q", got, want)
	}
}

func TestGzipJSONKeepsResponseControllerReachable(t *testing.T) {
	deadlineErr := make(chan error, 1)
	router := gzipTestRouter(func(router *gin.Engine) {
		router.GET("/deadline", func(c *gin.Context) {
			// The request-body limiter sets a read deadline this way, so the wrapper must not hide the connection.
			deadlineErr <- http.NewResponseController(c.Writer).SetReadDeadline(time.Now().Add(time.Second))
			c.JSON(http.StatusOK, gin.H{"ok": true})
		})
	})
	server := httptest.NewServer(router)
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL+"/deadline", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set(acceptEncodingHeader, "gzip")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /deadline error = %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body error = %v", err)
	}

	if got := resp.Header.Get("Content-Encoding"); got != gzipEncoding {
		t.Fatalf("Content-Encoding = %q, want %q", got, gzipEncoding)
	}
	if err := <-deadlineErr; err != nil {
		t.Errorf("SetReadDeadline through the gzip writer = %v, want the connection's deadline", err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("gzip.NewReader() error = %v", err)
	}
	defer zr.Close()
	decoded, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("read gzip body error = %v", err)
	}
	assertJSONEqual(t, decoded, []byte(`{"ok":true}`))
}

func TestServerCompressesJSONResponses(t *testing.T) {
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "gzip-json.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	srv, err := New("127.0.0.1:0", Deps{
		DB:            db,
		Logger:        discardLogger(),
		SchemaVersion: func(ctx context.Context) (int, error) { return store.CurrentVersion(ctx, db) },
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	plain := httptest.NewRecorder()
	srv.Handler().ServeHTTP(plain, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if got := plain.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("plain request Content-Encoding = %q, want no coding", got)
	}

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set(acceptEncodingHeader, "gzip")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Encoding"); got != gzipEncoding {
		t.Fatalf("Content-Encoding = %q, want %q", got, gzipEncoding)
	}
	if got := rec.Header().Get(cspHeader); got == "" {
		t.Error("the security headers are missing from the compressed response")
	}
	if got := rec.Header().Get("Vary"); !strings.Contains(got, acceptEncodingHeader) {
		t.Errorf("Vary = %q, want it to name %s", got, acceptEncodingHeader)
	}
	assertJSONEqual(t, gunzipBody(t, rec), plain.Body.Bytes())
}
