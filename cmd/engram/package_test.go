package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
	"gorm.io/gorm"
)

// CLI acceptance: export writes a .edeck, import round-trips from that file and produces the same report as REST.

// seedCLIDeck creates an owner + deck + two basic notes (one card each).
func seedCLIDeck(t *testing.T, db *gorm.DB) (userID, deckID uint64) {
	t.Helper()
	ctx := context.Background()
	u := store.User{
		Username: "cli_user", Email: "cli_user@example.com", DisplayName: "cli",
		Role: store.RoleUser, Status: store.StatusActive, Locale: "en",
		Timezone: "UTC", DayCutoffHour: store.Ptr(4), CreatedAt: time.Now().UTC(),
	}
	if err := db.Create(&u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	preset := store.NewPreset(u.ID, "Default")
	if err := store.NewPresetStore(db).Create(ctx, &preset); err != nil {
		t.Fatalf("create preset: %v", err)
	}
	d := store.Deck{OwnerUserID: u.ID, Name: "cli deck", PresetID: preset.ID, CreatedAt: time.Now().UTC()}
	if err := store.NewDeckStore(db).Create(ctx, &d); err != nil {
		t.Fatalf("create deck: %v", err)
	}
	ns := store.NewNoteStore(db)
	noteType, err := ns.EnsureStockNoteType(ctx, d.ID, "basic")
	if err != nil {
		t.Fatalf("create note type: %v", err)
	}
	for i, f := range []map[string]any{
		{"front": "q1", "back": "a1"},
		{"front": "q2", "back": "a2"},
	} {
		n := store.Note{DeckID: d.ID, NoteTypeID: store.Ptr(noteType.ID), TagsJSON: "[]", CreatedBy: store.Ptr(u.ID)}
		if _, err := ns.Create(ctx, &n, f); err != nil {
			t.Fatalf("create note %d: %v", i, err)
		}
	}
	return u.ID, d.ID
}

// cfgEnv is the minimal environment the CLI subcommands need (matching config.Load's required entries).
func cfgEnv(t *testing.T, dbPath string) {
	t.Helper()
	t.Setenv("DB_DRIVER", "sqlite")
	t.Setenv("DB_DSN", dbPath)
	t.Setenv("SESSION_SECRET", testSessionSecret)
	t.Setenv("ENCRYPTION_KEY", testEncryptionKey)
	t.Setenv("MEDIA_DIR", filepath.Join(t.TempDir(), "media"))
}

// TestCLIPackageRoundTrip is the CLI acceptance test: a file written by export can be round-tripped by import.
func TestCLIPackageRoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cli.db")
	cfgEnv(t, dbPath)

	db, err := store.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	userID, deckID := seedCLIDeck(t, db)
	uid, did := strconv.FormatUint(userID, 10), strconv.FormatUint(deckID, 10)

	pkgPath := filepath.Join(t.TempDir(), "deck.edeck")
	if out := captureCLI(t, func() error {
		return runExport([]string{"--deck", did, "--package", pkgPath, "--user", uid})
	}); out == "" {
		t.Fatal("runExport produced no output")
	}
	if fi, err := os.Stat(pkgPath); err != nil || fi.Size() == 0 {
		t.Fatalf("exported package missing or empty: %v", err)
	}

	// dry-run rehearsal: the report fields match REST and no new deck is written.
	report := captureImportReport(t, []string{"--package", pkgPath, "--user", uid, "--dry-run", "--target", "new_deck"})
	if report["dry_run"] != true {
		t.Errorf("dry_run = %v, want true", report["dry_run"])
	}
	if report["notes_created"] != float64(2) {
		t.Errorf("notes_created = %v, want 2", report["notes_created"])
	}
	for _, key := range []string{"notes_updated", "notes_skipped", "cards_created", "errors"} {
		if _, ok := report[key]; !ok {
			t.Errorf("report is missing %q: %v", key, report)
		}
	}

	// Import for real into a new deck, then confirm the new deck's note count.
	real := captureImportReport(t, []string{"--package", pkgPath, "--user", uid, "--target", "new_deck"})
	if real["notes_created"] != float64(2) {
		t.Fatalf("real import notes_created = %v, want 2", real["notes_created"])
	}
	var count int64
	if err := db.Model(&store.Note{}).Where("deck_id <> ?", deckID).Count(&count).Error; err != nil {
		t.Fatalf("count imported notes: %v", err)
	}
	if count != 2 {
		t.Errorf("imported note count = %d, want 2", count)
	}
}

// captureCLI temporarily redirects the CLI diagnostic output into a buffer and returns the text.
func captureCLI(t *testing.T, fn func() error) string {
	t.Helper()
	var buf bytes.Buffer
	old := cliOut
	cliOut = &buf
	defer func() { cliOut = old }()
	if err := fn(); err != nil {
		t.Fatalf("CLI command error: %v (output %s)", err, buf.String())
	}
	t.Logf("CLI output: %s", buf.String())
	return buf.String()
}

// captureImportReport runs runImport and parses stdout back into a report object.
func captureImportReport(t *testing.T, args []string) map[string]any {
	t.Helper()
	out := captureCLI(t, func() error { return runImport(args) })
	var report map[string]any
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("parse import report: %v (stdout %s)", err, out)
	}
	return report
}

// cliRun runs one CLI command and returns its error and diagnostic output; used to assert the rejection paths (non-zero exit).
func cliRun(fn func() error) (error, string) {
	var buf bytes.Buffer
	old := cliOut
	cliOut = &buf
	defer func() { cliOut = old }()
	return fn(), buf.String()
}

// importFixture creates the database, seeds a source deck and an import target account, then exports a round-trippable .edeck.
func importFixture(t *testing.T) (db *gorm.DB, target store.User, pkgPath string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "cli.db")
	cfgEnv(t, dbPath)
	var err error
	db, err = store.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	userID, deckID := seedCLIDeck(t, db)
	target = store.User{
		Username: "importer", Email: "importer@example.com", DisplayName: "importer",
		Role: store.RoleUser, Status: store.StatusActive, Locale: "en",
		Timezone: "UTC", DayCutoffHour: store.Ptr(4), CreatedAt: time.Now().UTC(),
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("create target user: %v", err)
	}
	pkgPath = filepath.Join(t.TempDir(), "deck.edeck")
	captureCLI(t, func() error {
		return runExport([]string{"--deck", strconv.FormatUint(deckID, 10), "--package", pkgPath, "--user", strconv.FormatUint(userID, 10)})
	})
	return db, target, pkgPath
}

// countDecks counts all decks, used to assert that a rejection path really wrote nothing.
func countDecks(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&store.Deck{}).Count(&n).Error; err != nil {
		t.Fatalf("count decks: %v", err)
	}
	return n
}

// TestCLIImportRequiresExplicitUser is command-level acceptance:
// a missing --user exits non-zero and creates no deck, --user lands the content under the given account (both the id and the
// username form), and a --user naming a nonexistent person is rejected without writing anything.
func TestCLIImportRequiresExplicitUser(t *testing.T) {
	db, target, pkgPath := importFixture(t)

	// (1) Missing --user: non-zero exit, the error must explain how to pass it, and no deck may be created.
	before := countDecks(t, db)
	err, _ := cliRun(func() error {
		return run(context.Background(), []string{"import", "--package", pkgPath, "--target", "new_deck"})
	})
	if err == nil {
		t.Fatal("import without --user exited 0, want a non-zero exit")
	}
	if !strings.Contains(err.Error(), "--user") {
		t.Errorf("error does not say how to pass --user: %v", err)
	}
	if after := countDecks(t, db); after != before {
		t.Errorf("import without --user created a deck: %d -> %d", before, after)
	}

	// (3) --user naming a nonexistent user: rejected by name, and nothing is written.
	before = countDecks(t, db)
	err, _ = cliRun(func() error {
		return run(context.Background(), []string{"import", "--package", pkgPath, "--user", "987654", "--target", "new_deck"})
	})
	if err == nil {
		t.Fatal("import with an unknown --user exited 0, want a non-zero exit")
	}
	if !strings.Contains(err.Error(), "987654") {
		t.Errorf("error does not name the unknown user: %v", err)
	}
	if after := countDecks(t, db); after != before {
		t.Errorf("import with an unknown --user created a deck: %d -> %d", before, after)
	}

	// (2) With --user (username form): the content lands under that account.
	report := captureImportReport(t, []string{"--package", pkgPath, "--user", target.Username, "--target", "new_deck"})
	if report["notes_created"] != float64(2) {
		t.Fatalf("import by username notes_created = %v, want 2", report["notes_created"])
	}
	assertDeckOwner(t, db, target.ID, 2)

	// (2) With --user (id form): likewise lands under that account.
	if report := captureImportReport(t, []string{"--package", pkgPath, "--user", strconv.FormatUint(target.ID, 10), "--target", "new_deck"}); report["notes_created"] != float64(2) {
		t.Fatalf("import by id notes_created = %v, want 2", report["notes_created"])
	}
	assertDeckOwner(t, db, target.ID, 2)
}

// TestCLIImportRejectsAmbiguousUserRef: when a numeric value hits both an id and a username that point to different people,
// the ambiguity must be called out and rejected, rather than picking one account to write into.
func TestCLIImportRejectsAmbiguousUserRef(t *testing.T) {
	db, _, pkgPath := importFixture(t)

	// First take an existing user id, then create an account whose username is exactly that number, manufacturing an ambiguous value.
	var existing store.User
	if err := db.Order("id ASC").First(&existing).Error; err != nil {
		t.Fatalf("load first user: %v", err)
	}
	clash := store.User{
		Username: strconv.FormatUint(existing.ID, 10), Email: "clash@example.com",
		Role: store.RoleUser, Status: store.StatusActive, Locale: "en",
		Timezone: "UTC", DayCutoffHour: store.Ptr(4), CreatedAt: time.Now().UTC(),
	}
	if err := db.Create(&clash).Error; err != nil {
		t.Fatalf("create clashing user: %v", err)
	}
	if clash.ID == existing.ID {
		t.Fatal("clashing user unexpectedly shares the id")
	}

	before := countDecks(t, db)
	err, _ := cliRun(func() error {
		return run(context.Background(), []string{"import", "--package", pkgPath, "--user", strconv.FormatUint(existing.ID, 10), "--target", "new_deck"})
	})
	if err == nil {
		t.Fatal("import with an ambiguous --user exited 0, want a non-zero exit")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("error does not flag the ambiguity: %v", err)
	}
	if after := countDecks(t, db); after != before {
		t.Errorf("ambiguous --user still created a deck: %d -> %d", before, after)
	}
}

// assertDeckOwner asserts that the given account owns a deck containing wantNotes notes.
func assertDeckOwner(t *testing.T, db *gorm.DB, ownerID uint64, wantNotes int64) {
	t.Helper()
	var d store.Deck
	if err := db.Where("owner_user_id = ?", ownerID).Order("id DESC").First(&d).Error; err != nil {
		t.Fatalf("no deck owned by user %d: %v", ownerID, err)
	}
	var n int64
	if err := db.Model(&store.Note{}).Where("deck_id = ?", d.ID).Count(&n).Error; err != nil {
		t.Fatalf("count notes under deck %d: %v", d.ID, err)
	}
	if n != wantNotes {
		t.Errorf("deck %d (owner %d) has %d notes, want %d", d.ID, ownerID, n, wantNotes)
	}
}
