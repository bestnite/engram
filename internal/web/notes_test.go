package web

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/media"
	"git.nite07.com/nite/engram/internal/store"
)

// newNotesServer wires a test server with account, session, deck and note stores, and signs in an owner.
// The returned csrf is the session-bound server token, for write-operation forms.
func newNotesServer(t *testing.T) (srv *Server, db *gorm.DB, ownerID uint64, cookies []*http.Cookie, csrf string) {
	t.Helper()
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "notes.db"))
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
	auditor, err := auth.NewAuditor(store.NewAuditStore(db))
	if err != nil {
		t.Fatalf("NewAuditor() error = %v", err)
	}
	mediaStore, err := media.New(filepath.Join(t.TempDir(), "media"), db)
	if err != nil {
		t.Fatalf("media.New() error = %v", err)
	}
	// Write operations such as import go through the api service (one entry for REST/MCP/CLI/web), so the test server must wire the API too;
	// MediaRoot is the same as the media store's, so media imported through the web lands on disk as in production.
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
	srv, err = New("127.0.0.1:0", Deps{
		DB:            db,
		Logger:        discardLogger(),
		SchemaVersion: func(ctx context.Context) (int, error) { return store.CurrentVersion(ctx, db) },
		Accounts:      accounts,
		Sessions:      mgr,
		Users:         users,
		// The session middleware runs before the language middleware, so the language in user settings takes effect.
		UserLocale: SessionUserLocale,
		Decks:      store.NewDeckStore(db),
		Notes:      store.NewNoteStore(db),
		Cards:      store.NewCardStore(db),
		Presets:    store.NewPresetStore(db),
		Auditor:    auditor,
		Media:      mediaStore,
		API:        apiSrv,
		LoginLimiter: auth.NewLoginLimiter(auth.LimiterConfig{
			Sleep: func(context.Context, time.Duration) error { return nil },
		}),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	owner, err := accounts.CreateLocalUser(context.Background(), auth.CreateUserInput{
		Username: "owner", Email: "owner@example.com", Password: "Sup3rSecret!", Role: store.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("CreateLocalUser() error = %v", err)
	}
	// Sign-in goes through the SPA's same-origin JSON endpoint: failure is Fatal, success returns the session cookie and the session-bound CSRF token.
	cookies, csrf = loginJSON(t, srv, db, "owner", "Sup3rSecret!")
	return srv, db, owner.ID, cookies, csrf
}

// seedDeck creates a deck owned by owner (with a scheduling preset, to satisfy the foreign-key column).
func seedDeck(t *testing.T, db *gorm.DB, ownerID uint64, name string) *store.Deck {
	t.Helper()
	now := time.Now().UTC()
	preset := store.Preset{OwnerUserID: ownerID, Name: "preset", LearningSteps: "1m,10m",
		RelearningSteps: "10m", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&preset).Error; err != nil {
		t.Fatalf("create preset: %v", err)
	}
	deck := store.Deck{OwnerUserID: ownerID, Name: name,
		PresetID: preset.ID, CreatedAt: now}
	if err := store.NewDeckStore(db).Create(context.Background(), &deck); err != nil {
		t.Fatalf("create deck: %v", err)
	}
	return &deck
}

// seedBasic creates a basic note; tags are written to tags_json when non-empty.
func seedBasic(t *testing.T, db *gorm.DB, deckID uint64, front, back string, tags ...string) *store.Note {
	t.Helper()
	note := &store.Note{DeckID: deckID, Kind: "basic"}
	if len(tags) > 0 {
		raw, _ := jsonMarshalStrings(tags)
		note.TagsJSON = raw
	}
	if _, err := store.NewNoteStore(db).Create(context.Background(), note,
		map[string]any{"front": front, "back": back}); err != nil {
		t.Fatalf("create note %q: %v", front, err)
	}
	return note
}

// jsonMarshalStrings is the minimal JSON array encoder for tests, so the tests do not pull in another set of encoding logic.
func jsonMarshalStrings(xs []string) (string, error) {
	quoted := make([]string, len(xs))
	for i, x := range xs {
		quoted[i] = `"` + strings.ReplaceAll(x, `"`, `\"`) + `"`
	}
	return "[" + strings.Join(quoted, ",") + "]", nil
}

// TestNotePagesRejectAnonymousAndForeign covers the pages' auth gates: anonymous redirect, non-owner 403.
// The card list requires reader and the edit page requires editor, both matching the pre-migration SSR pages item by item.
func TestNotePagesRejectAnonymousAndForeign(t *testing.T) {
	srv, db, ownerID, _, _ := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Private deck")
	note := seedBasic(t, db, deck.ID, "Q001", "A001")

	if code := get(t, srv, "/decks/"+deck.PublicID+"/notes", nil).Code; code != http.StatusSeeOther {
		t.Errorf("anonymous GET list status = %d, want 303 redirect to login", code)
	}

	// Another user (not the owner) accessing the same deck must be refused.
	users := store.NewUserStore(db)
	sessions := store.NewSessionStore(db)
	accounts, err := auth.NewAccountService(users, sessions, store.NewAPIKeyStore(db), auth.NewPasswordHasher(auth.Params{
		Memory: 8 * 1024, Time: 1, Threads: 1, SaltLength: 16, KeyLength: 32,
	}))
	if err != nil {
		t.Fatalf("NewAccountService() error = %v", err)
	}
	if _, err := accounts.CreateLocalUser(context.Background(), auth.CreateUserInput{
		Username: "stranger", Email: "stranger@example.com", Password: "Sup3rSecret!", Role: store.RoleUser,
	}); err != nil {
		t.Fatalf("create stranger: %v", err)
	}
	strangerCookies, _ := loginJSON(t, srv, db, "stranger", "Sup3rSecret!")
	foreign := getWithCookies(t, srv, "/decks/"+deck.PublicID+"/notes", strangerCookies)
	if foreign.Code != http.StatusForbidden {
		t.Errorf("non-owner GET list status = %d, want 403", foreign.Code)
	}
	if rec := getWithCookies(t, srv, "/decks/"+deck.PublicID+"/notes/"+note.PublicID, strangerCookies); rec.Code != http.StatusForbidden {
		t.Errorf("non-owner GET editor status = %d, want 403", rec.Code)
	}
}

func u64str(v uint64) string { return itoa(int(v)) }

// userPublicID returns the public id for a user primary key: in tests, it converts the numeric id into the public id used in routes/request bodies.
func userPublicID(t *testing.T, db *gorm.DB, id uint64) string {
	t.Helper()
	u, err := store.NewUserStore(db).ByID(context.Background(), id)
	if err != nil {
		t.Fatalf("load user %d: %v", id, err)
	}
	return u.PublicID
}

// pad3 is a test helper that pads a sequence number to three digits (used for generating front/back copy).
func pad3(i int) string {
	s := "000" + itoa(i)
	return s[len(s)-3:]
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
