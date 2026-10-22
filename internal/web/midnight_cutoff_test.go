package web

import (
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// Clearing the configuration must store NULL, not 0; 0 is itself a legitimate cutoff (midnight), and the two must not be confused.
// Profile writes go through the SPA's JSON PATCH (the SSR /settings/profile form has been deleted).
func TestSettingsCutoffCanClearToDefault(t *testing.T) {
	srv, db, id, cookies, csrf := newNotesServer(t)

	midnight := 0
	if rec := patchProfileCutoff(t, srv, cookies, csrf, &midnight); rec.Code != http.StatusOK {
		t.Fatalf("midnight status=%d body=%s", rec.Code, snippet(rec.Body.String()))
	}
	u, err := store.NewUserStore(db).ByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if u.DayCutoffHour == nil || *u.DayCutoffHour != 0 {
		t.Fatalf("midnight cutoff stored as %v, want 0", u.DayCutoffHour)
	}

	if rec := patchProfileCutoff(t, srv, cookies, csrf, nil); rec.Code != http.StatusOK {
		t.Fatalf("clear status=%d body=%s", rec.Code, snippet(rec.Body.String()))
	}
	u, err = store.NewUserStore(db).ByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if u.DayCutoffHour != nil {
		t.Fatal("clear did not store NULL")
	}
}
