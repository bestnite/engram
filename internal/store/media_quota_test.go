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
//   - removing a reference (soft-deleting the note) frees the quota;
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

	// u1's note: references both m1 (twice) and m2 as /media/<sha>.
	noteU1 := Note{
		DeckID: deckID, Kind: "basic", TagsJSON: "[]",
		FieldsJSON: fmt.Sprintf(`{"front":"![](/media/%s) /media/%s","back":"![](/media/%s)"}`,
			sha1, sha1, sha2),
		CreatedBy: &u1, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&noteU1).Error; err != nil {
		t.Fatalf("create u1 note: %v", err)
	}
	mapNote(t, db, &noteU1)
	// u2's note: references only m1 (a cross-user duplicate reference must not affect u1's usage).
	noteU2 := Note{
		DeckID: deckID, Kind: "basic", TagsJSON: "[]",
		FieldsJSON: fmt.Sprintf(`{"front":"![](/media/%s)"}`, sha1),
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
		DeckID: deckID, Kind: "basic", TagsJSON: "[]",
		FieldsJSON: fmt.Sprintf(`{"front":"![](media/%s.png)"}`, sha2),
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

	// delete u1's two referencing notes (soft delete) → the quota is fully released; u2 is unaffected.
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
	if usage2.Bytes != 1000 {
		t.Fatalf("u2 usage after u1 delete = %d, want 1000 (unchanged)", usage2.Bytes)
	}
}

// mapNote creates the media_notes mapping for fixture notes: the fixture uses db.Create directly, bypassing NoteStore,
// while accounting reads the mapping (in production the mapping is maintained by the note write path).
func mapNote(t *testing.T, db *gorm.DB, n *Note) {
	t.Helper()
	fields, err := ParseFields(n.FieldsJSON)
	if err != nil {
		t.Fatalf("parse fixture fields: %v", err)
	}
	if err := rebuildMediaNotes(context.Background(), db, n.ID, fields); err != nil {
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

	ref := Note{DeckID: deckID, Kind: "basic", TagsJSON: "[]", CreatedBy: &referrer, CreatedAt: now, UpdatedAt: now,
		FieldsJSON: fmt.Sprintf(`{"front":"![](/media/%s)","back":"x"}`, sha)}
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

// TestScanMediaRefsForms pins the two reference forms and proves they do not mis-match each other and that the old numeric id form is no longer recognized.
func TestScanMediaRefsForms(t *testing.T) {
	shaA := strings.Repeat("a", 64)
	shaB := strings.Repeat("b", 64)
	cases := []struct {
		name  string
		text  string
		want  []string
		avoid []string // strings that must not be collected
	}{
		{name: "url form", text: "![](" + "/media/" + shaA + ")", want: []string{shaA}},
		{name: "package form", text: "![](media/" + shaB + ".png)", want: []string{shaB}},
		{name: "both forms in one field", text: "/media/" + shaA + " and media/" + shaB + ".jpg",
			want: []string{shaA, shaB}},
		{name: "old numeric id ignored", text: "![](/media/123)", want: nil, avoid: []string{"123"}},
		// a 65-digit hex must not be truncated to 64 and matched by mistake: it records the 64-digit prefix while the whole string is 65 digits.
		{name: "65 hex not a ref", text: "/media/" + shaA + "f", avoid: []string{shaA}},
		// a URL-form 64-digit sha followed immediately by an extension (not in-package form, since it has a leading slash) still yields only the same sha.
		{name: "url form with extension", text: "/media/" + shaB + ".png", want: []string{shaB}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := map[string]bool{}
			scanMediaRefs(tc.text, got)
			for _, w := range tc.want {
				if !got[w] {
					t.Errorf("scanMediaRefs(%q) missed %q; got %v", tc.text, w, got)
				}
			}
			for _, a := range tc.avoid {
				if got[a] {
					t.Errorf("scanMediaRefs(%q) wrongly collected %q; got %v", tc.text, a, got)
				}
			}
		})
	}
}
