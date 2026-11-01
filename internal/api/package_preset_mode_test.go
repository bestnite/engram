package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

func TestImportPresetID(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "preset_resolver_owner", store.RoleUser)
	other := seedUser(t, env.db, "preset_resolver_other", store.RoleUser)
	preset := store.NewPreset(user.ID, "Selected")
	if err := env.db.Create(&preset).Error; err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		owner  uint64
		id     string
		want   uint64
		status int
	}{
		{"trimmed", user.ID, " \t" + preset.PublicID + " \n", preset.ID, 0},
		{"empty", user.ID, " \t", 0, 0},
		{"foreign", other.ID, preset.PublicID, 0, http.StatusBadRequest},
		{"missing", user.ID, "missing", 0, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := env.api.ImportPresetID(context.Background(), tc.owner, tc.id)
			if got != tc.want || (tc.status == 0 && err != nil) {
				t.Fatalf("got id %d, error %v; want id %d", got, err, tc.want)
			}
			if tc.status != 0 && (err == nil || asServiceError(err).Status != tc.status) {
				t.Fatalf("got error %v, want status %d", err, tc.status)
			}
		})
	}
	sqlDB, err := env.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = env.api.ImportPresetID(context.Background(), user.ID, preset.PublicID)
	if err == nil {
		t.Fatal("closed database returned no error")
	}
	if se := asServiceError(err); se.Status != http.StatusInternalServerError || se.Code != CodeInternal || !strings.Contains(se.Message, "resolve import preset") {
		t.Fatalf("closed database error = %+v, want 500 internal with resolve import preset context", se)
	}
}

func TestImportUploadPresetModes(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "upload_preset_modes", store.RoleUser)
	other := seedUser(t, env.db, "upload_preset_other", store.RoleUser)
	key := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	deck := seedDeck(t, env.db, user.ID)
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
	for _, body := range []string{fmt.Sprintf(`{"preset_mode":"new","preset_id":%q}`, p.PublicID), fmt.Sprintf(`{"preset_id":%q}`, p.PublicID), `{"preset_mode":"bad"}`, `{"preset_mode":"reuse"}`, `{"preset_mode":"copy"}`, fmt.Sprintf(`{"preset_mode":"reuse","preset_id":%q,"apply_weights":true}`, p.PublicID), fmt.Sprintf(`{"preset_mode":"copy","preset_id":%q,"apply_weights":true}`, p.PublicID), fmt.Sprintf(`{"preset_mode":"reuse","preset_id":%q,"target":"into_deck:any"}`, p.PublicID), fmt.Sprintf(`{"preset_mode":"reuse","preset_id":%q}`, foreign.PublicID), fmt.Sprintf(`{"target":%q,"preset_id":%q}`, "into_deck:"+deck.PublicID, p.PublicID)} {
		var before, after int64
		env.db.Model(&store.ActionToken{}).Count(&before)
		rec := createUpload(t, env, key.Plaintext, body)
		env.db.Model(&store.ActionToken{}).Count(&after)
		if rec.Code != http.StatusBadRequest || decodeErrorCode(t, rec.Body.Bytes()) != store.CodePresetSelectionInvalid || before != after {
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
	raw := fixturePackage(t, nil, nil, false)
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	f, err := w.CreateFormFile("file", "import.apkg")
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

func TestImportMultipartRejectsPresetOptions(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "multipart_preset_owner", store.RoleUser)
	other := seedUser(t, env.db, "multipart_preset_other", store.RoleUser)
	key := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	deck := seedDeck(t, env.db, user.ID)
	owned, foreign := store.NewPreset(user.ID, "Owned"), store.NewPreset(other.ID, "Foreign")
	for _, p := range []*store.Preset{&owned, &foreign} {
		if err := env.db.Create(p).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name   string
		fields map[string]string
	}{
		{"unknown mode", map[string]string{"preset_mode": "bad"}},
		{"missing preset", map[string]string{"preset_mode": "reuse"}},
		{"foreign preset", map[string]string{"preset_mode": "reuse", "preset_id": foreign.PublicID}},
		{"reuse weights", map[string]string{"preset_mode": "reuse", "preset_id": owned.PublicID, "apply_weights": "1"}},
		{"copy weights", map[string]string{"preset_mode": "copy", "preset_id": owned.PublicID, "apply_weights": "1"}},
		{"existing target", map[string]string{"preset_mode": "reuse", "preset_id": owned.PublicID, "target": "into_deck:" + deck.PublicID}},
		{"existing target empty mode", map[string]string{"preset_id": owned.PublicID, "target": "into_deck:" + deck.PublicID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var beforeDecks, beforePresets, afterDecks, afterPresets int64
			if err := env.db.Model(&store.Deck{}).Count(&beforeDecks).Error; err != nil {
				t.Fatal(err)
			}
			if err := env.db.Model(&store.Preset{}).Count(&beforePresets).Error; err != nil {
				t.Fatal(err)
			}
			var body bytes.Buffer
			w := multipart.NewWriter(&body)
			f, err := w.CreateFormFile("file", "import.apkg")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.Write(minimalPackageZip(t)); err != nil {
				t.Fatal(err)
			}
			for k, v := range tc.fields {
				if err := w.WriteField(k, v); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			rec := postImport(t, env, key.Plaintext, &body, w.FormDataContentType())
			if rec.Code != http.StatusBadRequest || decodeErrorCode(t, rec.Body.Bytes()) != store.CodePresetSelectionInvalid {
				t.Fatalf("response = %d %s, want 400 preset_selection_invalid", rec.Code, rec.Body.String())
			}
			if err := env.db.Model(&store.Deck{}).Count(&afterDecks).Error; err != nil {
				t.Fatal(err)
			}
			if err := env.db.Model(&store.Preset{}).Count(&afterPresets).Error; err != nil {
				t.Fatal(err)
			}
			if beforeDecks != afterDecks || beforePresets != afterPresets {
				t.Fatalf("decks %d -> %d, presets %d -> %d", beforeDecks, afterDecks, beforePresets, afterPresets)
			}
		})
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
	f, err := w.CreateFormFile("file", "import.apkg")
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
