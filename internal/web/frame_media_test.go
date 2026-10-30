package web

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/media"
	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

// This file covers GET /fm/<token>/<name>: a sandboxed frame has no session, so media reads there are authorized by an
// HMAC-signed token instead of the session cookie.
//
// The rule under test: the signature, the expiry, the token user's current status and deck access, the media-name lookup
// and the user's readability of the medium. Every failure is the same bare 404, so a caller cannot learn which check rejected it.

// frameMediaFixture is one server with two users, two decks and real media bytes on disk.
type frameMediaFixture struct {
	srv    *Server
	db     *gorm.DB
	owner  *store.User
	reader *store.User
	// deck is owned by owner and shared with reader as a reader; otherDeck is owned by owner and not shared with reader.
	deck      *store.Deck
	otherDeck *store.Deck
	// readable is media uploaded by owner and referenced by a note in deck: readable by reader through deck access.
	readable    *store.Media
	readableRaw []byte
	// foreign is media uploaded by a third user and referenced by no note: not readable by owner or reader.
	foreign *store.Media

	ownerCookies  []*http.Cookie
	ownerCSRF     string
	readerCookies []*http.Cookie
	readerCSRF    string
	// overrideToken replaces the token the fixture issued, for the rejection cases that need a broken or foreign token.
	overrideToken string
}

// newFrameMediaFixture builds the server and the fixtures described on frameMediaFixture.
func newFrameMediaFixture(t *testing.T) *frameMediaFixture {
	t.Helper()
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "frame.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	users := store.NewUserStore(db)
	sessions := store.NewSessionStore(db)
	accounts, err := auth.NewAccountService(users, sessions, store.NewAPIKeyStore(db), auth.NewPasswordHasher(auth.Params{
		Memory: 8 * 1024, Time: 1, Threads: 1, SaltLength: 16, KeyLength: 32,
	}))
	if err != nil {
		t.Fatalf("NewAccountService() error = %v", err)
	}
	mgr, err := auth.NewSessionManager(users, sessions, auth.SessionConfig{Secret: testSessionSecret})
	if err != nil {
		t.Fatalf("NewSessionManager() error = %v", err)
	}
	auditor, err := auth.NewAuditor(store.NewAuditStore(db), nil)
	if err != nil {
		t.Fatalf("NewAuditor() error = %v", err)
	}
	mediaStore, err := media.New(filepath.Join(t.TempDir(), "media"), db)
	if err != nil {
		t.Fatalf("media.New() error = %v", err)
	}
	apiSrv, err := api.New(api.Deps{
		DB:        db,
		Logger:    discardLogger(),
		Keys:      store.NewAPIKeyStore(db),
		Users:     users,
		Decks:     store.NewDeckStore(db),
		Notes:     store.NewNoteStore(db),
		Presets:   store.NewPresetStore(db),
		Cards:     store.NewCardStore(db),
		Auditor:   auditor,
		MediaRoot: mediaStore.Root(),
	})
	if err != nil {
		t.Fatalf("api.New() error = %v", err)
	}
	codec, err := store.NewSecretCodec(testEncryptionKey())
	if err != nil {
		t.Fatalf("NewSecretCodec() error = %v", err)
	}
	srv, err := New("127.0.0.1:0", Deps{
		DB:            db,
		Logger:        discardLogger(),
		SchemaVersion: func(ctx context.Context) (int, error) { return store.CurrentVersion(ctx, db) },
		Accounts:      accounts,
		Sessions:      mgr,
		Users:         users,
		UserLocale:    SessionUserLocale,
		Decks:         store.NewDeckStore(db),
		Notes:         store.NewNoteStore(db),
		Cards:         store.NewCardStore(db),
		Presets:       store.NewPresetStore(db),
		Auditor:       auditor,
		Media:         mediaStore,
		Secrets:       codec,
		API:           apiSrv,
		LoginLimiter: auth.NewLoginLimiter(auth.LimiterConfig{
			Sleep: func(context.Context, time.Duration) error { return nil },
		}),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	f := &frameMediaFixture{srv: srv, db: db}
	f.owner = createFrameMediaUser(t, accounts, "frame-owner", store.RoleAdmin)
	f.reader = createFrameMediaUser(t, accounts, "frame-reader", store.RoleUser)
	stranger := createFrameMediaUser(t, accounts, "frame-stranger", store.RoleUser)
	f.ownerCookies, f.ownerCSRF = loginJSON(t, srv, db, "frame-owner", "Sup3rSecret!")
	f.readerCookies, f.readerCSRF = loginJSON(t, srv, db, "frame-reader", "Sup3rSecret!")

	f.deck = seedDeck(t, db, f.owner.ID, "Frame shared")
	if err := store.NewGrantStore(db).Grant(context.Background(), f.deck.ID, f.reader.ID, store.RoleReader, store.Ptr(f.owner.ID)); err != nil {
		t.Fatalf("grant reader role: %v", err)
	}
	f.otherDeck = seedDeck(t, db, f.owner.ID, "Frame private")

	f.readableRaw = append([]byte("\x89PNG\r\n\x1a\n"), []byte("frame-media-readable")...)
	f.readable = saveFrameMedia(t, db, srv, f.readableRaw, f.owner.ID)
	// The frame route resolves a request through media_names; register both the bare sha and the migration's
	// "<sha>.<ext>" form so the tests exercise the name lookup rather than a legacy fallback.
	names := store.NewMediaNameStore(db)
	if err := names.Upsert(context.Background(), f.deck.ID, f.readable.Sha256, f.readable.Sha256); err != nil {
		t.Fatalf("register bare media name: %v", err)
	}
	if err := names.Upsert(context.Background(), f.deck.ID, f.readable.Sha256+".png", f.readable.Sha256); err != nil {
		t.Fatalf("register media name with extension: %v", err)
	}
	// The note makes the medium readable for everyone who can see the deck, including the reader.
	if _, err := store.NewNoteStore(db).Create(context.Background(), &store.Note{
		DeckID: f.deck.ID, NoteTypeID: store.Ptr(stockNoteTypeID(t, db, f.deck.ID, "basic")), CreatedBy: store.Ptr(f.owner.ID),
	}, map[string]any{
		"front": fmt.Sprintf(`<img src="%s.png">`, f.readable.Sha256),
		"back":  "back",
	}); err != nil {
		t.Fatalf("create media note: %v", err)
	}
	f.foreign = saveFrameMedia(t, db, srv,
		append([]byte("\x89PNG\r\n\x1a\n"), []byte("frame-media-foreign")...), stranger.ID)
	return f
}

// createFrameMediaUser creates an active local account.
func createFrameMediaUser(t *testing.T, accounts *auth.AccountService, username, role string) *store.User {
	t.Helper()
	user, err := accounts.CreateLocalUser(context.Background(), auth.CreateUserInput{
		Username: username,
		Email:    username + "@example.com",
		Password: "Sup3rSecret!",
		Role:     role,
	})
	if err != nil {
		t.Fatalf("CreateLocalUser(%s) error = %v", username, err)
	}
	return user
}

// saveFrameMedia writes real media bytes through the storage layer and returns the stored medium.
func saveFrameMedia(t *testing.T, db *gorm.DB, srv *Server, raw []byte, uploader uint64) *store.Media {
	t.Helper()
	m, err := store.NewMediaStore(db).SaveBytes(context.Background(), srv.media.Root(), "image/png", raw, store.Ptr(uploader))
	if err != nil {
		t.Fatalf("SaveBytes() error = %v", err)
	}
	return m
}

// token issues a frame media token for the given user and deck at the current time.
func (f *frameMediaFixture) token(t *testing.T, userID, deckID uint64) string {
	t.Helper()
	token, err := f.srv.frameMediaToken(context.Background(), userID, deckID, time.Now())
	if err != nil {
		t.Fatalf("frameMediaToken() error = %v", err)
	}
	return token
}

// get requests a frame media path without any cookie: the token alone must authorize it.
func (f *frameMediaFixture) get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	return getWithCookies(t, f.srv, path, nil)
}

// frameRenderResponse is the subset of the review render response the tests read.
type frameRenderResponse struct {
	FrontHTML   string   `json:"front_html"`
	BackHTML    string   `json:"back_html"`
	OptionsHTML []string `json:"options_html"`
	MediaBase   string   `json:"media_base"`
}

// renderCard posts to the SPA review render endpoint as the reader, optionally marking the request as frame output.
func (f *frameMediaFixture) renderCard(t *testing.T, cardPublicID string, forFrame bool) frameRenderResponse {
	t.Helper()
	rec := f.postJSON(t, "/api/v1/review/render",
		map[string]any{"card_id": cardPublicID, "deck": []string{f.deck.PublicID}},
		f.readerCookies, f.readerCSRF, forFrame)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/review/render = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var out frameRenderResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode render response: %v (body %s)", err, rec.Body.String())
	}
	return out
}

// framePreviewResponse is the subset of the note preview response the tests read.
type framePreviewResponse struct {
	Cards []struct {
		FrontHTML string `json:"front_html"`
		BackHTML  string `json:"back_html"`
	} `json:"cards"`
	MediaBase string `json:"media_base"`
}

// preview posts to the editor preview endpoint as the deck owner, optionally marking the request as frame output.
// The helper takes a stock kind for readability and resolves the deck's note type for it before posting.
func (f *frameMediaFixture) preview(t *testing.T, kind string, fields map[string]any, forFrame bool) framePreviewResponse {
	t.Helper()
	payload, err := json.Marshal(notePreviewRequest{
		NoteType: stockNoteTypePublicID(t, f.db, f.deck.ID, kind),
		Fields:   fields,
	})
	if err != nil {
		t.Fatalf("encode preview request: %v", err)
	}
	rec := f.postJSON(t, "/api/v1/decks/"+f.deck.PublicID+"/notes/preview", json.RawMessage(payload),
		f.ownerCookies, f.ownerCSRF, forFrame)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var out framePreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode preview response: %v (body %s)", err, rec.Body.String())
	}
	return out
}

// postJSON sends a JSON body with the session CSRF token, optionally carrying the frame output marker.
func (f *frameMediaFixture) postJSON(t *testing.T, path string, body any, cookies []*http.Cookie, csrf string, forFrame bool) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	switch v := body.(type) {
	case json.RawMessage:
		raw = v
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("encode request body: %v", err)
		}
		raw = encoded
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	if forFrame {
		req.Header.Set(frameOutputHeader, "1")
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(rec, req)
	return rec
}

// TestFrameMediaServesReadableMedia covers the success case and the headers a successful response must carry.
func TestFrameMediaServesReadableMedia(t *testing.T) {
	f := newFrameMediaFixture(t)
	token := f.token(t, f.reader.ID, f.deck.ID)
	rec := f.get(t, "/fm/"+token+"/"+f.readable.Sha256)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /fm/<token>/<sha> = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "private" {
		t.Errorf("Cache-Control = %q, want private", got)
	}
	if got := rec.Header().Get("ETag"); got != `"`+f.readable.Sha256+`"` {
		t.Errorf("ETag = %q, want the sha256 validator", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", got)
	}
	if got := rec.Body.Bytes(); string(got) != string(f.readableRaw) {
		t.Errorf("body = %q, want the stored bytes", got)
	}
	// An ETag revalidation must answer 304 without re-reading the bytes.
	req := httptest.NewRequest(http.MethodGet, "/fm/"+token+"/"+f.readable.Sha256, nil)
	req.Header.Set("If-None-Match", `"`+f.readable.Sha256+`"`)
	revalidate := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(revalidate, req)
	if revalidate.Code != http.StatusNotModified {
		t.Errorf("revalidation = %d, want 304", revalidate.Code)
	}
}

// TestFrameMediaAcceptsExtensionName asserts that "<sha256>.<ext>" resolves to the same bytes: the extension is part of
// the registered name the migration writes.
func TestFrameMediaAcceptsExtensionName(t *testing.T) {
	f := newFrameMediaFixture(t)
	token := f.token(t, f.reader.ID, f.deck.ID)
	rec := f.get(t, "/fm/"+token+"/"+f.readable.Sha256+".png")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /fm/<token>/<sha>.png = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", got)
	}
}

// TestFrameMediaServesNoteTypeOnlyName is the acceptance case for a CSS-only asset: "_font.woff2" is registered in
// media_names but referenced by no note, and the frame route must still serve it (deck read access is the authorization).
func TestFrameMediaServesNoteTypeOnlyName(t *testing.T) {
	f := newFrameMediaFixture(t)
	font, err := store.NewMediaStore(f.db).SaveBytes(context.Background(), f.srv.media.Root(), "font/woff2",
		[]byte("wOF2\x00\x01\x00\x00fake-font"), store.Ptr(f.owner.ID))
	if err != nil {
		t.Fatalf("SaveBytes(font) error = %v", err)
	}
	if err := store.NewMediaNameStore(f.db).Upsert(context.Background(), f.deck.ID, "_font.woff2", font.Sha256); err != nil {
		t.Fatalf("register font name: %v", err)
	}
	token := f.token(t, f.reader.ID, f.deck.ID)
	rec := f.get(t, "/fm/"+token+"/_font.woff2")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /fm/<token>/_font.woff2 = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "font/woff2" {
		t.Errorf("Content-Type = %q, want font/woff2", got)
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "attachment") {
		t.Errorf("Content-Disposition = %q, want attachment for a font", got)
	}
	if got := rec.Header().Get("Content-Security-Policy"); got != "sandbox" {
		t.Errorf("Content-Security-Policy = %q, want sandbox", got)
	}

	unknown := f.get(t, "/fm/"+token+"/_missing.woff2")
	if unknown.Code != http.StatusNotFound {
		t.Errorf("GET unknown media name = %d, want 404", unknown.Code)
	}
}

// TestFrameMediaServesEditorAbsoluteReference is the end-to-end defect case: the editor writes /media/<sha>, the note
// write stores the deck name, a frame-marked render returns that relative name, and the frame route serves it.
func TestFrameMediaServesEditorAbsoluteReference(t *testing.T) {
	f := newFrameMediaFixture(t)
	ctx := context.Background()
	media := saveFrameMedia(t, f.db, f.srv, append([]byte("\x89PNG\r\n\x1a\n"), []byte("editor-absolute")...), f.owner.ID)
	cards, err := store.NewNoteStore(f.db).Create(ctx, &store.Note{
		DeckID: f.deck.ID, NoteTypeID: store.Ptr(stockNoteTypeID(t, f.db, f.deck.ID, "basic")), CreatedBy: store.Ptr(f.owner.ID),
	}, map[string]any{
		"front": `<img src="/media/` + media.Sha256 + `">`,
		"back":  "back",
	})
	if err != nil || len(cards) == 0 {
		t.Fatalf("create note: %v (%d cards)", err, len(cards))
	}
	name := media.Sha256 + ".png"
	rendered := f.renderCard(t, cards[0].PublicID, true)
	if !strings.Contains(rendered.FrontHTML, `src="`+name+`"`) {
		t.Fatalf("frame-rendered front_html = %q, want the relative media name %q", rendered.FrontHTML, name)
	}
	if strings.Contains(rendered.FrontHTML, "/media/") {
		t.Fatalf("frame-rendered front_html kept an absolute media URL: %s", rendered.FrontHTML)
	}

	token := f.token(t, f.reader.ID, f.deck.ID)
	rec := f.get(t, "/fm/"+token+"/"+name)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /fm/<token>/%s = %d, want 200 (body %q)", name, rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", got)
	}
}

// TestFrameMediaRejectsNameOnlyKnownInAnotherDeck pins deck scoping: the reader can reach the other deck and its
// media_names row, but the token is issued for f.deck, which names neither the file nor the sha, so the request is 404.
func TestFrameMediaRejectsNameOnlyKnownInAnotherDeck(t *testing.T) {
	f := newFrameMediaFixture(t)
	ctx := context.Background()
	other := seedDeck(t, f.db, f.owner.ID, "Frame other shared")
	if err := store.NewGrantStore(f.db).Grant(ctx, other.ID, f.reader.ID, store.RoleReader, store.Ptr(f.owner.ID)); err != nil {
		t.Fatalf("grant other deck: %v", err)
	}
	m := saveFrameMedia(t, f.db, f.srv, append([]byte("\x89PNG\r\n\x1a\n"), []byte("other-deck")...), f.owner.ID)
	name := m.Sha256 + ".png"
	if err := store.NewMediaNameStore(f.db).Upsert(ctx, other.ID, name, m.Sha256); err != nil {
		t.Fatalf("register name in other deck: %v", err)
	}
	token := f.token(t, f.reader.ID, f.deck.ID)
	rec := f.get(t, "/fm/"+token+"/"+name)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /fm/<token>/%s with the name known only in another deck = %d, want 404", name, rec.Code)
	}
}

// TestFrameMediaRejectsRequests covers every rejection: expired token, tampered signature, lost deck access, unknown
// name, a token bound to another deck, a malformed token, an unknown name and a token whose user no longer exists.
// Every case must be the same bare 404: no reason in the body and no success headers.
func TestFrameMediaRejectsRequests(t *testing.T) {
	cases := []struct {
		name string
		// path builds the request path from the fixture; prepare may mutate state after the token is issued.
		path    func(f *frameMediaFixture, token string) string
		prepare func(t *testing.T, f *frameMediaFixture)
	}{
		{
			name: "expired token",
			path: func(f *frameMediaFixture, token string) string { return "/fm/" + token + "/" + f.readable.Sha256 },
			prepare: func(t *testing.T, f *frameMediaFixture) {
				// Re-issuing at a past time puts the expiry one minute before now.
				expired, err := f.srv.frameMediaToken(context.Background(), f.reader.ID, f.deck.ID,
					time.Now().Add(-frameMediaTokenTTL-time.Minute))
				if err != nil {
					t.Fatalf("frameMediaToken() error = %v", err)
				}
				f.overrideToken = expired
			},
		},
		{
			name: "tampered signature",
			path: func(f *frameMediaFixture, token string) string { return "/fm/" + token + "/" + f.readable.Sha256 },
			prepare: func(t *testing.T, f *frameMediaFixture) {
				f.overrideToken = tamperFrameMediaToken(f.overrideToken)
			},
		},
		{
			name: "user lost access to the deck",
			path: func(f *frameMediaFixture, token string) string { return "/fm/" + token + "/" + f.readable.Sha256 },
			prepare: func(t *testing.T, f *frameMediaFixture) {
				if err := store.NewGrantStore(f.db).Revoke(context.Background(), f.deck.ID, f.reader.ID); err != nil {
					t.Fatalf("revoke grant: %v", err)
				}
			},
		},
		{
			name: "sha not readable in that deck",
			path: func(f *frameMediaFixture, token string) string { return "/fm/" + token + "/" + f.foreign.Sha256 },
		},
		{
			name: "token issued for another deck",
			path: func(f *frameMediaFixture, token string) string { return "/fm/" + token + "/" + f.readable.Sha256 },
		},
		{
			name: "malformed token",
			path: func(f *frameMediaFixture, token string) string { return "/fm/" + token + "/" + f.readable.Sha256 },
			prepare: func(t *testing.T, f *frameMediaFixture) {
				f.overrideToken = "not-a-token"
			},
		},
		{
			name: "token with trailing data",
			path: func(f *frameMediaFixture, token string) string { return "/fm/" + token + "/" + f.readable.Sha256 },
			prepare: func(t *testing.T, f *frameMediaFixture) {
				f.overrideToken += ".extra"
			},
		},
		{
			name: "name is not a sha256",
			path: func(f *frameMediaFixture, token string) string {
				return "/fm/" + token + "/" + strings.ToUpper(f.readable.Sha256)
			},
		},
		{
			name: "token user does not exist",
			path: func(f *frameMediaFixture, token string) string { return "/fm/" + token + "/" + f.readable.Sha256 },
			prepare: func(t *testing.T, f *frameMediaFixture) {
				token, err := f.srv.frameMediaToken(context.Background(), 999999, f.deck.ID, time.Now())
				if err != nil {
					t.Fatalf("frameMediaToken() error = %v", err)
				}
				f.overrideToken = token
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFrameMediaFixture(t)
			// The "token issued for another deck" case is the only one whose token is bound to otherDeck; everything else
			// is bound to the shared deck.
			deckID := f.deck.ID
			if tc.name == "token issued for another deck" {
				deckID = f.otherDeck.ID
			}
			f.overrideToken = f.token(t, f.reader.ID, deckID)
			if tc.prepare != nil {
				tc.prepare(t, f)
			}
			rec := f.get(t, tc.path(f, f.overrideToken))
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404 (body %q)", rec.Code, rec.Body.String())
			}
			if rec.Body.Len() != 0 {
				t.Errorf("body = %q, want empty: a rejection must not name the failed check", rec.Body.String())
			}
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
				t.Errorf("Access-Control-Allow-Origin = %q on a rejection, want none", got)
			}
			if got := rec.Header().Get("Cache-Control"); got != "" {
				t.Errorf("Cache-Control = %q on a rejection, want none", got)
			}
		})
	}
}

// TestFrameMediaKeyIsCreatedOnceAndStoredEncrypted asserts the key lifecycle: the first use creates one encrypted settings
// row, concurrent first requests all succeed against the same key, and the stored value never holds the plaintext key.
func TestFrameMediaKeyIsCreatedOnceAndStoredEncrypted(t *testing.T) {
	f := newFrameMediaFixture(t)
	ctx := context.Background()
	if !store.IsSensitiveSettingKey(frameMediaKeySetting) {
		t.Fatalf("%q must be treated as a sensitive setting key", frameMediaKeySetting)
	}
	var before int64
	if err := f.db.Model(&store.Setting{}).Where("key = ?", frameMediaKeySetting).Count(&before).Error; err != nil {
		t.Fatalf("count settings: %v", err)
	}
	if before != 0 {
		t.Fatalf("frame media key exists before first use (%d rows)", before)
	}
	now := time.Now()
	const workers = 8
	tokens := make([]string, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tokens[i], errs[i] = f.srv.frameMediaToken(ctx, f.reader.ID, f.deck.ID, now)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent token issue %d: %v", i, err)
		}
	}
	key, err := f.srv.frameMediaSigningKey(ctx)
	if err != nil {
		t.Fatalf("frameMediaSigningKey() error = %v", err)
	}
	if len(key) != frameMediaKeyBytes {
		t.Fatalf("key length = %d, want %d", len(key), frameMediaKeyBytes)
	}
	for i, token := range tokens {
		if _, ok := f.srv.verifyFrameMediaToken(ctx, token, now); !ok {
			t.Errorf("token %d issued during the first-use race does not verify", i)
		}
	}
	var rows []store.Setting
	if err := f.db.Where("key = ?", frameMediaKeySetting).Find(&rows).Error; err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("frame media key rows = %d, want 1", len(rows))
	}
	if strings.Contains(rows[0].Value, base64.RawURLEncoding.EncodeToString(key)) {
		t.Error("the settings row contains the plaintext key")
	}
	stored, configured, err := store.GetSecret(ctx, f.db, f.srv.secrets, frameMediaKeySetting)
	if err != nil || !configured {
		t.Fatalf("GetSecret() = (%v, %v), want a configured key", configured, err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(stored)
	if err != nil || !hmac.Equal(decoded, key) {
		t.Errorf("stored key does not round-trip: %v", err)
	}
}

// TestFrameMediaTokenLifetime asserts the issued expiry is now + the six hour lifetime.
func TestFrameMediaTokenLifetime(t *testing.T) {
	f := newFrameMediaFixture(t)
	now := time.Now()
	token := f.token(t, f.reader.ID, f.deck.ID)
	payload, err := base64.RawURLEncoding.DecodeString(strings.SplitN(token, ".", 2)[0])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	claims, ok := parseFrameMediaPayload(string(payload))
	if !ok {
		t.Fatalf("parseFrameMediaPayload(%q) failed", payload)
	}
	if got := claims.expiry.Sub(now); got < frameMediaTokenTTL-time.Minute || got > frameMediaTokenTTL+time.Minute {
		t.Errorf("token lifetime = %v, want %v", got, frameMediaTokenTTL)
	}
}

// TestFrameMediaNameSHA was replaced by name resolution through media_names, covered by the tests below.

// TestCardFaceCarriesFrameMediaBase asserts that every response carrying a rendered card face also carries the frame media
// base, and that the face HTML uses relative media names the base resolves.
func TestCardFaceCarriesFrameMediaBase(t *testing.T) {
	f := newFrameMediaFixture(t)
	ctx := context.Background()
	cards, err := store.NewCardStore(f.db).ByNote(ctx, findNoteID(t, f.db, f.deck.ID))
	if err != nil || len(cards) == 0 {
		t.Fatalf("load card of the media note: %v (%d cards)", err, len(cards))
	}
	card := cards[0]
	view, err := f.srv.cardView(ctx, f.reader, schedule.QueueItem{CardID: card.ID})
	if err != nil {
		t.Fatalf("cardView() error = %v", err)
	}
	token := frameMediaTokenFromBase(t, view.MediaBase)
	if view.MediaBase != "/fm/"+token+"/" {
		t.Fatalf("MediaBase = %q, want /fm/<token>/", view.MediaBase)
	}
	// The view itself keeps absolute URLs: cardView is shared with every consumer, and only a marked request may rewrite.
	if !strings.Contains(view.FrontHTML, "/media/"+f.readable.Sha256) {
		t.Errorf("cardView front HTML lost the absolute media URL: %s", view.FrontHTML)
	}
	// The base must actually authorize a read of the name the marked request refers to.
	rec := f.get(t, view.MediaBase+f.readable.Sha256)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200 (body %q)", view.MediaBase+f.readable.Sha256, rec.Code, rec.Body.String())
	}

	// A default request is for the SPA: absolute URLs and the base, because the SPA has no <base> element.
	rendered := f.renderCard(t, card.PublicID, false)
	if rendered.MediaBase == "" || !strings.HasPrefix(rendered.MediaBase, "/fm/") {
		t.Errorf("render media_base = %q, want a /fm/ base", rendered.MediaBase)
	}
	if !strings.Contains(rendered.FrontHTML, "/media/"+f.readable.Sha256) {
		t.Errorf("default render front_html = %s, want the absolute media URL", rendered.FrontHTML)
	}

	// The editor preview carries the same fields and follows the same rule.
	fields := map[string]any{"front": fmt.Sprintf(`<img src="%s.png">`, f.readable.Sha256), "back": "back"}
	preview := f.preview(t, "basic", fields, false)
	if preview.MediaBase == "" || !strings.HasPrefix(preview.MediaBase, "/fm/") {
		t.Errorf("preview media_base = %q, want a /fm/ base", preview.MediaBase)
	}
	if len(preview.Cards) != 1 || !strings.Contains(preview.Cards[0].FrontHTML, "/media/"+f.readable.Sha256) {
		t.Errorf("default preview front_html = %+v, want the absolute media URL", preview.Cards)
	}
}

// TestCardFaceFrameOutputRewritesFaces covers the marked request: only the card faces that move into the frame lose the
// /media/ prefix, and choice options keep their absolute URLs because they stay in the parent page.
func TestCardFaceFrameOutputRewritesFaces(t *testing.T) {
	f := newFrameMediaFixture(t)
	ctx := context.Background()
	// A choice card whose option carries media: its options must survive a frame request unchanged.
	choiceFields := map[string]any{
		"question": "pick one",
		"options":  []any{fmt.Sprintf(`<img src="%s.png">`, f.readable.Sha256), "plain"},
		"answer":   1,
	}
	choiceCards, err := store.NewNoteStore(f.db).Create(ctx, &store.Note{
		DeckID: f.deck.ID, NoteTypeID: store.Ptr(stockNoteTypeID(t, f.db, f.deck.ID, "choice_single")), CreatedBy: store.Ptr(f.owner.ID),
	}, choiceFields)
	if err != nil || len(choiceCards) == 0 {
		t.Fatalf("create choice note: %v (%d cards)", err, len(choiceCards))
	}
	card := choiceCards[0]

	rendered := f.renderCard(t, card.PublicID, true)
	if rendered.MediaBase == "" || !strings.HasPrefix(rendered.MediaBase, "/fm/") {
		t.Errorf("marked render media_base = %q, want a /fm/ base", rendered.MediaBase)
	}
	if strings.Contains(rendered.FrontHTML, "/media/") {
		t.Errorf("marked render front_html keeps an absolute media URL: %s", rendered.FrontHTML)
	}
	if strings.Contains(rendered.BackHTML, "/media/") {
		t.Errorf("marked render back_html keeps an absolute media URL: %s", rendered.BackHTML)
	}
	if len(rendered.OptionsHTML) != 2 {
		t.Fatalf("marked render options_html = %d entries, want 2", len(rendered.OptionsHTML))
	}
	if !strings.Contains(rendered.OptionsHTML[0], "/media/"+f.readable.Sha256) {
		t.Errorf("marked render rewrote a choice option, want absolute: %s", rendered.OptionsHTML[0])
	}

	// The preview path follows the same marker: only a marked request receives relative names.
	fields := map[string]any{"front": fmt.Sprintf(`<img src="%s.png">`, f.readable.Sha256), "back": "back"}
	framePreview := f.preview(t, "basic", fields, true)
	if len(framePreview.Cards) != 1 {
		t.Fatalf("marked preview cards = %d, want 1", len(framePreview.Cards))
	}
	if strings.Contains(framePreview.Cards[0].FrontHTML, "/media/") {
		t.Errorf("marked preview front_html keeps an absolute media URL: %s", framePreview.Cards[0].FrontHTML)
	}
	if !strings.Contains(framePreview.Cards[0].FrontHTML, f.readable.Sha256) {
		t.Errorf("marked preview front_html lost the media reference: %s", framePreview.Cards[0].FrontHTML)
	}
	if framePreview.MediaBase == "" {
		t.Error("marked preview media_base is empty")
	}
}

// TestAbsoluteMediaRefs pins the rewrite boundary in the direction the SPA needs: a deck-scoped media name in a src or
// href attribute or a CSS url() becomes the absolute /media/<sha256> route, and nothing else is touched.
func TestAbsoluteMediaRefs(t *testing.T) {
	sha := strings.Repeat("ab", 32)
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"double-quoted src with extension", `<img src="` + sha + `.png">`, `<img src="/media/` + sha + `">`},
		{"single-quoted src", `<img src='` + sha + `.png'>`, `<img src='/media/` + sha + `'>`},
		{"bare sha", `<img src="` + sha + `">`, `<img src="/media/` + sha + `">`},
		{"href with extension", `<a href="` + sha + `.png">x</a>`, `<a href="/media/` + sha + `">x</a>`},
		{"single-quoted href", `<a href='` + sha + `.bin'>x</a>`, `<a href='/media/` + sha + `'>x</a>`},
		{"bare href", `<a href="` + sha + `">x</a>`, `<a href="/media/` + sha + `">x</a>`},
		{"css url", `<div style="background:url(` + sha + `.jpg)">`, `<div style="background:url(/media/` + sha + `)">`},
		{"quoted css url", `<div style="background:url('` + sha + `.jpg')">`, `<div style="background:url('/media/` + sha + `')">`},
		{"external host untouched", `<img src="https://cdn.example.com/a.png">`, `<img src="https://cdn.example.com/a.png">`},
		{"absolute media path untouched", `<img src="/media/` + sha + `">`, `<img src="/media/` + sha + `">`},
		{"plain text untouched", `<p>` + sha + `.png</p>`, `<p>` + sha + `.png</p>`},
		{"not a sha", `<img src="abc.png">`, `<img src="abc.png">`},
		{"uppercase hex untouched", `<img src="` + strings.ToUpper(sha) + `.png">`, `<img src="` + strings.ToUpper(sha) + `.png">`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := absoluteMediaRefs(tc.in); got != tc.want {
				t.Fatalf("absoluteMediaRefs(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestFrameMediaWithoutSecretsDegrades asserts the documented degradation: without the encrypted settings channel no token
// can be issued or verified, so every card-face response carries an empty media base, the /fm/ route answers 404, and the
// rendered HTML keeps its absolute references.
func TestFrameMediaWithoutSecretsDegrades(t *testing.T) {
	srv, _, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedDeck(t, srv.db, ownerID, "No secrets")
	if base := srv.frameMediaBase(context.Background(), ownerID, deck.ID); base != "" {
		t.Errorf("frameMediaBase() = %q without secrets, want empty", base)
	}
	if rec := getWithCookies(t, srv, "/fm/anything/"+strings.Repeat("ab", 32), nil); rec.Code != http.StatusNotFound {
		t.Errorf("GET /fm/ without secrets = %d, want 404", rec.Code)
	}
	payload, _ := json.Marshal(notePreviewRequest{NoteType: stockNoteTypePublicID(t, srv.db, deck.ID, "basic"), Fields: map[string]any{
		"front": `<img src="` + strings.Repeat("ab", 32) + `.png">`, "back": "back",
	}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/decks/"+deck.PublicID+"/notes/preview", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Cards []struct {
			FrontHTML string `json:"front_html"`
		} `json:"cards"`
		MediaBase string `json:"media_base"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode preview response: %v (body %s)", err, rec.Body.String())
	}
	if body.MediaBase != "" {
		t.Errorf("preview media_base = %q without secrets, want empty", body.MediaBase)
	}
	if len(body.Cards) != 1 || !strings.Contains(body.Cards[0].FrontHTML, "/media/") {
		t.Errorf("preview front_html = %+v, want the absolute media URL preserved", body.Cards)
	}
}

// tamperFrameMediaToken flips one signature character, which keeps the token well-formed but invalid.
func tamperFrameMediaToken(token string) string {
	i := strings.LastIndex(token, ".")
	if i < 0 || i+1 >= len(token) {
		return token + "A"
	}
	signature := token[i+1:]
	replacement := byte('A')
	if signature[0] == 'A' {
		replacement = 'B'
	}
	return token[:i+1] + string(replacement) + signature[1:]
}

// frameMediaTokenFromBase extracts the token from a "/fm/<token>/" base.
func frameMediaTokenFromBase(t *testing.T, base string) string {
	t.Helper()
	if !strings.HasPrefix(base, "/fm/") || !strings.HasSuffix(base, "/") {
		t.Fatalf("media base = %q, want /fm/<token>/", base)
	}
	return strings.TrimSuffix(strings.TrimPrefix(base, "/fm/"), "/")
}

// findNoteID returns the first note of a deck.
func findNoteID(t *testing.T, db *gorm.DB, deckID uint64) uint64 {
	t.Helper()
	var note store.Note
	if err := db.Where("deck_id = ?", deckID).Order("id").First(&note).Error; err != nil {
		t.Fatalf("load note of deck %d: %v", deckID, err)
	}
	return note.ID
}
