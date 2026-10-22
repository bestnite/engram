package web

import (
	"context"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/jobs"
	"git.nite07.com/nite/engram/internal/store"
)

// newPresetsServer wires a test server with a job runner and signs in an owner.
// runner does not call Start: jobs stay queued in the in-memory queue, so the test can deterministically simulate
// "the adapter finished and wrote the weights back" without really running an external optimizer (fake commands may be injected / state driven directly).
func newPresetsServer(t *testing.T) (srv *Server, db *gorm.DB, ownerID uint64, cookies []*http.Cookie, csrf string, runner *jobs.Runner) {
	t.Helper()
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "presets.db"))
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
	runner, err = jobs.New(jobs.Deps{DB: db, Logger: discardLogger()})
	if err != nil {
		t.Fatalf("jobs.New() error = %v", err)
	}
	srv, err = New("127.0.0.1:0", Deps{
		DB:            db,
		Logger:        discardLogger(),
		SchemaVersion: func(ctx context.Context) (int, error) { return store.CurrentVersion(ctx, db) },
		Accounts:      accounts,
		Sessions:      mgr,
		Users:         users,
		Decks:         store.NewDeckStore(db),
		Presets:       store.NewPresetStore(db),
		Auditor:       auditor,
		Jobs:          runner,
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
	return srv, db, owner.ID, cookies, csrf, runner
}

// seedPreset creates a **default** preset owned by owner, named with the stable literal store.DefaultPresetName,
// so store.EnsureDefaultPreset recognises it during page rendering and does not add another.
func seedPreset(t *testing.T, db *gorm.DB, ownerID uint64) *store.Preset {
	t.Helper()
	p := store.NewPreset(ownerID, store.DefaultPresetName)
	if err := store.NewPresetStore(db).Create(context.Background(), &p); err != nil {
		t.Fatalf("create preset: %v", err)
	}
	return &p
}

// seedReviews inserts n review log rows belonging to userID; the optimization gate counts by user_id only.
func seedReviews(t *testing.T, db *gorm.DB, userID, presetID uint64, n int) {
	t.Helper()
	now := time.Now().UTC()
	// The optimization gate counts only reviews on decks carrying that preset, so the reviews must land on cards in such a deck.
	deck := store.Deck{OwnerUserID: userID, Name: "optimize fuel", PresetID: presetID, CreatedAt: now}
	if err := db.Create(&deck).Error; err != nil {
		t.Fatalf("seed deck: %v", err)
	}
	cardID := seedCardRow(t, db, deck.ID, "fuel", now)
	for i := 0; i < n; i++ {
		row := store.Review{
			CardID:      cardID,
			UserID:      userID,
			Rating:      3,
			GradeSource: "self",
			ReviewedAt:  now,
			ReviewDay:   now.Format("2006-01-02"),
			StateBefore: 2,
		}
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("seed review: %v", err)
		}
	}
}

// setMinReviews overrides the optimization gate; the value put is a JSON-encoded string, decoded again on the optimize read side.
func setMinReviews(t *testing.T, db *gorm.DB, n int) {
	t.Helper()
	if err := store.PutSetting(context.Background(), db, store.SettingKeyOptimizeMinReviews,
		strconv.Itoa(n), nil, time.Now().UTC()); err != nil {
		t.Fatalf("PutSetting(%s): %v", store.SettingKeyOptimizeMinReviews, err)
	}
}

// TestPresetListRedirectsAnonymous covers anonymous access redirecting to the login page.
func TestPresetListRedirectsAnonymous(t *testing.T) {
	srv, _, _, _, _, _ := newPresetsServer(t)
	rec := getWithCookies(t, srv, "/presets", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("GET /presets (anonymous) status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Fatalf("GET /presets (anonymous) Location = %q, want /login", loc)
	}
}

// ---- Preset management: create, edit and the default preset ------------------------------------
