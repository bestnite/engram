package store

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"git.nite07.com/nite/engram/internal/apkg"
	"git.nite07.com/nite/engram/internal/notetype"
)

// TestExportAnkiPackageRefusesUnreadableTemplateMedia pins the export rule: a deck editor who cannot
// read a medium must not receive its bytes by exporting a note type template that references it.
// The owner, who supplied the bytes, still exports the same file.
func TestExportAnkiPackageRefusesUnreadableTemplateMedia(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			users := seedUsers(t, db, "export_media_owner", "export_media_editor")
			owner, editor := users[0], users[1]
			deckID := seedDeck(t, db, owner)
			if err := NewGrantStore(db).Grant(ctx, deckID, editor, RoleEditor, Ptr(owner)); err != nil {
				t.Fatalf("grant editor: %v", err)
			}

			root := t.TempDir()
			raw := mediaBytes("template-only")
			sha := saveMedia(t, db, root, owner, raw)
			const name = "cover.png"
			if err := NewMediaNameStore(db).Upsert(ctx, deckID, name, sha); err != nil {
				t.Fatalf("register media name: %v", err)
			}

			// The template is the only reference to the medium: there is no note, so no media_notes
			// row grants the editor read access the way a note field would.
			stock, ok := notetype.EngramStockByKind("basic")
			if !ok {
				t.Fatal("basic stock type is missing")
			}
			cfg := stock.Config
			cfg.Templates[0].QFormat = `<img src="cover.png">` + cfg.Templates[0].QFormat
			nt := &NoteType{DeckID: deckID, Name: "Templated"}
			if err := NewNoteTypeStore(db).Create(ctx, nt, cfg); err != nil {
				t.Fatalf("create templated note type: %v", err)
			}

			if ok, err := MediaAccessibleToUser(ctx, db, editor, sha); err != nil || ok {
				t.Fatalf("editor readable=%v err=%v, want false", ok, err)
			}
			if ok, err := MediaAccessibleToUser(ctx, db, owner, sha); err != nil || !ok {
				t.Fatalf("owner readable=%v err=%v, want true", ok, err)
			}

			opts := PackageOptions{IncludeMedia: true, MediaRoot: root, IncludeDeckConfigs: Ptr(false)}
			if _, err := NewDeckStore(db).ExportDeckPackage(ctx, editor, deckID, opts); err == nil {
				t.Fatal("editor export succeeded, want the unreadable template media refused")
			} else {
				var pe *PackageError
				if !errors.As(err, &pe) || pe.Code != CodePackageMediaForbidden {
					t.Fatalf("editor export error = %v, want *PackageError with code %q", err, CodePackageMediaForbidden)
				}
			}

			data, err := NewDeckStore(db).ExportDeckPackage(ctx, owner, deckID, opts)
			if err != nil {
				t.Fatalf("owner export: %v", err)
			}
			pkg, err := apkg.ReadWithLimits(bytes.NewReader(data), int64(len(data)), apkg.DefaultLimits())
			if err != nil {
				t.Fatalf("read exported package: %v", err)
			}
			var got []byte
			for _, m := range pkg.Media {
				if m.Name != name {
					continue
				}
				f, err := m.Open()
				if err != nil {
					t.Fatalf("open exported media %q: %v", name, err)
				}
				got, err = io.ReadAll(f)
				f.Close()
				if err != nil {
					t.Fatalf("read exported media %q: %v", name, err)
				}
			}
			if !bytes.Equal(got, raw) {
				t.Fatalf("exported media %q = %d bytes, want the owner's %d bytes", name, len(got), len(raw))
			}
		})
	}
}
