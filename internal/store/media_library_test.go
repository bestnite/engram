package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/mediatype"
	"git.nite07.com/nite/engram/internal/retention"
)

// This file covers the read side of the global media browser (M22-1): the readable set, the four reference states, the
// deck/class/state/name filters, the deck-scoped labels and filtered keyset paging.
//
// The core invariant is that the browser and the reclamation job share one definition: the state is derived from the
// collector's own conditions (orphanCondition in media_gc.go), and TestMediaLibraryStateMatchesCollector drives the
// collector on the same fixture to prove the two agree. Media rows are written directly where the state needs a specific
// orphan stamp or mime; notes always go through NoteStore's write method so media_notes is built by the write path.

// mediaLibraryFixture is the shared scene of the browser tests: two users, three decks (own, granted, invisible) and
// one readable medium per interesting state.
type mediaLibraryFixture struct {
	owner, other uint64
	now          time.Time

	deckOwned, deckGranted, deckPrivate uint64

	shaReferenced    string // live note in own deck
	shaPendingInside string // orphan stamp, grace still running
	shaPendingPast   string // orphan stamp, grace elapsed but no sweep yet
	shaRestorable    string // only a soft-deleted note maps to it
	shaTypeAsset     string // referenced by a note type `_` asset name, never by a note
	shaUnreferenced  string // orphan condition holds, no stamp yet
	shaShared        string // live note in the granted deck plus a name in the own deck
	shaUploadOnly    string // readable only through the uploader branch, referenced from a private deck
	shaPrivate       string // another user's medium, unreadable here
}

// seedMediaLibraryFixture builds the shared scene on one database.
func seedMediaLibraryFixture(t *testing.T, db *gorm.DB) mediaLibraryFixture {
	t.Helper()
	ctx := context.Background()
	users := seedUsers(t, db, "lib_owner", "lib_other")
	f := mediaLibraryFixture{owner: users[0], other: users[1]}
	f.now = time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	f.deckOwned = seedDeck(t, db, f.owner)
	f.deckGranted = seedDeck(t, db, f.other)
	f.deckPrivate = seedDeck(t, db, f.other)
	if err := db.Model(&Deck{}).Where("id = ?", f.deckOwned).Update("name", "Alpha Owned").Error; err != nil {
		t.Fatalf("name owned deck: %v", err)
	}
	if err := db.Model(&Deck{}).Where("id = ?", f.deckGranted).Update("name", "Beta Granted").Error; err != nil {
		t.Fatalf("name granted deck: %v", err)
	}
	if err := db.Model(&Deck{}).Where("id = ?", f.deckPrivate).Update("name", "Zulu Private").Error; err != nil {
		t.Fatalf("name private deck: %v", err)
	}
	if err := NewGrantStore(db).Grant(ctx, f.deckGranted, f.owner, RoleReader, Ptr(f.other)); err != nil {
		t.Fatalf("grant reader on shared deck: %v", err)
	}

	base := f.now
	uploaded := func(sha, mime string, createdAt time.Time) {
		t.Helper()
		row := Media{Sha256: sha, RelPath: sha[:2] + "/" + sha, Mime: mime, Bytes: 12, CreatedAt: createdAt}
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("create media %s: %v", sha, err)
		}
		if err := RecordMediaUploader(ctx, db, sha, f.owner); err != nil {
			t.Fatalf("record uploader of %s: %v", sha, err)
		}
	}
	name := func(deckID uint64, mediaName, sha string) {
		t.Helper()
		if err := NewMediaNameStore(db).Upsert(ctx, deckID, mediaName, sha); err != nil {
			t.Fatalf("register media name %q: %v", mediaName, err)
		}
	}

	f.shaReferenced = testMediaSha(101)
	uploaded(f.shaReferenced, "image/png", base.Add(1*time.Minute))
	saveRefNote(t, db, f.deckOwned, Ptr(f.owner), refFront(f.shaReferenced))
	name(f.deckOwned, "Photo One.PNG", f.shaReferenced)
	// A name registered in a deck the owner cannot see must never reach names, decks or a q match.
	name(f.deckPrivate, "hidden-secret.png", f.shaReferenced)

	f.shaPendingInside = testMediaSha(102)
	uploaded(f.shaPendingInside, "audio/mpeg", base.Add(2*time.Minute))
	stampOrphaned(t, db, f.shaPendingInside, base.Add(-retention.MediaOrphanGrace+time.Hour))

	f.shaPendingPast = testMediaSha(103)
	uploaded(f.shaPendingPast, "video/mp4", base.Add(3*time.Minute))
	stampOrphaned(t, db, f.shaPendingPast, base.Add(-retention.MediaOrphanGrace-time.Hour))

	f.shaRestorable = testMediaSha(104)
	uploaded(f.shaRestorable, "font/woff2", base.Add(4*time.Minute))
	noteID := saveRefNote(t, db, f.deckOwned, Ptr(f.owner), refFront(f.shaRestorable))
	if err := NewNoteStore(db).Delete(ctx, noteID); err != nil {
		t.Fatalf("soft-delete referencing note: %v", err)
	}
	// A stale orphan stamp must not turn a restorable medium into a pending one.
	stampOrphaned(t, db, f.shaRestorable, base.Add(-2*time.Hour))

	f.shaTypeAsset = testMediaSha(105)
	uploaded(f.shaTypeAsset, "text/javascript", base.Add(5*time.Minute))
	{
		ntID := stockNoteTypeID(t, db, f.deckOwned, "basic")
		name(f.deckOwned, "_header.png", f.shaTypeAsset)
		if err := db.Create(&MediaNoteType{NoteTypeID: ntID, MediaName: "_header.png"}).Error; err != nil {
			t.Fatalf("create note type media reference: %v", err)
		}
	}

	f.shaUnreferenced = testMediaSha(106)
	uploaded(f.shaUnreferenced, "text/css", base.Add(6*time.Minute))
	name(f.deckOwned, "100% done.png", f.shaUnreferenced)

	f.shaShared = testMediaSha(107)
	uploaded(f.shaShared, "application/octet-stream", base.Add(7*time.Minute))
	saveRefNote(t, db, f.deckGranted, Ptr(f.owner), refFront(f.shaShared))
	name(f.deckOwned, "shared-alias.png", f.shaShared)

	f.shaUploadOnly = testMediaSha(108)
	uploaded(f.shaUploadOnly, "image/png", base.Add(8*time.Minute))
	saveRefNote(t, db, f.deckPrivate, Ptr(f.owner), refFront(f.shaUploadOnly))

	f.shaPrivate = testMediaSha(109)
	if err := db.Create(&Media{Sha256: f.shaPrivate, RelPath: f.shaPrivate[:2] + "/" + f.shaPrivate, Mime: "image/png", Bytes: 12, CreatedAt: base.Add(9 * time.Minute)}).Error; err != nil {
		t.Fatalf("create private media: %v", err)
	}
	if err := RecordMediaUploader(ctx, db, f.shaPrivate, f.other); err != nil {
		t.Fatalf("record private uploader: %v", err)
	}
	saveRefNote(t, db, f.deckPrivate, Ptr(f.other), refFront(f.shaPrivate))

	return f
}

// stampOrphaned writes one media row's orphan stamp directly (the collector would write the same column).
func stampOrphaned(t *testing.T, db *gorm.DB, sha string, at time.Time) {
	t.Helper()
	if err := db.Model(&Media{}).Where("sha256 = ?", sha).Update("orphaned_at", at.UTC()).Error; err != nil {
		t.Fatalf("stamp orphaned_at on %s: %v", sha, err)
	}
}

// collectMediaLibrary walks every page of one filtered listing and returns the entries in order.
func collectMediaLibrary(t *testing.T, db *gorm.DB, userID uint64, limit int, filter MediaLibraryFilter) []MediaLibraryEntry {
	t.Helper()
	cursor := ""
	var out []MediaLibraryEntry
	for page := 0; page < 100; page++ {
		entries, next, err := ListMediaLibrary(context.Background(), db, userID, limit, cursor, filter)
		if err != nil {
			t.Fatalf("ListMediaLibrary page %d filter %+v error = %v", page, filter, err)
		}
		out = append(out, entries...)
		if next == "" {
			return out
		}
		cursor = next
	}
	t.Fatalf("media library pagination did not terminate")
	return nil
}

// entryShas returns the entries' shas in order.
func entryShas(entries []MediaLibraryEntry) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.Sha256)
	}
	return out
}

// libraryEntryBySha finds one entry in a page.
func libraryEntryBySha(t *testing.T, entries []MediaLibraryEntry, sha string) MediaLibraryEntry {
	t.Helper()
	for _, entry := range entries {
		if entry.Sha256 == sha {
			return entry
		}
	}
	t.Fatalf("media %s missing from page %v", sha, entryShas(entries))
	return MediaLibraryEntry{}
}

// assertShas asserts a listing's exact sha order.
func assertShas(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s shas = %v (%d), want %v (%d)", label, got, len(got), want, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s shas[%d] = %s, want %s (all: %v)", label, i, got[i], want[i], got)
		}
	}
}

// TestMediaLibraryStatesLabelsAndPrivacy covers the full readable set with one medium per state: names/decks are scoped
// to the caller's visible decks, a sha shared by two decks is listed once with both labels, a restorable medium ignores a
// stale orphan stamp, and another user's medium is absent.
func TestMediaLibraryStatesLabelsAndPrivacy(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			f := seedMediaLibraryFixture(t, db)
			now := f.now

			entries := collectMediaLibrary(t, db, f.owner, 100, MediaLibraryFilter{})
			wantOrder := []string{f.shaUploadOnly, f.shaShared, f.shaUnreferenced, f.shaTypeAsset, f.shaRestorable, f.shaPendingPast, f.shaPendingInside, f.shaReferenced}
			assertShas(t, "default listing", entryShas(entries), wantOrder)
			for _, entry := range entries {
				if entry.Sha256 == f.shaPrivate {
					t.Fatalf("listing leaked another user's unreadable medium %s", f.shaPrivate)
				}
			}

			cases := []struct {
				sha   string
				state string
			}{
				{f.shaReferenced, MediaStateReferenced},
				{f.shaPendingInside, MediaStatePending},
				{f.shaPendingPast, MediaStatePending},
				{f.shaRestorable, MediaStateRestorable},
				{f.shaTypeAsset, MediaStateReferenced},
				{f.shaUnreferenced, MediaStateUnreferenced},
				{f.shaShared, MediaStateReferenced},
				{f.shaUploadOnly, MediaStateReferenced},
			}
			for _, tc := range cases {
				entry := libraryEntryBySha(t, entries, tc.sha)
				if entry.State != tc.state {
					t.Errorf("media %s state = %q, want %q", tc.sha, entry.State, tc.state)
				}
			}

			// The store reports the orphan stamp; the transport turns it into cleanup_at. It is non-null only for a
			// pending item, and the store never subtracts the grace itself (that policy lives in internal/retention and
			// is applied by internal/api, so the store stays out of the retention test dependency graph).
			for _, tc := range cases {
				entry := libraryEntryBySha(t, entries, tc.sha)
				switch tc.state {
				case MediaStatePending:
					if entry.OrphanedAt == nil {
						t.Fatalf("pending media %s has no orphan stamp", tc.sha)
					}
					stamp := now
					if tc.sha == f.shaPendingInside {
						stamp = now.Add(-retention.MediaOrphanGrace + time.Hour)
					}
					if tc.sha == f.shaPendingPast {
						stamp = now.Add(-retention.MediaOrphanGrace - time.Hour)
					}
					if !entry.OrphanedAt.Equal(stamp) {
						t.Errorf("media %s orphan stamp = %s, want %s", tc.sha, entry.OrphanedAt, stamp)
					}
				default:
					// A restorable medium may still carry a stale stamp until the next sweep clears it (its state, not
					// the stamp, decides); every other non-pending state must have none.
					if tc.state != MediaStateRestorable && entry.OrphanedAt != nil {
						t.Errorf("media %s state %q reports orphan stamp %s, want null", tc.sha, tc.state, entry.OrphanedAt)
					}
				}
			}

			// names and decks: every label belongs to a visible deck and is sorted and deduplicated.
			referenced := libraryEntryBySha(t, entries, f.shaReferenced)
			assertStrings(t, "referenced names", referenced.Names, []string{f.shaReferenced + ".png", "Photo One.PNG"})
			assertDecks(t, "referenced decks", referenced.Decks, []MediaLibraryDeck{{PublicID: publicIDOf(t, db, f.deckOwned), Name: "Alpha Owned"}})

			typeAsset := libraryEntryBySha(t, entries, f.shaTypeAsset)
			assertStrings(t, "note type asset names", typeAsset.Names, []string{"_header.png"})

			restorable := libraryEntryBySha(t, entries, f.shaRestorable)
			assertStrings(t, "restorable names", restorable.Names, []string{f.shaRestorable + ".png"})

			shared := libraryEntryBySha(t, entries, f.shaShared)
			assertStrings(t, "shared names", shared.Names, []string{f.shaShared + ".png", "shared-alias.png"})
			assertDecks(t, "shared decks", shared.Decks, []MediaLibraryDeck{
				{PublicID: publicIDOf(t, db, f.deckOwned), Name: "Alpha Owned"},
				{PublicID: publicIDOf(t, db, f.deckGranted), Name: "Beta Granted"},
			})

			// Uploader-only media: readable through the uploader branch even though the only referencing deck is invisible,
			// and that invisible deck contributes no name and no label.
			uploadOnly := libraryEntryBySha(t, entries, f.shaUploadOnly)
			assertStrings(t, "uploader-only names", uploadOnly.Names, []string{})
			assertDecks(t, "uploader-only decks", uploadOnly.Decks, []MediaLibraryDeck{})

			unreferenced := libraryEntryBySha(t, entries, f.shaUnreferenced)
			assertStrings(t, "unreferenced names", unreferenced.Names, []string{"100% done.png"})
			assertDecks(t, "unreferenced decks", unreferenced.Decks, []MediaLibraryDeck{{PublicID: publicIDOf(t, db, f.deckOwned), Name: "Alpha Owned"}})

			pending := libraryEntryBySha(t, entries, f.shaPendingInside)
			assertStrings(t, "pending names", pending.Names, []string{})
			assertDecks(t, "pending decks", pending.Decks, []MediaLibraryDeck{})
		})
	}
}

// TestMediaLibraryFilters covers every filter alone and combined, filtered keyset paging across a page boundary, and the
// q semantics: a case-insensitive substring over visible names only, with LIKE wildcards treated literally.
func TestMediaLibraryFilters(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			f := seedMediaLibraryFixture(t, db)

			deckFiltered := collectMediaLibrary(t, db, f.owner, 100, MediaLibraryFilter{DeckID: f.deckOwned})
			// A deck filter matches both the live notes referencing the bytes and the names registered in the deck, so the
			// own deck also matches media whose only link is a name row (the shared alias and the note-type asset).
			assertShas(t, "deck filter", entryShas(deckFiltered), []string{
				f.shaShared, f.shaUnreferenced, f.shaTypeAsset, f.shaRestorable, f.shaReferenced,
			})

			grantedFiltered := collectMediaLibrary(t, db, f.owner, 100, MediaLibraryFilter{DeckID: f.deckGranted})
			assertShas(t, "granted deck filter", entryShas(grantedFiltered), []string{f.shaShared})

			assertShas(t, "state pending", entryShas(collectMediaLibrary(t, db, f.owner, 100, MediaLibraryFilter{State: MediaStatePending})),
				[]string{f.shaPendingPast, f.shaPendingInside})
			assertShas(t, "state restorable", entryShas(collectMediaLibrary(t, db, f.owner, 100, MediaLibraryFilter{State: MediaStateRestorable})),
				[]string{f.shaRestorable})
			assertShas(t, "state unreferenced", entryShas(collectMediaLibrary(t, db, f.owner, 100, MediaLibraryFilter{State: MediaStateUnreferenced})),
				[]string{f.shaUnreferenced})
			assertShas(t, "state referenced", entryShas(collectMediaLibrary(t, db, f.owner, 100, MediaLibraryFilter{State: MediaStateReferenced})),
				[]string{f.shaUploadOnly, f.shaShared, f.shaTypeAsset, f.shaReferenced})

			assertShas(t, "class image", entryShas(collectMediaLibrary(t, db, f.owner, 100, MediaLibraryFilter{Class: "image"})),
				[]string{f.shaUploadOnly, f.shaReferenced})
			assertShas(t, "class audio", entryShas(collectMediaLibrary(t, db, f.owner, 100, MediaLibraryFilter{Class: "audio"})),
				[]string{f.shaPendingInside})
			assertShas(t, "class video", entryShas(collectMediaLibrary(t, db, f.owner, 100, MediaLibraryFilter{Class: "video"})),
				[]string{f.shaPendingPast})
			assertShas(t, "class font", entryShas(collectMediaLibrary(t, db, f.owner, 100, MediaLibraryFilter{Class: "font"})),
				[]string{f.shaRestorable})
			assertShas(t, "class script", entryShas(collectMediaLibrary(t, db, f.owner, 100, MediaLibraryFilter{Class: "script"})),
				[]string{f.shaTypeAsset})
			assertShas(t, "class style", entryShas(collectMediaLibrary(t, db, f.owner, 100, MediaLibraryFilter{Class: "style"})),
				[]string{f.shaUnreferenced})
			assertShas(t, "class other", entryShas(collectMediaLibrary(t, db, f.owner, 100, MediaLibraryFilter{Class: "other"})),
				[]string{f.shaShared})

			assertShas(t, "q photo", entryShas(collectMediaLibrary(t, db, f.owner, 100, MediaLibraryFilter{Query: "photo"})),
				[]string{f.shaReferenced})
			assertShas(t, "q PHOTO", entryShas(collectMediaLibrary(t, db, f.owner, 100, MediaLibraryFilter{Query: "PHOTO"})),
				[]string{f.shaReferenced})
			assertShas(t, "q header", entryShas(collectMediaLibrary(t, db, f.owner, 100, MediaLibraryFilter{Query: "header"})),
				[]string{f.shaTypeAsset})
			// A name registered in a deck the caller cannot see must not answer the search.
			assertShas(t, "q private name", entryShas(collectMediaLibrary(t, db, f.owner, 100, MediaLibraryFilter{Query: "hidden-secret"})), nil)
			// LIKE wildcards are literal characters: `100%` matches only the name containing `100%`, and a lone `%`
			// matches nothing else than that name instead of matching every medium.
			assertShas(t, "q literal percent", entryShas(collectMediaLibrary(t, db, f.owner, 100, MediaLibraryFilter{Query: "100%"})),
				[]string{f.shaUnreferenced})
			assertShas(t, "q lone percent", entryShas(collectMediaLibrary(t, db, f.owner, 100, MediaLibraryFilter{Query: "%"})),
				[]string{f.shaUnreferenced})

			// Filters combine with each other and with the keyset cursor.
			combined := collectMediaLibrary(t, db, f.owner, 100, MediaLibraryFilter{
				DeckID: f.deckOwned, Class: "image", State: MediaStateReferenced, Query: "photo",
			})
			assertShas(t, "combined filters", entryShas(combined), []string{f.shaReferenced})

			// Filtered paging across a page boundary: 5 own-deck rows at limit 2 yields 3 pages, in listing order, no gaps.
			paged := collectMediaLibrary(t, db, f.owner, 2, MediaLibraryFilter{DeckID: f.deckOwned})
			assertShas(t, "paged deck filter", entryShas(paged), []string{
				f.shaShared, f.shaUnreferenced, f.shaTypeAsset, f.shaRestorable, f.shaReferenced,
			})
			seen := map[string]int{}
			for _, sha := range entryShas(paged) {
				seen[sha]++
			}
			for sha, count := range seen {
				if count != 1 {
					t.Errorf("paged listing returned %s %d times, want once", sha, count)
				}
			}
		})
	}
}

// TestMediaLibraryClassFilterMatchesClassifier proves the SQL class mapping equals mediatype.ClassOfMime, including the
// aliases and the parameter suffix the classifier normalises. One medium per mime is seeded; for every class the filter
// must return exactly the media whose ClassOfMime is that class.
func TestMediaLibraryClassFilterMatchesClassifier(t *testing.T) {
	mimes := []string{
		"image/png", "IMAGE/JPEG", "image/svg+xml",
		"audio/mpeg", "audio/x-wav", "audio/ogg",
		"video/mp4", "video/webm",
		"font/woff2", "font/ttf",
		"text/javascript", "application/javascript", "text/javascript; charset=utf-8",
		"text/css", "text/css; charset=utf-8",
		"application/octet-stream", "application/pdf", "application/x-javascript",
	}
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "class_owner")
			owner := users[0]
			base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
			byClass := map[string]map[string]bool{}
			for i, mime := range mimes {
				sha := testMediaSha(500 + i)
				row := Media{Sha256: sha, RelPath: sha[:2] + "/" + sha, Mime: mime, Bytes: 1, CreatedAt: base.Add(time.Duration(i) * time.Minute)}
				if err := db.Create(&row).Error; err != nil {
					t.Fatalf("create media %s: %v", sha, err)
				}
				if err := RecordMediaUploader(ctx, db, sha, owner); err != nil {
					t.Fatalf("record uploader: %v", err)
				}
				class := string(mediatype.ClassOfMime(mime))
				if byClass[class] == nil {
					byClass[class] = map[string]bool{}
				}
				byClass[class][sha] = true
			}
			for _, class := range []string{"image", "audio", "video", "font", "script", "style", "other"} {
				entries := collectMediaLibrary(t, db, owner, 100, MediaLibraryFilter{Class: class})
				got := map[string]bool{}
				for _, entry := range entries {
					got[entry.Sha256] = true
				}
				want := byClass[class]
				if len(got) != len(want) {
					t.Errorf("class %q returned %d media (%v), want %d", class, len(got), entryShas(entries), len(want))
				}
				for sha := range want {
					if !got[sha] {
						t.Errorf("class %q omitted %s (mime mapped by ClassOfMime to that class)", class, sha)
					}
				}
			}
		})
	}
}

// TestMediaLibraryStateMatchesCollector runs the reclamation job on the fixture and proves the page's states describe
// exactly what the job does: only the unreferenced medium is stamped by the marking pass, and only the pending medium
// whose grace has elapsed is reclaimed. A restorable medium with a stale stamp is neither.
func TestMediaLibraryStateMatchesCollector(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			f := seedMediaLibraryFixture(t, db)

			marked, cleared, err := MarkOrphanedMedia(ctx, db, f.now)
			if err != nil {
				t.Fatalf("MarkOrphanedMedia() error = %v", err)
			}
			// Exactly the unreferenced medium is new to the orphan set: referenced, restorable and already-stamped media stay.
			if marked != 1 || cleared != 1 {
				// The restorable medium carried a stale stamp; the marking sweep clears it because the medium is no longer
				// orphaned, which is exactly the stamp the page already ignored.
				t.Fatalf("MarkOrphanedMedia() = (%d marked, %d cleared), want (1, 1)", marked, cleared)
			}
			if state := libraryEntryBySha(t, collectMediaLibrary(t, db, f.owner, 100, MediaLibraryFilter{}), f.shaUnreferenced).State; state != MediaStatePending {
				t.Fatalf("medium %s state after the marking sweep = %q, want %q", f.shaUnreferenced, state, MediaStatePending)
			}

			deleted, err := DeleteOrphanedMedia(ctx, db, f.now.Add(-retention.MediaOrphanGrace), 100)
			if err != nil {
				t.Fatalf("DeleteOrphanedMedia() error = %v", err)
			}
			deletedShas := make([]string, 0, len(deleted))
			for _, row := range deleted {
				deletedShas = append(deletedShas, row.Sha256)
			}
			assertShas(t, "reclaimed media", deletedShas, []string{f.shaPendingPast})

			remaining := collectMediaLibrary(t, db, f.owner, 100, MediaLibraryFilter{})
			assertShas(t, "listing after the sweep", entryShas(remaining), []string{
				f.shaUploadOnly, f.shaShared, f.shaUnreferenced, f.shaTypeAsset, f.shaRestorable, f.shaPendingInside, f.shaReferenced,
			})
			if state := libraryEntryBySha(t, remaining, f.shaUnreferenced).State; state != MediaStatePending {
				t.Errorf("newly stamped medium state = %q, want %q", state, MediaStatePending)
			}
			if entry := libraryEntryBySha(t, remaining, f.shaRestorable); entry.State != MediaStateRestorable || entry.OrphanedAt != nil {
				t.Errorf("restorable medium after the sweep = state %q orphan stamp %v, want restorable and no stamp", entry.State, entry.OrphanedAt)
			}
		})
	}
}

// TestMediaLibraryRejectsInvalidFilters asserts the store's stable sentinels for invalid class/state/cursor values; the
// REST transport maps them to 400.
func TestMediaLibraryRejectsInvalidFilters(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			if _, _, err := ListMediaLibrary(ctx, db, 1, 10, "", MediaLibraryFilter{Class: "nope"}); !errors.Is(err, ErrInvalidMediaClass) {
				t.Errorf("invalid class error = %v, want ErrInvalidMediaClass", err)
			}
			if _, _, err := ListMediaLibrary(ctx, db, 1, 10, "", MediaLibraryFilter{State: "nope"}); !errors.Is(err, ErrInvalidMediaState) {
				t.Errorf("invalid state error = %v, want ErrInvalidMediaState", err)
			}
			if _, _, err := ListMediaLibrary(ctx, db, 1, 10, "not-a-cursor", MediaLibraryFilter{}); !errors.Is(err, ErrInvalidMediaCursor) {
				t.Errorf("invalid cursor error = %v, want ErrInvalidMediaCursor", err)
			}
		})
	}
}

// publicIDOf reads a deck's external id for label assertions.
func publicIDOf(t *testing.T, db *gorm.DB, deckID uint64) string {
	t.Helper()
	var deck Deck
	if err := db.First(&deck, "id = ?", deckID).Error; err != nil {
		t.Fatalf("load deck %d: %v", deckID, err)
	}
	return deck.PublicID
}

// assertStrings asserts a string slice exactly.
func assertStrings(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v (%d), want %v (%d)", label, got, len(got), want, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s[%d] = %q, want %q (all: %v)", label, i, got[i], want[i], got)
		}
	}
}

// assertDecks asserts a deck label list exactly.
func assertDecks(t *testing.T, label string, got, want []MediaLibraryDeck) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %+v (%d), want %+v (%d)", label, got, len(got), want, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s[%d] = %+v, want %+v", label, i, got[i], want[i])
		}
	}
}

// TestMediaLibrarySearchFoldsNonASCII proves the q filter folds Unicode on both drivers: a name stored as "Été.png"
// is found by its lower-case and upper-case spellings. PostgreSQL's LOWER folds the whole of Unicode; SQLite compares
// through the same Go fold rule (sqlite_lower.go), so the two databases answer the same search.
func TestMediaLibrarySearchFoldsNonASCII(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "nonascii_owner")
			owner := users[0]
			deck := seedDeck(t, db, owner)
			sha := testMediaSha(900)
			row := Media{Sha256: sha, RelPath: sha[:2] + "/" + sha, Mime: "image/png", Bytes: 1, CreatedAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)}
			if err := db.Create(&row).Error; err != nil {
				t.Fatalf("create media: %v", err)
			}
			if err := RecordMediaUploader(ctx, db, sha, owner); err != nil {
				t.Fatalf("record uploader: %v", err)
			}
			if err := NewMediaNameStore(db).Upsert(ctx, deck, "Été.png", sha); err != nil {
				t.Fatalf("register non-ASCII name: %v", err)
			}
			for _, query := range []string{"été", "ÉTÉ", "éTé", "Été"} {
				assertShas(t, "non-ASCII query "+query, entryShas(collectMediaLibrary(t, db, owner, 100, MediaLibraryFilter{Query: query})), []string{sha})
			}
			assertShas(t, "non-ASCII miss", entryShas(collectMediaLibrary(t, db, owner, 100, MediaLibraryFilter{Query: "hiver"})), nil)
		})
	}
}
