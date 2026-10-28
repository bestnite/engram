package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/store"
)

// newKeysAPI wires the real /api/v1 handler chain against the same database, to verify that a created key
// can reach GET /api/v1/decks with bearer auth.
func newKeysAPI(t *testing.T, db *gorm.DB) http.Handler {
	t.Helper()
	a, err := api.New(api.Deps{
		DB:      db,
		Logger:  discardLogger(),
		Keys:    store.NewAPIKeyStore(db),
		Users:   store.NewUserStore(db),
		Decks:   store.NewDeckStore(db),
		Notes:   store.NewNoteStore(db),
		Presets: store.NewPresetStore(db),
		Cards:   store.NewCardStore(db),
	})
	if err != nil {
		t.Fatalf("api.New() error = %v", err)
	}
	r := gin.New()
	a.Register(r)
	return r
}

// apiGetDecks requests GET /api/v1/decks with a bearer key and returns the status code.
func apiGetDecks(t *testing.T, h http.Handler, plaintext string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/decks", nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}
