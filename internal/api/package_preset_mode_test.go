package api

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

func TestImportPresetReuseResponse(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "import_preset_response", store.RoleUser)
	key := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	preset := store.NewPreset(user.ID, "Selected")
	if err := env.db.Create(&preset).Error; err != nil {
		t.Fatal(err)
	}
	raw := packageZip(t, map[string]string{
		"manifest.json":   `{"format_version":2,"exported_at":"2026-10-02T00:00:00Z","deck":{"name":"imported"},"include_progress":false,"include_media":false,"include_reviews":false,"counts":{"notes":0,"cards":0}}`,
		"note_types.json": `[]`, "notes.json": `[]`, "cards.json": `[]`,
		"preset.json": `{"desired_retention":0.9,"learning_steps":"1m","relearning_steps":"10m","maximum_interval_days":100,"enable_fuzz":true}`,
	})
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	f, err := w.CreateFormFile("file", "import.edeck")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteField("preset_mode", "reuse"); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteField("preset_id", preset.PublicID); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	rec := postImport(t, env, key.Plaintext, &body, w.FormDataContentType())
	t.Logf("raw response: %s", rec.Body.String())
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(`"preset_mode":"reuse"`)) || !bytes.Contains(rec.Body.Bytes(), []byte(`"preset_name":"Selected"`)) {
		t.Fatalf("unexpected response: %d %s", rec.Code, rec.Body.String())
	}
}
