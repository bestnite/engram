package web

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestRequestLoggerDoesNotLogCredentials drives the real logger middleware over the three routes
// whose path carries a bearer credential and asserts that no token reaches the captured log line.
func TestRequestLoggerDoesNotLogCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name   string
		method string
		route  string
		path   string
		token  string
	}{
		{"frame media", http.MethodGet, frameMediaPath, "/fm/frame-token-secret/_font.woff2", "frame-token-secret"},
		{"import ticket", http.MethodPut, "/api/v1/decks/import-uploads/:token", "/api/v1/decks/import-uploads/import-token-secret", "import-token-secret"},
		{"export ticket", http.MethodGet, "/api/v1/decks/export-downloads/:token", "/api/v1/decks/export-downloads/export-token-secret", "export-token-secret"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, nil))
			router := gin.New()
			router.Use(requestLogger(logger))
			router.Handle(tc.method, tc.route, func(c *gin.Context) { c.Status(http.StatusOK) })

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))

			if rec.Code != http.StatusOK {
				t.Fatalf("%s = %d, want 200", tc.path, rec.Code)
			}
			line := logs.String()
			if line == "" {
				t.Fatal("request logger wrote no line")
			}
			if strings.Contains(line, tc.token) {
				t.Errorf("log line leaked the credential %q: %s", tc.token, line)
			}
			if !strings.Contains(line, "<token>") {
				t.Errorf("log line does not show the redacted path: %s", line)
			}
		})
	}
}
