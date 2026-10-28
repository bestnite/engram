package web

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// The part of the deck package browser path still in the web layer: the export download. Import itself has moved to REST
// `POST /api/v1/decks/import` (the SPA's client.ts uses it), so the web layer keeps only the export and the /import shell.

// TestDeckPackageExportServesAttachment asserts the export endpoint's response shape and auth: a signed-in user's download gets
// an .edeck attachment (same MIME as the REST export), and anonymous access is redirected to the login page.
func TestDeckPackageExportServesAttachment(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Pack Deck")
	seedBasic(t, db, deck.ID, "Q1", "A1")

	exp := getWithCookies(t, srv, "/decks/"+deck.PublicID+"/package", cookies)
	if exp.Code != http.StatusOK {
		t.Fatalf("GET export = %d, want 200 (body %s)", exp.Code, snippet(exp.Body.String()))
	}
	if ct := exp.Header().Get("Content-Type"); ct != packageExportMediaType {
		t.Errorf("export content-type = %q, want %q", ct, packageExportMediaType)
	}
	if got := downloadName(t, exp); got != "Pack Deck.edeck" {
		t.Errorf("export filename = %q, want the deck title %q", got, "Pack Deck.edeck")
	}
	if exp.Body.Len() == 0 {
		t.Error("exported package is empty")
	}

	anon := getWithCookies(t, srv, "/decks/"+deck.PublicID+"/package", nil)
	if anon.Code != http.StatusSeeOther {
		t.Errorf("anonymous export = %d, want 303 redirect to login", anon.Code)
	}
}

// downloadName parses the download filename out of Content-Disposition (including the UTF-8 filename* form).
func downloadName(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	cd := rec.Header().Get("Content-Disposition")
	disposition, params, err := mime.ParseMediaType(cd)
	if err != nil || disposition != "attachment" {
		t.Fatalf("Content-Disposition = %q, want an attachment (parse error %v)", cd, err)
	}
	return params["filename"]
}

// TestDeckPackageDownloadsAreNamedAfterTheDeck asserts the REST export the SPA actually calls names the file after the deck title,
// with a Chinese title restored verbatim after filename* encoding, rather than using the public id.
func TestDeckPackageDownloadsAreNamedAfterTheDeck(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "日语 N2 词汇")
	seedBasic(t, db, deck.ID, "Q1", "A1")

	rec := getWithCookies(t, srv, "/api/v1/decks/"+deck.PublicID+"/package", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET REST export = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if got := downloadName(t, rec); got != "日语 N2 词汇.edeck" {
		t.Errorf("REST export filename = %q, want %q", got, "日语 N2 词汇.edeck")
	}
	if strings.Contains(rec.Header().Get("Content-Disposition"), deck.PublicID) {
		t.Errorf("Content-Disposition %q still carries the deck id", rec.Header().Get("Content-Disposition"))
	}
}

// TestBatchExportNamesEntriesAfterDecksWithoutCollisions asserts batch export zip entries are named after deck titles,
// and same-title decks get a sequence number appended instead of overwriting each other.
func TestBatchExportNamesEntriesAfterDecksWithoutCollisions(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	first := seedDeck(t, db, ownerID, "Biology")
	second := seedDeck(t, db, ownerID, "Biology")
	third := seedDeck(t, db, ownerID, "a/b")

	rec := postJSONWithCSRF(t, srv, "/api/v1/decks/export-zip", map[string]any{
		"deck_ids": []string{first.PublicID, second.PublicID, third.PublicID},
	}, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST export-zip = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("read batch zip: %v", err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	want := []string{"Biology.edeck", "Biology (2).edeck", "a_b.edeck"}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Errorf("zip entries = %q, want %q", names, want)
	}
}

// TestImportPageServesShell asserts /import returns the SPA shell, with route authorization handled by the API session endpoints.
func TestImportPageServesShell(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	rec := getWithCookies(t, srv, "/import", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("anonymous GET /import = %d (%q), want SPA HTML shell", rec.Code, rec.Header().Get("Content-Type"))
	}
}

// seedMediaDeck creates a deck, puts in a note referencing media, and writes the media bytes into the server's media root; returns the deck and the media bytes.
func seedMediaDeck(t *testing.T, srv *Server, db *gorm.DB, ownerID uint64, name string) (*store.Deck, []byte) {
	t.Helper()
	deck := seedDeck(t, db, ownerID, name)
	raw := append([]byte("\x89PNG\r\n\x1a\n"), []byte("web-export-media-payload")...)
	m, err := store.NewMediaStore(db).SaveBytes(context.Background(), srv.media.Root(), "image/png", raw, store.Ptr(ownerID))
	if err != nil {
		t.Fatalf("SaveBytes: %v", err)
	}
	if _, err := store.NewNoteStore(db).Create(context.Background(), &store.Note{DeckID: deck.ID, Kind: "basic"},
		map[string]any{"front": "img?", "back": "media/" + m.Sha256 + ".png"}); err != nil {
		t.Fatalf("create media note: %v", err)
	}
	return deck, raw
}

// packageHasMedia reports whether a .edeck blob carries a media entry with the given content.
func packageHasMedia(t *testing.T, edeck, want []byte) bool {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(edeck), int64(len(edeck)))
	if err != nil {
		t.Fatalf("read package zip: %v", err)
	}
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, "media/") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		got, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		if bytes.Equal(got, want) {
			return true
		}
	}
	return false
}

// TestWebPackageExportsInlineMedia asserts both web-side export routes read bytes from the media root like REST:
// a deck referencing media can be exported and the package carries that media, rather than returning 500 because the file was not found.
func TestWebPackageExportsInlineMedia(t *testing.T) {
	cases := []struct {
		name   string
		export func(t *testing.T, srv *Server, deck *store.Deck, cookies []*http.Cookie, csrf string) []byte
	}{
		{"single deck download", func(t *testing.T, srv *Server, deck *store.Deck, cookies []*http.Cookie, _ string) []byte {
			rec := getWithCookies(t, srv, "/decks/"+deck.PublicID+"/package", cookies)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET export = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
			}
			return rec.Body.Bytes()
		}},
		{"batch zip", func(t *testing.T, srv *Server, deck *store.Deck, cookies []*http.Cookie, csrf string) []byte {
			rec := postJSONWithCSRF(t, srv, "/api/v1/decks/export-zip", map[string]any{"deck_ids": []string{deck.PublicID}}, cookies, csrf)
			if rec.Code != http.StatusOK {
				t.Fatalf("POST export-zip = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
			}
			zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
			if err != nil || len(zr.File) != 1 {
				t.Fatalf("batch zip entries = %v (err %v), want exactly one package", zr, err)
			}
			rc, err := zr.File[0].Open()
			if err != nil {
				t.Fatalf("open batch entry: %v", err)
			}
			defer rc.Close()
			inner, err := io.ReadAll(rc)
			if err != nil {
				t.Fatalf("read batch entry: %v", err)
			}
			return inner
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, db, ownerID, cookies, csrf := newNotesServer(t)
			deck, raw := seedMediaDeck(t, srv, db, ownerID, "Media Deck")
			if !packageHasMedia(t, tc.export(t, srv, deck, cookies, csrf), raw) {
				t.Fatal("exported package does not carry the deck's media bytes")
			}
		})
	}
}
