package api

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

func TestImportProgressTransports(t *testing.T) {
	for _, transport := range []string{"multipart", "url", "ticket"} {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/enabled=%t", transport, enabled), func(t *testing.T) {
				env := newTestEnv(t, 60, 60)
				user := seedUser(t, env.db, "progress", store.RoleUser)
				key := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
				raw := ankiFixtureBytes(t)
				option := ""
				if enabled {
					option = `,"include_progress":true`
				}
				switch transport {
				case "multipart":
					var body bytes.Buffer
					w := multipart.NewWriter(&body)
					file, err := w.CreateFormFile("file", "deck.apkg")
					if err != nil {
						t.Fatal(err)
					}
					if _, err := file.Write(raw); err != nil {
						t.Fatal(err)
					}
					if enabled {
						if err := w.WriteField("include_progress", "1"); err != nil {
							t.Fatal(err)
						}
					}
					if err := w.Close(); err != nil {
						t.Fatal(err)
					}
					rec := postImport(t, env, key.Plaintext, &body, w.FormDataContentType())
					if rec.Code != http.StatusOK {
						t.Fatalf("%d %s", rec.Code, rec.Body.String())
					}
				case "url":
					env.api.fetcher = &stubFetcher{body: raw}
					rec := postImportURL(t, env, key.Plaintext, `{"url":"https://example.com/deck.apkg"`+option+`}`)
					if rec.Code != http.StatusOK {
						t.Fatalf("%d %s", rec.Code, rec.Body.String())
					}
				case "ticket":
					up := issueUpload(t, env, key.Plaintext, `{"target":"new_deck"`+option+`}`)
					rec := putUpload(t, env, up.UploadURL, raw)
					if rec.Code != http.StatusOK {
						t.Fatalf("%d %s", rec.Code, rec.Body.String())
					}
				}
				var count int64
				if err := env.db.Model(&store.CardState{}).Where("user_id = ?", user.ID).Count(&count).Error; err != nil {
					t.Fatal(err)
				}
				if (count > 0) != enabled {
					t.Fatalf("states = %d, enabled = %t", count, enabled)
				}
			})
		}
	}
}

func TestImportProgressReaderDenied(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "progress_owner", store.RoleUser)
	reader := seedUser(t, env.db, "progress_reader", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	if err := store.NewGrantStore(env.db).Grant(t.Context(), deck.ID, reader.ID, store.RoleReader, &owner.ID); err != nil {
		t.Fatal(err)
	}
	key := seedKey(t, env.keys, reader.ID, []string{store.ScopeWrite}, nil)
	rec := createUpload(t, env, key.Plaintext, fmt.Sprintf(`{"target":"into_deck:%s","include_progress":true}`, deck.PublicID))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
}
