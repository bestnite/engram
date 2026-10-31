package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

func TestImportUploadPresetModes(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "upload_preset_modes", store.RoleUser)
	other := seedUser(t, env.db, "upload_preset_other", store.RoleUser)
	key := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	p := store.NewPreset(user.ID, "Chosen")
	foreign := store.NewPreset(other.ID, "Foreign")
	if err := env.db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	if err := env.db.Create(&foreign).Error; err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"new", "reuse", "copy"} {
		t.Run(mode, func(t *testing.T) {
			body := fmt.Sprintf(`{"preset_mode":%q}`, mode)
			if mode != "new" {
				body = fmt.Sprintf(`{"preset_mode":%q,"preset_id":%q}`, mode, p.PublicID)
			}
			up := issueUpload(t, env, key.Plaintext, body)
			rec := putUpload(t, env, up.UploadURL, minimalPackageZip(t))
			if rec.Code != http.StatusOK {
				t.Fatalf("upload %d %s", rec.Code, rec.Body.String())
			}
			var report store.PackageImportReport
			if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			deck, err := store.NewDeckStore(env.db).ByPublicID(t.Context(), report.DeckPublicID)
			if err != nil {
				t.Fatal(err)
			}
			var got store.Preset
			if err := env.db.First(&got, deck.PresetID).Error; err != nil {
				t.Fatal(err)
			}
			if report.PresetMode != mode || report.PresetName != got.Name || (mode == "reuse") != (got.ID == p.ID) {
				t.Fatalf("report %+v preset %+v", report, got)
			}
		})
	}
	for _, body := range []string{fmt.Sprintf(`{"preset_mode":"new","preset_id":%q}`, p.PublicID), fmt.Sprintf(`{"preset_id":%q}`, p.PublicID), `{"preset_mode":"bad"}`, `{"preset_mode":"reuse"}`, `{"preset_mode":"copy"}`, fmt.Sprintf(`{"preset_mode":"reuse","preset_id":%q,"apply_weights":true}`, p.PublicID), fmt.Sprintf(`{"preset_mode":"copy","preset_id":%q,"apply_weights":true}`, p.PublicID), fmt.Sprintf(`{"preset_mode":"reuse","preset_id":%q,"target":"into_deck:any"}`, p.PublicID), fmt.Sprintf(`{"preset_mode":"reuse","preset_id":%q}`, foreign.PublicID)} {
		var before, after int64
		env.db.Model(&store.ActionToken{}).Count(&before)
		rec := createUpload(t, env, key.Plaintext, body)
		env.db.Model(&store.ActionToken{}).Count(&after)
		if rec.Code != http.StatusBadRequest || decodeErrorCode(t, rec.Body.Bytes()) != CodeInvalidRequest || before != after {
			t.Fatalf("request %s response %d %s tokens %d -> %d", body, rec.Code, rec.Body.String(), before, after)
		}
	}
}

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

func TestImportSharedDeckOmitsOwnerPreset(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "report_owner", store.RoleUser)
	editor := seedUser(t, env.db, "report_editor", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	preset := store.NewPreset(owner.ID, "Owner private preset")
	if err := env.db.Create(&preset).Error; err != nil {
		t.Fatal(err)
	}
	if err := env.db.Model(&store.Deck{}).Where("id = ?", deck.ID).Update("preset_id", preset.ID).Error; err != nil {
		t.Fatal(err)
	}
	grant := store.DeckGrant{DeckID: deck.ID, UserID: editor.ID, Role: store.RoleEditor, CreatedAt: env.now}
	if err := env.db.Create(&grant).Error; err != nil {
		t.Fatal(err)
	}
	key := seedKey(t, env.keys, editor.ID, []string{store.ScopeWrite}, nil)
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	f, err := w.CreateFormFile("file", "import.edeck")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(minimalPackageZip(t)); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteField("target", "into_deck:"+deck.PublicID); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	rec := postImport(t, env, key.Plaintext, &body, w.FormDataContentType())
	t.Logf("editor response: %s", rec.Body.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	for _, forbidden := range []string{"preset_mode", "preset_name", preset.Name} {
		if bytes.Contains(rec.Body.Bytes(), []byte(forbidden)) {
			t.Fatalf("response contains %q: %s", forbidden, rec.Body.String())
		}
	}
}
