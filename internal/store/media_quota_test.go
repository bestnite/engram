package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

// TestUserMediaUsageDedupesAndReleases covers the accounting rule:
//   - the same blob referenced by several notes / in several places of one user counts once (dedup);
//   - different users referencing the same blob do not affect each other (each counts once, never twice for the same person);
//   - removing a live reference (soft-deleting the note) frees the referencer's quota; bytes whose only remaining
//     reference is a soft-deleted note stay charged to their uploader, because reclamation keeps them;
//   - deck-package form references media/<sha>.<ext> are recognized too.
func TestUserMediaUsageDedupesAndReleases(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t)
	if err := AutoMigrate(ctx, db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	now := time.Now().UTC()

	u1 := seedUsers(t, db, "usage-one")[0]
	u2 := seedUsers(t, db, "usage-two")[0]
	deckID := seedDeck(t, db, u1)

	sha1 := strings.Repeat("a", 64)
	sha2 := strings.Repeat("b", 64)
	m1 := Media{Sha256: sha1, RelPath: "aa/" + sha1 + ".png", Mime: "image/png", Bytes: 1000, CreatedAt: now}
	m2 := Media{Sha256: sha2, RelPath: "bb/" + sha2 + ".png", Mime: "image/png", Bytes: 500, CreatedAt: now}
	if err := db.Create(&m1).Error; err != nil {
		t.Fatalf("create media 1: %v", err)
	}
	if err := db.Create(&m2).Error; err != nil {
		t.Fatalf("create media 2: %v", err)
	}
	// u2 supplied both files, so u1's charge comes only from its own references; bytes u1's
	// soft-deleted notes leave behind stay charged to their uploader (u2).
	for _, sha := range []string{sha1, sha2} {
		if err := RecordMediaUploader(ctx, db, sha, u2); err != nil {
			t.Fatalf("record uploader %s: %v", sha, err)
		}
	}

	// u1's note: references both m1 (twice) and m2 as /media/<sha>.
	noteTypeID := stockNoteTypeID(t, db, deckID, "basic")
	noteU1 := Note{
		DeckID: deckID, NoteTypeID: Ptr(noteTypeID), TagsJSON: "[]",
		FieldsJSON: fmt.Sprintf(`{"front":"<img src=\"%s.png\"> <img src=\"%s.png\">","back":"<img src=\"%s.png\">"}`,
			sha1, sha1, sha2),
		CreatedBy: &u1, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&noteU1).Error; err != nil {
		t.Fatalf("create u1 note: %v", err)
	}
	mapNote(t, db, &noteU1)
	// u2's note: references only m1 (a cross-user duplicate reference must not affect u1's usage).
	noteU2 := Note{
		DeckID: deckID, NoteTypeID: Ptr(noteTypeID), TagsJSON: "[]",
		FieldsJSON: fmt.Sprintf(`{"front":"<img src=\"%s.png\">"}`, sha1),
		CreatedBy:  &u2, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&noteU2).Error; err != nil {
		t.Fatalf("create u2 note: %v", err)
	}
	mapNote(t, db, &noteU2)

	usage1, err := UserMediaUsage(ctx, db, u1)
	if err != nil {
		t.Fatalf("UserMediaUsage(u1) error = %v", err)
	}
	if usage1.Bytes != 1500 {
		t.Fatalf("u1 usage = %d, want 1500 (m1 counted once + m2)", usage1.Bytes)
	}
	if len(usage1.Sha256) != 2 || !usage1.Sha256[sha1] || !usage1.Sha256[sha2] {
		t.Fatalf("u1 sha set = %v, want {%s,%s}", usage1.Sha256, sha1, sha2)
	}
	usage2, err := UserMediaUsage(ctx, db, u2)
	if err != nil {
		t.Fatalf("UserMediaUsage(u2) error = %v", err)
	}
	if usage2.Bytes != 1000 {
		t.Fatalf("u2 usage = %d, want 1000 (its own reference to m1)", usage2.Bytes)
	}

	// deck-package form references are recognized too: add u1 a sha-form note that references only m2 → still only +500.
	pkgNote := Note{
		DeckID: deckID, NoteTypeID: Ptr(noteTypeID), TagsJSON: "[]",
		FieldsJSON: fmt.Sprintf(`{"front":"<img src=\"%s.png\">"}`, sha2),
		CreatedBy:  &u1, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&pkgNote).Error; err != nil {
		t.Fatalf("create sha-form note: %v", err)
	}
	mapNote(t, db, &pkgNote)
	if usage1, err = UserMediaUsage(ctx, db, u1); err != nil {
		t.Fatalf("UserMediaUsage(u1) after sha note error = %v", err)
	}
	if usage1.Bytes != 1500 {
		t.Fatalf("u1 usage after sha-form note = %d, want 1500 (no double count)", usage1.Bytes)
	}

	// delete u1's two referencing notes (soft delete) → u1's own charge is fully released; the bytes
	// that only u1's soft-deleted notes referenced become pending to their uploader u2.
	if err := db.Delete(&noteU1).Error; err != nil {
		t.Fatalf("soft delete u1 note: %v", err)
	}
	if err := db.Delete(&pkgNote).Error; err != nil {
		t.Fatalf("soft delete pkg note: %v", err)
	}
	if usage1, err = UserMediaUsage(ctx, db, u1); err != nil {
		t.Fatalf("UserMediaUsage(u1) after delete error = %v", err)
	}
	if usage1.Bytes != 0 || len(usage1.Sha256) != 0 {
		t.Fatalf("u1 usage after delete = %d (%d shas), want 0", usage1.Bytes, len(usage1.Sha256))
	}
	if usage2, err = UserMediaUsage(ctx, db, u2); err != nil {
		t.Fatalf("UserMediaUsage(u2) after u1 delete error = %v", err)
	}
	if usage2.Bytes != 1500 {
		t.Fatalf("u2 usage after u1 delete = %d, want 1500 (m1's live reference plus m2, which only the soft-deleted notes referenced)", usage2.Bytes)
	}
}

// mapNote creates the media_notes mapping for fixture notes: the fixture uses db.Create directly, bypassing NoteStore,
// while accounting reads the mapping (in production the mapping is maintained by the note write path).
func mapNote(t *testing.T, db *gorm.DB, n *Note) {
	t.Helper()
	if err := rebuildMediaNotes(context.Background(), db, n.ID, n.DeckID, NoteFieldValues(n.FieldsJSON)); err != nil {
		t.Fatalf("map fixture note: %v", err)
	}
}

// TestUserMediaUsageCountsPendingUploads covers 'pending uploads': an upload no note references is charged to
// the uploader (counter-case: not counting it would allow unlimited uploads of unreferenced files); once someone else's note references it, it is charged to
// the referencer and no longer to the uploader; when the reference disappears (mapping hard-deleted) it returns to the uploader.
func TestUserMediaUsageCountsPendingUploads(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t)
	if err := AutoMigrate(ctx, db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	now := time.Now().UTC()
	users := seedUsers(t, db, "pending-uploader", "pending-referrer")
	uploader, referrer := users[0], users[1]
	deckID := seedDeck(t, db, referrer)
	sha := strings.Repeat("c", 64)
	if err := db.Create(&Media{Sha256: sha, RelPath: "cc/" + sha + ".png", Mime: "image/png", Bytes: 700, CreatedAt: now}).Error; err != nil {
		t.Fatalf("create media: %v", err)
	}
	if err := RecordMediaUploader(ctx, db, sha, uploader); err != nil {
		t.Fatalf("record uploader: %v", err)
	}

	usage := func(user uint64) int64 {
		t.Helper()
		u, err := UserMediaUsage(ctx, db, user)
		if err != nil {
			t.Fatalf("UserMediaUsage(%d) error = %v", user, err)
		}
		return u.Bytes
	}
	if got := usage(uploader); got != 700 {
		t.Fatalf("pending upload: uploader usage = %d, want 700", got)
	}

	ref := Note{DeckID: deckID, NoteTypeID: Ptr(stockNoteTypeID(t, db, deckID, "basic")), TagsJSON: "[]", CreatedBy: &referrer, CreatedAt: now, UpdatedAt: now,
		FieldsJSON: fmt.Sprintf(`{"front":"<img src=\"%s.png\">","back":"x"}`, sha)}
	if err := db.Create(&ref).Error; err != nil {
		t.Fatalf("create referencing note: %v", err)
	}
	mapNote(t, db, &ref)
	if got := usage(uploader); got != 0 {
		t.Errorf("referenced by someone else: uploader usage = %d, want 0", got)
	}
	if got := usage(referrer); got != 700 {
		t.Errorf("referenced by someone else: referrer usage = %d, want 700", got)
	}

	if err := db.Where("note_id = ?", ref.ID).Delete(&MediaNote{}).Error; err != nil {
		t.Fatalf("drop mapping: %v", err)
	}
	if got := usage(uploader); got != 700 {
		t.Errorf("reference gone: uploader usage = %d, want 700 again", got)
	}
}

// TestUserMediaUsageChargesMediaRetainedForSoftDeletedNotes pins the retention/charging policy for media that
// only soft-deleted notes reference: the note write path keeps its media_notes mappings so the note can be
// restored, the collector therefore keeps those media rows, and the uploader stays charged for them until a
// live note references the bytes or reclamation removes them. Several upload/soft-delete/collect rounds must
// therefore consume the budget instead of returning usage to zero.
func TestUserMediaUsageChargesMediaRetainedForSoftDeletedNotes(t *testing.T) {
	const budget = int64(2500)
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			if err := AutoMigrate(ctx, db); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			now := time.Now().UTC()
			owner := seedUsers(t, db, "retained-media")[0]
			deckID := seedDeck(t, db, owner)
			noteTypeID := stockNoteTypeID(t, db, deckID, "basic")

			usage := func() int64 {
				t.Helper()
				u, err := UserMediaUsage(ctx, db, owner)
				if err != nil {
					t.Fatalf("UserMediaUsage() error = %v", err)
				}
				return u.Bytes
			}

			for round := 1; round <= 3; round++ {
				sha := fmt.Sprintf("%064d", round)
				if err := db.Create(&Media{Sha256: sha, RelPath: sha[:2] + "/" + sha + ".png", Mime: "image/png", Bytes: 1000, CreatedAt: now}).Error; err != nil {
					t.Fatalf("round %d: create media: %v", round, err)
				}
				if err := RecordMediaUploader(ctx, db, sha, owner); err != nil {
					t.Fatalf("round %d: record uploader: %v", round, err)
				}
				note := &Note{
					DeckID: deckID, NoteTypeID: Ptr(noteTypeID), TagsJSON: "[]",
					FieldsJSON: fmt.Sprintf(`{"front":"<img src=\"%s.png\">","back":"x"}`, sha),
					CreatedBy:  &owner, CreatedAt: now, UpdatedAt: now,
				}
				if err := db.Create(note).Error; err != nil {
					t.Fatalf("round %d: create note: %v", round, err)
				}
				mapNote(t, db, note)
				if got, want := usage(), int64(round)*1000; got != want {
					t.Fatalf("round %d: usage with a live reference = %d, want %d", round, got, want)
				}
				if err := db.Delete(note).Error; err != nil {
					t.Fatalf("round %d: soft delete note: %v", round, err)
				}
				if _, _, err := MarkOrphanedMedia(ctx, db, now); err != nil {
					t.Fatalf("round %d: MarkOrphanedMedia() error = %v", round, err)
				}
				deleted, err := DeleteOrphanedMedia(ctx, db, now, 10)
				if err != nil {
					t.Fatalf("round %d: DeleteOrphanedMedia() error = %v", round, err)
				}
				if len(deleted) != 0 {
					t.Fatalf("round %d: collector removed %d media that restorable notes still reference", round, len(deleted))
				}
				if got, want := usage(), int64(round)*1000; got != want {
					t.Fatalf("round %d: usage after soft delete + collect = %d, want %d (media retained for restore stays charged)", round, got, want)
				}
			}
			if got := usage(); got+1000 <= budget {
				t.Fatalf("charged usage after three retained rounds = %d against budget %d, want the next 1000-byte upload refused", got, budget)
			}
		})
	}
}

// TestMediaNamesInHTMLForms pins the three reference forms a field can carry and proves that an absolute
// URL is not treated as a deck-scoped file name.
func TestMediaNamesInHTMLForms(t *testing.T) {
	shaA := strings.Repeat("a", 64)
	shaB := strings.Repeat("b", 64)
	cases := []struct {
		name string
		html string
		want []string
	}{
		{name: "img src", html: `<img src="` + shaA + `.png">`, want: []string{shaA + ".png"}},
		{name: "single-quoted video src", html: `<video src='clip.mp4'></video>`, want: []string{"clip.mp4"}},
		{name: "sound tag", html: "listen [sound:bird.mp3] now", want: []string{"bird.mp3"}},
		{name: "css url", html: `<div style="background:url(` + shaB + `.jpg)">`, want: []string{shaB + ".jpg"}},
		{name: "quoted css url", html: `<div style="background:url('` + shaB + `.jpg')">`, want: []string{shaB + ".jpg"}},
		{name: "external url is not a name", html: `<img src="https://cdn.example.com/a.png">`, want: nil},
		{name: "site path is not a name", html: `<img src="/media/` + shaA + `">`, want: nil},
		{name: "duplicate name once", html: `<img src="a.png"><img src="a.png">`, want: []string{"a.png"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mediaNamesInHTML(tc.html)
			if len(got) != len(tc.want) {
				t.Fatalf("mediaNamesInHTML(%q) = %v, want %v", tc.html, got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("mediaNamesInHTML(%q)[%d] = %q, want %q", tc.html, i, got[i], tc.want[i])
				}
			}
		})
	}
}
