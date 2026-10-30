package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"gorm.io/gorm"
)

// TestBundledDecksImport asserts every package shipped in decks/ is a format-2 package this release can
// import: the library is the first thing a new instance pulls in, so a deck that no longer imports breaks
// the documented getting-started path.
func TestBundledDecksImport(t *testing.T) {
	decksRoot := filepath.Join("..", "..", "decks")
	var files []string
	if err := filepath.WalkDir(decksRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Ext(path) == ".edeck" {
			files = append(files, path)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk %s: %v", decksRoot, err)
	}
	if len(files) == 0 {
		t.Fatalf("no .edeck files under %s", decksRoot)
	}

	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			for _, path := range files {
				t.Run(filepath.Base(path), func(t *testing.T) {
					f, err := os.Open(path)
					if err != nil {
						t.Fatalf("open %s: %v", path, err)
					}
					defer f.Close()
					report, err := NewDeckStore(db).ImportPackage(context.Background(), 1, f, PackageImportOptions{
						Target:    PackageTargetNewDeck,
						MediaRoot: t.TempDir(),
					})
					if err != nil {
						t.Fatalf("import %s: %v", path, err)
					}
					if report.NotesCreated == 0 || report.DeckID == 0 {
						t.Fatalf("import %s created no content: %+v", path, report)
					}
					if len(report.Errors) != 0 {
						t.Fatalf("import %s reported entry errors: %+v", path, report.Errors)
					}
					assertAllMediaNamesResolve(t, db, report.DeckID)
				})
			}
		})
	}
}

// assertAllMediaNamesResolve checks that every media name the imported deck records points at a media row.
func assertAllMediaNamesResolve(t *testing.T, db *gorm.DB, deckID uint64) {
	t.Helper()
	names, err := NewMediaNameStore(db).ListByDeck(context.Background(), deckID)
	if err != nil {
		t.Fatalf("list media names: %v", err)
	}
	for _, name := range names {
		row, err := NewMediaStore(db).BySha256(context.Background(), name.Sha256)
		if err != nil {
			t.Fatalf("load media %s: %v", name.Sha256, err)
		}
		if row == nil {
			t.Fatalf("media name %q points at missing media %s", name.Name, name.Sha256)
		}
	}
}
