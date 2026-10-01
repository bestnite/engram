package web

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"example.com/flashcard/internal/store"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestHealthzReturnsDatabaseAndSchemaVersion(t *testing.T) {
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "healthz.db"))
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

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("healthz body is not JSON: %v (%s)", err, rec.Body.String())
	}
	if body["database"] != "ok" {
		t.Errorf(`healthz database = %v, want "ok"`, body["database"])
	}
	if _, ok := body["schema_version"]; !ok {
		t.Errorf("healthz response is missing schema_version: %s", rec.Body.String())
	}
}

func TestHealthzReportsSchemaVersionAfterSync(t *testing.T) {
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "healthz-sync.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := store.Sync(context.Background(), db, nil); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	srv, err := New("127.0.0.1:0", Deps{
		DB:            db,
		Logger:        discardLogger(),
		SchemaVersion: func(ctx context.Context) (int, error) { return store.CurrentVersion(ctx, db) },
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	var body struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("healthz body is not JSON: %v", err)
	}
	if body.SchemaVersion != 0 {
		t.Errorf("schema_version = %d, want 0 (no migration registered yet)", body.SchemaVersion)
	}
}

func TestNewRejectsMissingDependencies(t *testing.T) {
	if _, err := New("127.0.0.1:0", Deps{}); err == nil {
		t.Error("New() accepted empty Deps, want an error")
	}
}

func TestRecoveryTurnsPanicInto500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(recovery(discardLogger()))
	router.GET("/boom", func(c *gin.Context) { panic("kaboom") })

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("panicking handler status = %d, want 500", rec.Code)
	}
}
