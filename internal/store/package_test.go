package store

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gorm.io/gorm"
)

// ---- test fixtures ----

// packageDatabases returns fresh test databases on this machine with the tables created: a temporary SQLite database + a gated PostgreSQL one.
// The deck package cases run the same assertions on both drivers -- the PG side gets an independent schema per run via internal/pgtest,
// equivalent in isolation to the temporary SQLite database (the PG branch has to really run, not just pass on SQLite).
func packageDatabases(t *testing.T) map[string]*gorm.DB {
	t.Helper()
	out := testDatabases(t)
	for driver, db := range out {
		if err := db.AutoMigrate(AllModels()...); err != nil {
			t.Fatalf("AutoMigrate %s: %v", driver, err)
		}
	}
	return out
}

// seedPackageDeck builds a source deck holding notes of several kinds and writes one progress row belonging to owner.
func seedPackageDeck(t *testing.T, db *gorm.DB, owner uint64) (deckID uint64, noteIDs []uint64) {
	t.Helper()
	ctx := context.Background()
	presetID := seedPresetRow(t, db, owner)
	d := Deck{OwnerUserID: owner, Name: "源卡组", PresetID: presetID, Description: "source"}
	if err := NewDeckStore(db).Create(ctx, &d); err != nil {
		t.Fatalf("create deck: %v", err)
	}
	ns := NewNoteStore(db)
	type seed struct {
		kind   string
		fields map[string]any
		ref    string
		tags   []string
	}
	seeds := []seed{
		{"basic", map[string]any{"front": "capital?", "back": `\(E=mc^2\)`}, "ext:1", []string{"geo"}},
		{"cloze", map[string]any{"text": "The {{c1::mitochondrion}} is the powerhouse."}, "", []string{"bio"}},
		{"choice_single", map[string]any{"question": "2+2?", "options": []any{"3", "4"}, "answer": float64(1)}, "", nil},
		{"typed", map[string]any{"prompt": "spell", "answer": "ok"}, "", []string{"lang"}},
	}
	for _, s := range seeds {
		n := Note{DeckID: d.ID, NoteTypeID: Ptr(stockNoteTypeID(t, db, d.ID, s.kind)), TagsJSON: TagsJSON(s.tags), CreatedBy: Ptr(owner), Source: Ptr("manual")}
		if s.ref != "" {
			n.ExternalRef = Ptr(s.ref)
		}
		cards, err := ns.Create(ctx, &n, s.fields)
		if err != nil {
			t.Fatalf("create note %s: %v", s.kind, err)
		}
		noteIDs = append(noteIDs, n.ID)
		if s.kind == "basic" {
			if err := db.Create(&CardState{CardID: cards[0].ID, UserID: owner, State: "review", StepIndex: 0, Reps: 3, Version: 2}).Error; err != nil {
				t.Fatalf("seed card state: %v", err)
			}
		}
	}
	return d.ID, noteIDs
}

// seedPresetDeck builds an empty deck (no notes) for the media cases to use.
func seedPresetDeck(t *testing.T, db *gorm.DB, owner uint64) uint64 {
	t.Helper()
	presetID := seedPresetRow(t, db, owner)
	d := Deck{OwnerUserID: owner, Name: "empty", PresetID: presetID}
	if err := NewDeckStore(db).Create(context.Background(), &d); err != nil {
		t.Fatalf("create deck: %v", err)
	}
	return d.ID
}

// exportZip exports and serializes the result into zip bytes.
func exportZip(t *testing.T, db *gorm.DB, owner, deckID uint64, opts PackageOptions) []byte {
	t.Helper()
	pkg, err := NewDeckStore(db).ExportPackage(context.Background(), owner, deckID, opts)
	if err != nil {
		t.Fatalf("ExportPackage: %v", err)
	}
	var buf bytes.Buffer
	if err := pkg.WriteZip(&buf); err != nil {
		t.Fatalf("WriteZip: %v", err)
	}
	return buf.Bytes()
}

// ---- export ----

// TestExportPackageValidatesAgainstSchema asserts the exported package validates against schema/deck-package.schema.json (SQLite + PG).
func TestExportPackageValidatesAgainstSchema(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			owner := seedUsers(t, db, "pkg_schema_owner")[0]
			deckID, _ := seedPackageDeck(t, db, owner)

			pkg, err := NewDeckStore(db).ExportPackage(context.Background(), owner, deckID, PackageOptions{IncludeProgress: true, IncludeMedia: true})
			if err != nil {
				t.Fatalf("ExportPackage: %v", err)
			}
			doc := pkg.Document()
			// A JSON round trip unifies the numeric types, then the real library (draft 2020-12) validates it against the repo schema.
			encoded, _ := json.Marshal(doc)
			var instance any
			if err := json.Unmarshal(encoded, &instance); err != nil {
				t.Fatalf("re-decode document: %v", err)
			}
			if err := validateAgainstSchema(t, instance, filepath.Join("..", "..", "schema", "deck-package.schema.json")); err != nil {
				t.Fatalf("exported package does not validate against deck-package.schema.json: %v", err)
			}
		})
	}
}

// TestExportProgressIsolatedPerUser asserts the package never contains anyone's progress but the exporter's
func TestExportProgressIsolatedPerUser(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			users := seedUsers(t, db, "pkg_iso_owner", "pkg_iso_reader")
			owner, reader := users[0], users[1]
			deckID, noteIDs := seedPackageDeck(t, db, owner)

			// reader also has its own progress on the first card of the same deck.
			cards, err := NewCardStore(db).ByNote(context.Background(), noteIDs[0])
			if err != nil || len(cards) == 0 {
				t.Fatalf("load cards: %v", err)
			}
			if err := db.Create(&CardState{CardID: cards[0].ID, UserID: reader, State: "learning", Reps: 99, Version: 7}).Error; err != nil {
				t.Fatalf("seed reader state: %v", err)
			}

			pkg, err := NewDeckStore(db).ExportPackage(context.Background(), owner, deckID, PackageOptions{IncludeProgress: true, IncludeMedia: false})
			if err != nil {
				t.Fatalf("ExportPackage: %v", err)
			}
			if pkg.Progress == nil || len(pkg.Progress.CardStates) != 1 {
				t.Fatalf("progress card_states = %v, want exactly the owner's one row", pkg.Progress)
			}
			for _, st := range pkg.Progress.CardStates {
				if st.Reps == 99 || st.State == "learning" {
					t.Fatalf("package leaked another user's progress: %+v", st)
				}
			}
			if pkg.Manifest.ExportedBy != "pkg_iso_owner" {
				t.Fatalf("manifest.exported_by = %q, want owner username", pkg.Manifest.ExportedBy)
			}
		})
	}
}

// TestExportMediaOffDeclaresHonestly asserts include_media=0 yields no media entries and the manifest declares it honestly
func TestExportMediaOffDeclaresHonestly(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			owner := seedUsers(t, db, "pkg_media_owner")[0]
			deckID := seedPresetDeck(t, db, owner)
			// One note referencing media, so the "media off" path is really exercised.
			n := Note{DeckID: deckID, NoteTypeID: Ptr(stockNoteTypeID(t, db, deckID, "basic")), TagsJSON: "[]", CreatedBy: Ptr(owner)}
			if _, err := NewNoteStore(db).Create(context.Background(), &n, map[string]any{
				"front": "img?", "back": `<img src="0000000000000000000000000000000000000000000000000000000000000000.png">`,
			}); err != nil {
				t.Fatalf("create note: %v", err)
			}
			pkg, err := NewDeckStore(db).ExportPackage(context.Background(), owner, deckID, PackageOptions{IncludeMedia: false})
			if err != nil {
				t.Fatalf("ExportPackage: %v", err)
			}
			if pkg.Manifest.IncludeMedia {
				t.Fatal("manifest.include_media = true, want false")
			}
			if len(pkg.Media) != 0 || len(pkg.MediaBytes) != 0 {
				t.Fatalf("media not empty with include_media=0: %v", pkg.Media)
			}
			if pkg.Manifest.Counts.Media != 0 {
				t.Fatalf("manifest.counts.media = %d, want 0", pkg.Manifest.Counts.Media)
			}
			if _, ok := pkg.Document()["media.json"]; ok {
				t.Fatal("media.json present with include_media=0")
			}
		})
	}
}

// ---- zip building and schema validation helpers ----

// buildZip packs a set of entries into a zip; entry names are kept as given, so the security-rejection cases can build malicious archives.
func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// buildSymlinkZip builds a zip containing a symlink entry, for the security-rejection cases.
func buildSymlinkZip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	h := &zip.FileHeader{Name: "link"}
	h.SetMode(os.ModeSymlink | 0o777)
	w, err := zw.CreateHeader(h)
	if err != nil {
		t.Fatalf("zip symlink header: %v", err)
	}
	if _, err := w.Write([]byte("../target")); err != nil {
		t.Fatalf("zip symlink write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// validateAgainstSchema validates a document with the real library (santhosh-tekuri/jsonschema v6, draft 2020-12)
// against the schema file in the repo; it replaced a hand-written subset validator, so keyword coverage matches the spec.
func validateAgainstSchema(t *testing.T, instance any, schemaPath string) error {
	t.Helper()
	raw, err := os.ReadFile(schemaPath)
	if err != nil {
		return fmt.Errorf("read schema: %w", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("decode schema: %w", err)
	}
	c := jsonschema.NewCompiler()
	// The schema's $id is used as the resource URL so $ref can resolve to definitions inside the file.
	if err := c.AddResource("https://example.com/schema/deck-package.schema.json", doc); err != nil {
		return fmt.Errorf("add schema resource: %w", err)
	}
	sch, err := c.Compile("https://example.com/schema/deck-package.schema.json")
	if err != nil {
		return fmt.Errorf("compile schema: %w", err)
	}
	return sch.Validate(instance)
}
