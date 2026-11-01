package web

import (
	"context"
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// One invalid value must fail the whole settings submission: before the fix the handler wrote each
// key as it validated it, so a valid key that came first stayed stored after the later key was rejected.
func TestAdminSettingsRejectsTheWholeSubmission(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	ctx := context.Background()

	if rec := adminPostJSON(t, srv, "/api/v1/admin/settings", adminSettingsRequest{
		Values: map[string]string{"site.name": "Before"},
	}, cookies, csrf); rec.Code != http.StatusNoContent {
		t.Fatalf("seed site.name = %d, want 204 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	// site.name is valid and is validated before media_max_bytes, which is not a number.
	rec := adminPostJSON(t, srv, "/api/v1/admin/settings", adminSettingsRequest{
		Values: map[string]string{"site.name": "After", "media_max_bytes": "abc"},
	}, cookies, csrf)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid submission = %d, want 400 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	settings, err := store.LoadSettings(ctx, db)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if got := settings["site.name"]; got != "Before" {
		t.Errorf("site.name after the rejected submission = %q, want the previous %q: the valid key must not be written", got, "Before")
	}
	if _, ok := settings["media_max_bytes"]; ok {
		t.Error("media_max_bytes was stored from a rejected submission")
	}
}

// A write that fails inside the database must roll the whole submission back, including keys that
// were already written earlier in the same transaction.
func TestAdminSettingsRollsBackAFailingWrite(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	ctx := context.Background()

	for _, action := range []string{"INSERT", "UPDATE"} {
		if err := db.Exec(`CREATE TRIGGER probe_fail_setting_` + action + ` BEFORE ` + action + ` ON settings
			WHEN NEW.key = 'media_max_bytes'
			BEGIN SELECT RAISE(ABORT, 'injected setting write failure'); END;`).Error; err != nil {
			t.Fatalf("install %s trigger: %v", action, err)
		}
	}

	rec := adminPostJSON(t, srv, "/api/v1/admin/settings", adminSettingsRequest{
		Values: map[string]string{"site.name": "Rolled Back", "media_max_bytes": "4096"},
	}, cookies, csrf)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("failing submission = %d, want 500 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	settings, err := store.LoadSettings(ctx, db)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if got, ok := settings["site.name"]; ok {
		t.Errorf("site.name = %q after the rolled-back submission, want it not stored", got)
	}
	if _, ok := settings["media_max_bytes"]; ok {
		t.Error("media_max_bytes was stored although its write failed")
	}
}
