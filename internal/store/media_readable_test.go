package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"
)

// This file covers the media library listing (the store primitive shared by GET /api/v1/media and the picker):
//   - the readable set is the union of 'what I uploaded' ∪ 'referenced by non-soft-deleted notes in decks I can read', and must be deduped;
//   - keyset paging neither duplicates nor skips under the same cursor, and ordering by media.created_at DESC, sha256 DESC is stable;
//   - media referenced inside someone else's deck must not appear; only an explicit grant (deck_grants) brings those bytes into the readable set;
//   - after a grant is revoked it disappears from the listing immediately (no caching).
//
// Media rows are written directly to control created_at precisely (the sort key); notes are always built through NoteStore's write method,
// guaranteeing media_notes is rebuilt by the write path (AGENTS.md's fixture convention).

// testMediaSha generates a stable 64-digit lowercase hex sha (different i, different sha).
func testMediaSha(i int) string { return fmt.Sprintf("%064x", i) }

// seedListMediaRow writes one media row directly, with created_at given by the caller (the listing sort key).
func seedListMediaRow(t *testing.T, db *gorm.DB, sha string, createdAt time.Time) {
	t.Helper()
	row := Media{Sha256: sha, RelPath: sha[:2] + "/" + sha + ".png", Mime: "image/png", Bytes: 12, CreatedAt: createdAt}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create media %s: %v", sha, err)
	}
}

// listAllReadable pages repeatedly by limit and returns the full sha sequence; a repeated request with the same cursor goes through the same path,
// which makes 'no duplicates, no gaps' easy to assert.
func listAllReadable(t *testing.T, db *gorm.DB, userID uint64, limit int) []string {
	t.Helper()
	var out []string
	cursor := ""
	for page := 0; page < 100; page++ {
		rows, next, err := ListReadableMedia(context.Background(), db, userID, limit, cursor)
		if err != nil {
			t.Fatalf("ListReadableMedia page %d error = %v", page, err)
		}
		for _, m := range rows {
			out = append(out, m.Sha256)
		}
		if next == "" {
			return out
		}
		cursor = next
	}
	t.Fatalf("pagination did not terminate after 100 pages")
	return out
}

// TestListReadableMediaPaginatesStableAndDeduped asserts union dedup and stable keyset paging.
func TestListReadableMediaPaginatesStableAndDeduped(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			users := seedUsers(t, db, "list_owner", "list_other")
			owner, other := users[0], users[1]
			deck := seedDeck(t, db, owner)
			ctx := context.Background()
			base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

			// m0..m4 are uploaded by owner; m5 is uploaded by other but referenced by a note in a deck visible to owner (hits only the second branch).
			for i := 0; i < 5; i++ {
				sha := testMediaSha(i + 1)
				seedListMediaRow(t, db, sha, base.Add(time.Duration(i)*time.Minute))
				if err := RecordMediaUploader(ctx, db, sha, owner); err != nil {
					t.Fatalf("RecordMediaUploader(%s): %v", sha, err)
				}
			}
			shaRefOnly := testMediaSha(99)
			seedListMediaRow(t, db, shaRefOnly, base.Add(10*time.Minute))
			if err := RecordMediaUploader(ctx, db, shaRefOnly, other); err != nil {
				t.Fatalf("RecordMediaUploader(ref-only): %v", err)
			}

			// one note references both m3 (owner also uploaded it, so both union branches hit and it must appear only once)
			// and a sha that is only referenced, not uploaded by owner (second branch).
			saveRefNote(t, db, deck, Ptr(owner), refFront(testMediaSha(3), shaRefOnly))

			got := listAllReadable(t, db, owner, 2)
			want := []string{shaRefOnly, testMediaSha(5), testMediaSha(4), testMediaSha(3), testMediaSha(2), testMediaSha(1)}
			if len(got) != len(want) {
				t.Fatalf("readable media = %v (%d), want %v (%d)", got, len(got), want, len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("readable media order[%d] = %s, want %s (all: %v)", i, got[i], want[i], got)
				}
			}

			// fetch everything in one page (a sufficiently large limit): the union must dedupe, and bytes hit on both paths appear only once.
			// This assertion cannot rely on paging alone — keyset's strict less-than keeps 'duplicate rows with the same sort key' off the next page,
			// so UNION dedup is only really verified when a single page can hold every row.
			single, singleNext, err := ListReadableMedia(ctx, db, owner, 10, "")
			if err != nil {
				t.Fatalf("single page error = %v", err)
			}
			if singleNext != "" {
				t.Fatalf("single page returned a cursor %q while the whole set fits", singleNext)
			}
			if len(single) != len(want) {
				shas := make([]string, 0, len(single))
				for _, m := range single {
					shas = append(shas, m.Sha256)
				}
				t.Fatalf("single page has %d rows, want %d deduped (got %v)", len(single), len(want), shas)
			}

			// repeating a request with the same cursor must yield the same page (keyset is stable), with no duplicates and no gaps.
			first, nextCursor, err := ListReadableMedia(ctx, db, owner, 2, "")
			if err != nil {
				t.Fatalf("first page error = %v", err)
			}
			if nextCursor == "" {
				t.Fatalf("first page returned an empty cursor while more pages exist")
			}
			again, nextAgain, err := ListReadableMedia(ctx, db, owner, 2, "")
			if err != nil {
				t.Fatalf("repeat first page error = %v", err)
			}
			if len(again) != len(first) || nextAgain != nextCursor {
				t.Fatalf("repeating the first page drifted: first %v/%q vs again %v/%q", first, nextCursor, again, nextAgain)
			}

			// the last page's next_cursor must be an empty string.
			last, lastNext, err := ListReadableMedia(ctx, db, owner, 2, nextCursorOf(t, db, owner, 4))
			if err != nil {
				t.Fatalf("last page error = %v", err)
			}
			if len(last) == 0 || lastNext != "" {
				t.Fatalf("last page = (%d rows, cursor %q), want non-empty rows and empty cursor", len(last), lastNext)
			}
		})
	}
}

// nextCursorOf pages forward, accumulating up to the n-th item, and returns that item's cursor (used to fetch the last page directly).
func nextCursorOf(t *testing.T, db *gorm.DB, userID uint64, n int) string {
	t.Helper()
	cursor := ""
	seen := 0
	for {
		rows, next, err := ListReadableMedia(context.Background(), db, userID, 1, cursor)
		if err != nil {
			t.Fatalf("walk cursor error = %v", err)
		}
		if len(rows) == 0 {
			t.Fatalf("walk cursor exhausted before %d items", n)
		}
		seen++
		if seen >= n {
			return next
		}
		cursor = next
	}
}

// TestListReadableMediaExcludesOtherUsersDecks asserts that media inside someone else's deck does not appear, and that
// only an explicit grant (deck_grants) brings those bytes into the readable set — visibility is no longer a source of read access.
func TestListReadableMediaExcludesOtherUsersDecks(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			users := seedUsers(t, db, "scope_owner", "scope_other")
			owner, other := users[0], users[1]
			ctx := context.Background()
			base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

			foreignDeck := seedDeck(t, db, other)
			grantedDeck := seedDeck(t, db, other)

			hidden := testMediaSha(201)
			seedListMediaRow(t, db, hidden, base)
			if err := RecordMediaUploader(ctx, db, hidden, other); err != nil {
				t.Fatalf("record uploader: %v", err)
			}
			saveRefNote(t, db, foreignDeck, Ptr(other), refFront(hidden))

			shared := testMediaSha(202)
			seedListMediaRow(t, db, shared, base.Add(time.Minute))
			if err := RecordMediaUploader(ctx, db, shared, other); err != nil {
				t.Fatalf("record uploader: %v", err)
			}
			saveRefNote(t, db, grantedDeck, Ptr(other), refFront(shared))

			// before the grant: not a single byte from someone else's deck gets in.
			for _, sha := range listAllReadable(t, db, owner, 10) {
				if sha == hidden || sha == shared {
					t.Fatalf("list leaked media %s from another user's deck before any grant", sha)
				}
			}

			// grant only grantedDeck: the bytes it references appear with the grant, while ungranted ones still do not appear.
			if err := NewGrantStore(db).Grant(ctx, grantedDeck, owner, RoleReader, Ptr(other)); err != nil {
				t.Fatalf("grant: %v", err)
			}
			got := listAllReadable(t, db, owner, 10)
			found := false
			for _, sha := range got {
				if sha == hidden {
					t.Fatalf("list leaked media %s from an ungranted deck: %v", hidden, got)
				}
				if sha == shared {
					found = true
				}
			}
			if !found {
				t.Fatalf("list omitted media %s referenced by a granted deck: %v", shared, got)
			}
		})
	}
}

// TestListReadableMediaDropsAfterGrantRevoked asserts that after a grant is revoked the media disappears from the listing immediately (no caching).
func TestListReadableMediaDropsAfterGrantRevoked(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			users := seedUsers(t, db, "revoke_owner", "revoke_viewer")
			owner, viewer := users[0], users[1]
			ctx := context.Background()
			deck := seedDeck(t, db, owner)

			sha := testMediaSha(301)
			seedListMediaRow(t, db, sha, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
			if err := RecordMediaUploader(ctx, db, sha, owner); err != nil {
				t.Fatalf("record uploader: %v", err)
			}
			saveRefNote(t, db, deck, Ptr(owner), refFront(sha))

			grants := NewGrantStore(db)
			if err := grants.Grant(ctx, deck, viewer, RoleReader, Ptr(owner)); err != nil {
				t.Fatalf("grant: %v", err)
			}
			if got := listAllReadable(t, db, viewer, 10); len(got) != 1 || got[0] != sha {
				t.Fatalf("viewer readable before revoke = %v, want [%s]", got, sha)
			}
			if err := grants.Revoke(ctx, deck, viewer); err != nil {
				t.Fatalf("revoke: %v", err)
			}
			if got := listAllReadable(t, db, viewer, 10); len(got) != 0 {
				t.Fatalf("viewer readable after revoke = %v, want empty", got)
			}
		})
	}
}

// TestListReadableMediaRejectsInvalidCursor asserts that an invalid cursor returns the stable error (for REST to map to 400).
func TestListReadableMediaRejectsInvalidCursor(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			if _, _, err := ListReadableMedia(context.Background(), db, 1, 10, "not-a-cursor"); !errors.Is(err, ErrInvalidMediaCursor) {
				t.Fatalf("ListReadableMedia(invalid cursor) error = %v, want ErrInvalidMediaCursor", err)
			}
		})
	}
}
