package api

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

func TestExportAnkiRESTSwitches(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	ctx := context.Background()
	owner := seedUser(t, env.db, "switch_owner", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	env.api.mediaRoot = t.TempDir()
	weights, err := json.Marshal([]float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1, 1.1, 1.2, 1.3, 1.4, 1.5, 1.6, 1.7, 1.8, 1.9, 2, 2.1})
	if err != nil {
		t.Fatal(err)
	}
	if err := env.db.Model(&store.Preset{}).Where("id = ?", deck.PresetID).Updates(map[string]any{"weights_json": string(weights), "desired_retention": 0.87}).Error; err != nil {
		t.Fatal(err)
	}
	media, err := store.NewMediaStore(env.db).SaveBytes(ctx, env.api.mediaRoot, "image/png", []byte("\x89PNG\r\n\x1a\nexample"), store.Ptr(owner.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.NewMediaNameStore(env.db).Upsert(ctx, deck.ID, "pic.png", media.Sha256); err != nil {
		t.Fatal(err)
	}
	note := store.Note{DeckID: deck.ID, NoteTypeID: store.Ptr(stockNoteTypeID(t, env.db, deck.ID, "basic")), CreatedBy: store.Ptr(owner.ID)}
	cards, err := store.NewNoteStore(env.db).Create(ctx, &note, map[string]any{"front": "question", "back": `<img src="pic.png">`})
	if err != nil {
		t.Fatal(err)
	}
	due := env.now.Add(72 * time.Hour)
	state := store.CardState{CardID: cards[0].ID, UserID: owner.ID, State: "review", DueAt: &due, Stability: store.Ptr(4.5), Difficulty: store.Ptr(6.2), Reps: 3, ScheduledDays: 5, LastReviewAt: &env.now, Version: 4}
	if err := env.db.Create(&state).Error; err != nil {
		t.Fatal(err)
	}
	review := store.Review{CardID: cards[0].ID, UserID: owner.ID, Rating: 3, GradeSource: "self", ReviewedAt: env.now, ReviewDay: "2026-10-02", StateBefore: 2, IntervalDays: store.Ptr(5.0)}
	if err := env.db.Create(&review).Error; err != nil {
		t.Fatal(err)
	}
	key := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead}, nil)
	router := env.router()
	get := func(query string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/decks/"+deck.PublicID+"/package?"+query, nil)
		req.Header.Set("Authorization", "Bearer "+key.Plaintext)
		r := httptest.NewRecorder()
		router.ServeHTTP(r, req)
		if r.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", query, r.Code, r.Body.String())
		}
		return r
	}
	if r := get(""); r.Header().Get("Content-Type") != "application/vnd.engram.edeck" {
		t.Fatalf("default content type: %s", r.Header().Get("Content-Type"))
	}
	if r := get("format=edeck"); r.Header().Get("Content-Type") != "application/vnd.engram.edeck" {
		t.Fatalf("edeck content type: %s", r.Header().Get("Content-Type"))
	}
	for _, off := range []string{"", "include_progress", "include_reviews", "include_deck_configs", "include_media", "include_weights"} {
		name := off
		if name == "" {
			name = "all_on"
		}
		t.Run(name, func(t *testing.T) {
			q := url.Values{"format": {"apkg"}, "include_progress": {"1"}, "include_reviews": {"1"}, "include_deck_configs": {"1"}, "include_media": {"1"}, "include_weights": {"1"}}
			if off != "" {
				q.Set(off, "0")
			}
			r := get(q.Encode())
			if got := r.Header().Get("Content-Type"); got != "application/zip" {
				t.Fatalf("content type %s", got)
			}
			z, err := zip.NewReader(bytes.NewReader(r.Body.Bytes()), int64(r.Body.Len()))
			if err != nil {
				t.Fatal(err)
			}
			entries := map[string][]byte{}
			for _, f := range z.File {
				reader, err := f.Open()
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(reader)
				reader.Close()
				if err != nil {
					t.Fatal(err)
				}
				entries[f.Name] = data
			}
			path := filepath.Join(t.TempDir(), "collection.db")
			if err := os.WriteFile(path, entries["collection.anki21"], 0600); err != nil {
				t.Fatal(err)
			}
			col, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer col.Close()
			var typ, queue, due, reps, revs int
			if err := col.QueryRow("SELECT type, queue, due, reps FROM cards").Scan(&typ, &queue, &due, &reps); err != nil {
				t.Fatal(err)
			}
			if err := col.QueryRow("SELECT count(*) FROM revlog").Scan(&revs); err != nil {
				t.Fatal(err)
			}
			if off == "include_progress" {
				if typ != 0 || queue != 0 || due != 1 || reps != 0 {
					t.Fatalf("new card: %d/%d/%d/%d", typ, queue, due, reps)
				}
			} else if typ != 2 || queue != 2 || reps != 3 {
				t.Fatalf("progress: %d/%d/%d/%d", typ, queue, due, reps)
			}
			wantRevs := 1
			if off == "include_reviews" {
				wantRevs = 0
			}
			if revs != wantRevs {
				t.Fatalf("reviews %d want %d", revs, wantRevs)
			}
			var rawConf string
			if err := col.QueryRow("SELECT dconf FROM col").Scan(&rawConf); err != nil {
				t.Fatal(err)
			}
			var configs map[string]map[string]any
			if err := json.Unmarshal([]byte(rawConf), &configs); err != nil {
				t.Fatal(err)
			}
			if off == "include_deck_configs" {
				if rawConf != "{}" {
					t.Fatalf("dconf: %s", rawConf)
				}
			} else {
				if len(configs) != 1 {
					t.Fatalf("dconf: %s", rawConf)
				}
				for _, c := range configs {
					params, ok := c["fsrsParams6"].([]any)
					if !ok {
						t.Fatalf("fsrsParams6: %v", c)
					}
					want := 21
					if off == "include_weights" {
						want = 0
					}
					if len(params) != want || c["desiredRetention"] != 0.87 {
						t.Fatalf("config: %v", c)
					}
				}
			}
			var mediaIndex map[string]string
			if err := json.Unmarshal(entries["media"], &mediaIndex); err != nil {
				t.Fatal(err)
			}
			numbered := 0
			for n := range entries {
				if _, err := strconv.Atoi(n); err == nil {
					numbered++
				}
			}
			if off == "include_media" {
				if len(mediaIndex) != 0 || numbered != 0 {
					t.Fatalf("media: %v numbered %d", mediaIndex, numbered)
				}
				report, err := env.api.ImportDeckPackage(ctx, owner, nil, bytes.NewReader(r.Body.Bytes()), store.PackageImportOptions{Target: "new_deck", DryRun: true, SkipMissingMedia: true})
				if err != nil {
					t.Fatalf("import without media: %v", err)
				}
				if report.NotesCreated != 1 {
					t.Fatalf("import report: %+v", report)
				}
			} else if len(mediaIndex) != 1 || numbered != 1 {
				t.Fatalf("media: %v numbered %d", mediaIndex, numbered)
			}
		})
	}
}
