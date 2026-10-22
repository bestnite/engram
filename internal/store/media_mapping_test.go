package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"gorm.io/gorm"
)

// This file covers L2's media mapping and pre-write validation:
//   - media_notes is **rebuilt** by the note write path (removing a reference revokes access, not append);
//   - pre-write validation rejects 'references newly introduced this time that the writer cannot read', evaluated against the state before the write;
//   - media_uploaders registers every provider even on a dedup hit;
//   - after cloning, images in the copy are readable by the cloner without relying on the source deck staying visible;
//   - package import does not persist notes that 'reference unreadable media'.
//
// All note fixtures are built through NoteStore's write method (AGENTS / task requirement), so the mapping necessarily matches the fields.

// mediaBytes is a chunk of test bytes with the PNG magic number; the content is distinguished by payload.
func mediaBytes(payload string) []byte {
	return append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, []byte(payload)...)
}

// saveMedia stores one media file and returns its sha256 (registering uploader attribution).
func saveMedia(t *testing.T, db *gorm.DB, root string, owner uint64, raw []byte) string {
	t.Helper()
	m, err := NewMediaStore(db).SaveBytes(context.Background(), root, "image/png", raw, Ptr(owner))
	if err != nil {
		t.Fatalf("SaveBytes: %v", err)
	}
	return m.Sha256
}

// saveRefNote creates, through the write method, a basic note referencing the given sha; the author is author (may be nil).
// actor is 0 (unattributed), so pre-write validation is not triggered here — the fixture must line up with 'the mapping is built by the write',
// not construct a privilege-escalation scenario.
func saveRefNote(t *testing.T, db *gorm.DB, deckID uint64, author *uint64, front string) uint64 {
	t.Helper()
	n := Note{DeckID: deckID, Kind: "basic", CreatedBy: author}
	fields := map[string]any{"front": front, "back": "x"}
	if _, err := NewNoteStore(db).Save(context.Background(), &n, fields); err != nil {
		t.Fatalf("save referencing note: %v", err)
	}
	return n.ID
}

func refFront(shas ...string) string {
	parts := make([]string, 0, len(shas))
	for _, sha := range shas {
		parts = append(parts, "![](/media/"+sha+")")
	}
	return strings.Join(parts, " ")
}

// mappingShas computes, from the mapping, the set of shas referenced in decks readable by that user; same source as branch two of read authorization.
func mappingShas(t *testing.T, db *gorm.DB, userID uint64) map[string]bool {
	t.Helper()
	var rows []MediaNote
	if err := db.Model(&MediaNote{}).
		Joins("JOIN notes ON notes.id = media_notes.note_id AND notes.deleted_at IS NULL").
		Where("notes.deck_id IN (?)", visibleDeckIDsQuery(db, userID)).
		Find(&rows).Error; err != nil {
		t.Fatalf("load media_notes: %v", err)
	}
	out := map[string]bool{}
	for _, r := range rows {
		out[r.MediaSha] = true
	}
	return out
}

// fieldsShas scans the fields of non-soft-deleted notes in decks readable by that user to get the sha set (the old rule, used as a control).
func fieldsShas(t *testing.T, db *gorm.DB, userID uint64) map[string]bool {
	t.Helper()
	var notes []Note
	if err := db.Where("deck_id IN (?)", visibleDeckIDsQuery(db, userID)).Find(&notes).Error; err != nil {
		t.Fatalf("list visible notes: %v", err)
	}
	out := map[string]bool{}
	for i := range notes {
		fields, err := ParseFields(notes[i].FieldsJSON)
		if err != nil {
			continue
		}
		scanMediaRefs(fields, out)
	}
	return out
}

// TestMediaMappingInvariantMatchesFieldsScan is the core invariant: under random fixtures,
// 'the readable set computed from the mapping table' must equal 'the readable set computed by scanning fields_json'.
//
// The two paths check each other: as soon as the mapping drifts from the fields (a missed write, or an old row left after removing a reference) they must differ.
// Fixtures cover updates (removing references), soft deletes, and a deck invisible to another user, so both directions can be caught.
func TestMediaMappingInvariantMatchesFieldsScan(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "invariant_owner", "invariant_other")
			owner, other := users[0], users[1]
			deckOwner := seedDeck(t, db, owner)
			deckOther := seedDeck(t, db, other)

			// random sha pool; references may be shas that are not in the DB (the mapping records them anyway, since they are in the fields).
			rng := rand.New(rand.NewSource(20261006))
			pool := make([]string, 8)
			for i := range pool {
				pool[i] = fmt.Sprintf("%064x", rng.Int63())
			}
			pick := func() string { return pool[rng.Intn(len(pool))] }

			deckIDs := map[uint64]uint64{deckOwner: owner, deckOther: other}
			noteIDs := []uint64{}
			deckOf := map[uint64]uint64{}
			for i := 0; i < 60; i++ {
				deckID := deckOwner
				if rng.Intn(4) == 0 {
					deckID = deckOther
				}
				id := saveRefNote(t, db, deckID, Ptr(deckIDs[deckID]), refFront(pick(), pick(), pick()))
				noteIDs = append(noteIDs, id)
				deckOf[id] = deckID
			}
			// random update: half drop all references, half replace them with new ones.
			//
			// the key construct: 'removing a reference' first makes the note reference a sha **unique to this case** (a sha that appears in no
			// other note), then drops it. An append-only implementation would leave the old mapping row for that unique sha in the table, so the mapping set
			// gains one entry the field set does not have — the invariant necessarily flags it (an ordinary overlapping sha pool would be masked by set dedup).
			ns := NewNoteStore(db)
			unique := 0
			uniqueSha := func() string {
				unique++
				return fmt.Sprintf("%064x", uint64(0x9000000000000000)+uint64(unique))
			}
			for _, id := range noteIDs {
				switch rng.Intn(3) {
				case 0:
					nA := Note{ID: id, Kind: "basic"}
					if _, err := ns.Save(ctx, &nA, map[string]any{"front": refFront(uniqueSha()), "back": "x"}); err != nil {
						t.Fatalf("update note %d: %v", id, err)
					}
					nB := Note{ID: id, Kind: "basic"}
					if _, err := ns.Save(ctx, &nB, map[string]any{"front": "no refs", "back": "x"}); err != nil {
						t.Fatalf("update note %d: %v", id, err)
					}
				case 1:
					n := Note{ID: id, Kind: "basic"}
					if _, err := ns.Save(ctx, &n, map[string]any{"front": refFront(pick()), "back": "x"}); err != nil {
						t.Fatalf("update note %d: %v", id, err)
					}
				}
			}
			// soft-delete a few: the mapping rows may stay in the table, but both sides must exclude them.
			for _, id := range noteIDs[:5] {
				if err := ns.Delete(ctx, id); err != nil {
					t.Fatalf("delete note %d: %v", id, err)
				}
			}

			fromMapping := mappingShas(t, db, owner)
			fromFields := fieldsShas(t, db, owner)
			if len(fromFields) == 0 {
				t.Fatal("fixture produced no references; the invariant is vacuous")
			}
			if len(fromMapping) != len(fromFields) {
				t.Fatalf("mapping set size %d != fields set size %d (mapping=%v fields=%v)",
					len(fromMapping), len(fromFields), keys(fromMapping), keys(fromFields))
			}
			for sha := range fromFields {
				if !fromMapping[sha] {
					t.Errorf("sha %s referenced by a visible note but missing from media_notes", sha)
				}
			}
			for sha := range fromMapping {
				if !fromFields[sha] {
					t.Errorf("sha %s present in media_notes but not referenced by any visible note (stale row)", sha)
				}
			}
		})
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestMediaMappingRebuiltOnReferenceRemoval proves the mapping is rebuilt, not appended: after removing a reference and saving,
// the old mapping row must be gone and the reader loses access at once (the old append implementation left the old row → the reader could still read).
func TestMediaMappingRebuiltOnReferenceRemoval(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "rebuild_owner", "rebuild_reader")
			owner, reader := users[0], users[1]
			deckID := seedDeck(t, db, owner)
			if err := NewGrantStore(db).Grant(ctx, deckID, reader, RoleReader, Ptr(owner)); err != nil {
				t.Fatalf("grant reader: %v", err)
			}
			sha := saveMedia(t, db, t.TempDir(), owner, mediaBytes("rebuild"))
			id := saveRefNote(t, db, deckID, Ptr(owner), refFront(sha))

			if ok, err := MediaAccessibleToUser(ctx, db, reader, "", sha); err != nil || !ok {
				t.Fatalf("reader before removal: readable=%v err=%v, want true", ok, err)
			}

			n := Note{ID: id, Kind: "basic"}
			if _, err := NewNoteStore(db).Save(ctx, &n, map[string]any{"front": "no refs", "back": "x"}); err != nil {
				t.Fatalf("update note: %v", err)
			}

			var rows int64
			if err := db.Model(&MediaNote{}).Where("note_id = ?", id).Count(&rows).Error; err != nil {
				t.Fatalf("count media_notes: %v", err)
			}
			if rows != 0 {
				t.Errorf("stale media_notes rows after reference removal = %d, want 0 (rebuild must delete old rows)", rows)
			}
			if ok, err := MediaAccessibleToUser(ctx, db, reader, "", sha); err != nil || ok {
				t.Fatalf("reader after removal: readable=%v err=%v, want false", ok, err)
			}
		})
	}
}

// TestMediaMappingSoftDeletedNoteDoesNotAuthorize covers soft delete: after a note is soft-deleted the mapping row may remain,
// but read authorization must stop allowing it because of notes.deleted_at.
func TestMediaMappingSoftDeletedNoteDoesNotAuthorize(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "softdel_owner", "softdel_reader")
			owner, reader := users[0], users[1]
			deckID := seedDeck(t, db, owner)
			if err := NewGrantStore(db).Grant(ctx, deckID, reader, RoleReader, Ptr(owner)); err != nil {
				t.Fatalf("grant reader: %v", err)
			}
			sha := saveMedia(t, db, t.TempDir(), owner, mediaBytes("softdel"))
			id := saveRefNote(t, db, deckID, Ptr(owner), refFront(sha))

			if ok, _ := MediaAccessibleToUser(ctx, db, reader, "", sha); !ok {
				t.Fatal("reader should read the media before the note is soft deleted")
			}
			if err := NewNoteStore(db).Delete(ctx, id); err != nil {
				t.Fatalf("soft delete note: %v", err)
			}
			if ok, err := MediaAccessibleToUser(ctx, db, reader, "", sha); err != nil || ok {
				t.Fatalf("reader after note soft delete: readable=%v err=%v, want false", ok, err)
			}
		})
	}
}

// TestMediaUploadersDedupeRegistersEveryUploader covers dedup registration in the store layer's media write:
// the same bytes supplied in turn by two users give both attribution, one row each (idempotent).
func TestMediaUploadersDedupeRegistersEveryUploader(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "uploader_a", "uploader_b")
			a, b := users[0], users[1]
			root := t.TempDir()
			raw := mediaBytes("dedupe")

			shaA := saveMedia(t, db, root, a, raw)
			shaB := saveMedia(t, db, root, b, raw)
			if shaA != shaB {
				t.Fatalf("dedupe failed: %s != %s", shaA, shaB)
			}
			for _, uid := range []uint64{a, b} {
				var n int64
				if err := db.Model(&MediaUploader{}).
					Where("media_sha = ? AND user_id = ?", shaA, uid).Count(&n).Error; err != nil {
					t.Fatalf("count uploader: %v", err)
				}
				if n != 1 {
					t.Errorf("uploader %d rows = %d, want 1", uid, n)
				}
			}
			// registering again is idempotent: no new row, no error.
			if err := RecordMediaUploader(ctx, db, shaA, a); err != nil {
				t.Fatalf("idempotent RecordMediaUploader: %v", err)
			}
			var total int64
			if err := db.Model(&MediaUploader{}).Where("media_sha = ?", shaA).Count(&total).Error; err != nil {
				t.Fatalf("count uploaders: %v", err)
			}
			if total != 2 {
				t.Errorf("total uploaders = %d, want 2", total)
			}
		})
	}
}

// TestMediaWriteValidationRejectsUnreadableReference covers pre-write validation itself:
// with actor B, creating / updating a note that references media B cannot read must both be rejected (*MediaWriteError),
// and no note / mapping row may remain; evaluating against the state before the write keeps 'the mapping satisfies itself once created' from holding.
func TestMediaWriteValidationRejectsUnreadableReference(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "writeval_a", "writeval_b")
			a, b := users[0], users[1]
			deckA := seedDeck(t, db, a)
			deckB := seedDeck(t, db, b)
			sha := saveMedia(t, db, t.TempDir(), a, mediaBytes("writeval"))

			// create: B references A's media inside B's own deck → rejected.
			n := Note{DeckID: deckB, Kind: "basic", CreatedBy: Ptr(b)}
			_, err := NewNoteStore(db).Save(WithActor(ctx, b), &n, map[string]any{
				"front": refFront(sha), "back": "x",
			})
			var mwe *MediaWriteError
			if !errors.As(err, &mwe) {
				t.Fatalf("create with unreadable ref: err=%v, want *MediaWriteError", err)
			}
			if n.ID != 0 {
				t.Errorf("rejected create assigned note id %d, want 0", n.ID)
			}
			var created int64
			if err := db.Model(&Note{}).Where("deck_id = ?", deckB).Count(&created).Error; err != nil {
				t.Fatalf("count notes: %v", err)
			}
			if created != 0 {
				t.Errorf("rejected create left %d notes", created)
			}
			var mapping int64
			if err := db.Model(&MediaNote{}).Where("media_sha = ?", sha).Count(&mapping).Error; err != nil {
				t.Fatalf("count media_notes: %v", err)
			}
			if mapping != 0 {
				t.Errorf("rejected create left %d media_notes rows", mapping)
			}

			// update: first create an ordinary note for B (actor 0), then have B inject A's media reference → rejected, fields unchanged.
			id := saveRefNote(t, db, deckB, Ptr(b), "plain")
			n2 := Note{ID: id, Kind: "basic"}
			_, err = NewNoteStore(db).Save(WithActor(ctx, b), &n2, map[string]any{
				"front": refFront(sha), "back": "x",
			})
			if !errors.As(err, &mwe) {
				t.Fatalf("update with unreadable ref: err=%v, want *MediaWriteError", err)
			}
			reloaded, err := NewNoteStore(db).ByID(ctx, id)
			if err != nil {
				t.Fatalf("reload note: %v", err)
			}
			if strings.Contains(reloaded.FieldsJSON, sha) {
				t.Errorf("rejected update wrote the unreadable reference: %s", reloaded.FieldsJSON)
			}
			if n := func() int64 {
				var c int64
				_ = db.Model(&MediaNote{}).Where("note_id = ?", id).Count(&c).Error
				return c
			}(); n != 0 {
				t.Errorf("rejected update left %d media_notes rows", n)
			}
			_ = deckA
		})
	}
}

// TestCloneKeepsClonedMediaReadable covers cloning end to end: after B clones a visible deck, the images in the copy are readable by B,
// and no longer depend on the source deck staying visible to B (after revoking the source deck's grant B can still read the copy's images).
func TestCloneKeepsClonedMediaReadable(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "clone_owner", "clone_reader")
			owner, cloner := users[0], users[1]

			presetID := seedPresetRow(t, db, owner)
			src := Deck{OwnerUserID: owner, Name: "shared source", PresetID: presetID}
			if err := NewDeckStore(db).Create(ctx, &src); err != nil {
				t.Fatalf("create source deck: %v", err)
			}
			// the cloner can see the source deck only through an explicit grant (visibility is no longer a source of read access).
			if err := NewGrantStore(db).Grant(ctx, src.ID, cloner, RoleReader, Ptr(owner)); err != nil {
				t.Fatalf("grant source deck: %v", err)
			}
			sha := saveMedia(t, db, t.TempDir(), owner, mediaBytes("clone"))
			saveRefNote(t, db, src.ID, Ptr(owner), refFront(sha))

			clonerPreset := seedPresetRow(t, db, cloner)
			cloned, err := NewDeckStore(db).Clone(ctx, &src, cloner, "my copy", clonerPreset)
			if err != nil {
				t.Fatalf("Clone: %v", err)
			}
			if ok, err := MediaAccessibleToUser(ctx, db, cloner, "", sha); err != nil || !ok {
				t.Fatalf("cloner reads cloned media: readable=%v err=%v, want true", ok, err)
			}
			// revoke the source deck's grant and soft-delete the source notes: the copy no longer depends on the source deck being visible.
			if err := NewGrantStore(db).Revoke(ctx, src.ID, cloner); err != nil {
				t.Fatalf("revoke source grant: %v", err)
			}
			if _, err := NewNoteStore(db).DeleteMany(ctx, mustNoteIDs(t, db, src.ID), false); err != nil {
				t.Fatalf("soft delete source notes: %v", err)
			}
			if ok, err := MediaAccessibleToUser(ctx, db, cloner, "", sha); err != nil || !ok {
				t.Fatalf("cloner reads cloned media after source hidden: readable=%v err=%v, want true", ok, err)
			}
			// the copy really does have a note referencing that sha (the mapping belongs to the copy, not the source).
			fromMapping := mappingShas(t, db, cloner)
			if !fromMapping[sha] {
				t.Errorf("cloned media %s missing from the cloner's mapping set", sha)
			}
			_ = cloned
		})
	}
}

// mustNoteIDs lists a deck's note ids (soft-deleted included, for collection before a soft delete).
func mustNoteIDs(t *testing.T, db *gorm.DB, deckID uint64) []uint64 {
	t.Helper()
	var ids []uint64
	if err := db.Model(&Note{}).Where("deck_id = ?", deckID).Pluck("id", &ids).Error; err != nil {
		t.Fatalf("list note ids: %v", err)
	}
	return ids
}

// TestImportPackageRejectsUnreadableMediaRef covers privilege injection at the package import entry point:
// A's package references A's media without carrying the bytes, so when B (who cannot read that media) imports it the whole package is rejected (stable code),
// and after the transaction rolls back B owns no deck / note / mapping rows.
func TestImportPackageRejectsUnreadableMediaRef(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "pkgforbid_owner", "pkgforbid_importer")
			owner, importer := users[0], users[1]
			srcDeck := seedPresetDeck(t, db, owner)
			sha := saveMedia(t, db, t.TempDir(), owner, mediaBytes("pkgforbid"))
			// the source note references the media in in-package form; export with media disabled → the package carries no bytes.
			srcNote := Note{DeckID: srcDeck, Kind: "basic", CreatedBy: Ptr(owner)}
			if _, err := NewNoteStore(db).Save(ctx, &srcNote, map[string]any{
				"front": "q?", "back": "media/" + sha + ".png",
			}); err != nil {
				t.Fatalf("create source note: %v", err)
			}
			raw := exportZip(t, db, owner, srcDeck, PackageOptions{IncludeMedia: false})

			_, err := NewDeckStore(db).ImportPackage(ctx, importer, bytes.NewReader(raw), PackageImportOptions{
				SkipMissingMedia: true,
				MediaRoot:        t.TempDir(),
			})
			var pe *PackageError
			if !errors.As(err, &pe) || pe.Code != CodePackageMediaForbidden {
				t.Fatalf("import error = %v, want PackageError code %s", err, CodePackageMediaForbidden)
			}
			if !entryContains(pe.Entries, sha) {
				t.Errorf("rejection entries = %v, want one naming media/%s", pe.Entries, sha)
			}
			// the rollback is complete: the importer owns no deck, no note, no mapping.
			var deckCount, noteCount, mappingCount int64
			if err := db.Model(&Deck{}).Where("owner_user_id = ?", importer).Count(&deckCount).Error; err != nil {
				t.Fatalf("count importer decks: %v", err)
			}
			if err := db.Model(&Note{}).Where("created_by = ?", importer).Count(&noteCount).Error; err != nil {
				t.Fatalf("count importer notes: %v", err)
			}
			if err := db.Model(&MediaNote{}).
				Joins("JOIN notes ON notes.id = media_notes.note_id").
				Where("media_notes.media_sha = ? AND notes.created_by = ?", sha, importer).
				Count(&mappingCount).Error; err != nil {
				t.Fatalf("count media_notes: %v", err)
			}
			if deckCount != 0 || noteCount != 0 || mappingCount != 0 {
				t.Errorf("rejected import left decks=%d notes=%d mapping=%d, want 0/0/0",
					deckCount, noteCount, mappingCount)
			}
		})
	}
}
