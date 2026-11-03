package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/retention"
	"git.nite07.com/nite/engram/internal/store"
)

// This file covers the M22-1 additions to REST `GET /api/v1/media`: the item fields names/decks/state/cleanup_at, the
// deck/class/state/q filters over the existing keyset paging, the 400s for invalid filters and the privacy rule that
// another user's medium appears in no item, name or search result.

// mediaLibraryPageBody is the JSON shape of one media library page.
type mediaLibraryPageBody struct {
	Items      []mediaLibraryItemBody `json:"items"`
	NextCursor string                 `json:"next_cursor"`
}

// mediaLibraryItemBody decodes an item; CleanupAt stays a pointer so a JSON null is distinguishable from a value.
type mediaLibraryItemBody struct {
	Sha256    string `json:"sha256"`
	Mime      string `json:"mime"`
	Bytes     int64  `json:"bytes"`
	URL       string `json:"url"`
	Width     *int   `json:"width"`
	Height    *int   `json:"height"`
	CreatedAt string `json:"created_at"`
	Names     []string
	Decks     []mediaLibraryDeckBody
	State     string
	CleanupAt *string `json:"cleanup_at"`
}

// mediaLibraryDeckBody decodes one deck label.
type mediaLibraryDeckBody struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// mediaLibrarySha generates a stable 64-digit lowercase hex sha for a test row.
func mediaLibrarySha(i int) string { return fmt.Sprintf("%064x", i) }

// seedLibraryMediaRow writes one media row with an explicit mime and creation time, registering the uploader.
func seedLibraryMediaRow(t *testing.T, env *testEnv, ownerID uint64, sha, mime string, createdAt time.Time) {
	t.Helper()
	row := store.Media{Sha256: sha, RelPath: sha[:2] + "/" + sha, Mime: mime, Bytes: 34, CreatedAt: createdAt}
	if err := env.db.Create(&row).Error; err != nil {
		t.Fatalf("create media %s: %v", sha, err)
	}
	if err := store.RecordMediaUploader(context.Background(), env.db, sha, ownerID); err != nil {
		t.Fatalf("record uploader of %s: %v", sha, err)
	}
}

// stampLibraryOrphan writes a media row's orphan stamp directly.
func stampLibraryOrphan(t *testing.T, env *testEnv, sha string, at time.Time) {
	t.Helper()
	if err := env.db.Model(&store.Media{}).Where("sha256 = ?", sha).Update("orphaned_at", at.UTC()).Error; err != nil {
		t.Fatalf("stamp orphaned_at of %s: %v", sha, err)
	}
}

// renameDeck sets a deck's display name so label ordering is assertable.
func renameDeck(t *testing.T, env *testEnv, deckID uint64, name string) {
	t.Helper()
	if err := env.db.Model(&store.Deck{}).Where("id = ?", deckID).Update("name", name).Error; err != nil {
		t.Fatalf("rename deck %d: %v", deckID, err)
	}
}

// fetchMediaLibrary reads one page and fails the test on a non-200.
func fetchMediaLibrary(t *testing.T, router http.Handler, key, query string) mediaLibraryPageBody {
	t.Helper()
	code, raw := doJSON(t, router, http.MethodGet, "/api/v1/media"+query, key, "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/v1/media%s = %d, want 200 (body %s)", query, code, raw)
	}
	var body mediaLibraryPageBody
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode media page: %v (%s)", err, raw)
	}
	return body
}

// walkFilteredMediaLibrary pages a filtered listing until next_cursor is empty.
func walkFilteredMediaLibrary(t *testing.T, router http.Handler, key, query string, limit int) []mediaLibraryItemBody {
	t.Helper()
	var out []mediaLibraryItemBody
	cursor := ""
	for page := 0; page < 50; page++ {
		path := fmt.Sprintf("%s&limit=%d", query, limit)
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		body := fetchMediaLibrary(t, router, key, path)
		out = append(out, body.Items...)
		if body.NextCursor == "" {
			return out
		}
		cursor = body.NextCursor
	}
	t.Fatalf("media library paging did not terminate")
	return nil
}

// itemShas returns an item slice's shas in order.
func itemShas(items []mediaLibraryItemBody) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Sha256)
	}
	return out
}

// findItem returns the item with the given sha or fails.
func findItem(t *testing.T, items []mediaLibraryItemBody, sha string) mediaLibraryItemBody {
	t.Helper()
	for _, item := range items {
		if item.Sha256 == sha {
			return item
		}
	}
	t.Fatalf("media %s missing from items %v", sha, itemShas(items))
	return mediaLibraryItemBody{}
}

// assertItemShas asserts an item order exactly.
func assertItemShas(t *testing.T, label string, got, want []string) {
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

// TestMediaLibraryEndpointShapeStatesAndPrivacy covers the item shape, every reference state with its cleanup_at rule,
// the visible-only labels of a sha shared by two decks, and the absence of another user's medium.
func TestMediaLibraryEndpointShapeStatesAndPrivacy(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	owner := seedUser(t, env.db, "lib_api_owner", store.RoleUser)
	other := seedUser(t, env.db, "lib_api_other", store.RoleUser)
	deckOwned := seedDeck(t, env.db, owner.ID)
	deckGranted := seedDeck(t, env.db, other.ID)
	deckPrivate := seedDeck(t, env.db, other.ID)
	renameDeck(t, env, deckOwned.ID, "Alpha")
	renameDeck(t, env, deckGranted.ID, "Beta")
	renameDeck(t, env, deckPrivate.ID, "Hidden")
	if err := store.NewGrantStore(env.db).Grant(context.Background(), deckGranted.ID, owner.ID, store.RoleReader, store.Ptr(other.ID)); err != nil {
		t.Fatalf("grant reader: %v", err)
	}

	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	referenced := seedMedia(t, env, owner.ID, "library-referenced")
	// SaveBytes stamps created_at with the wall clock; pin it so the listing order is deterministic.
	if err := env.db.Model(&store.Media{}).Where("sha256 = ?", referenced).Update("created_at", now.Add(time.Minute)).Error; err != nil {
		t.Fatalf("pin referenced created_at: %v", err)
	}
	seedRefNote(t, env, deckOwned.ID, owner.ID, referenced)
	if err := store.NewMediaNameStore(env.db).Upsert(context.Background(), deckOwned.ID, "Photo One.PNG", referenced); err != nil {
		t.Fatalf("register alias: %v", err)
	}
	// A name in a deck the caller cannot see must not appear in names or decks.
	if err := store.NewMediaNameStore(env.db).Upsert(context.Background(), deckPrivate.ID, "hidden-secret.png", referenced); err != nil {
		t.Fatalf("register hidden alias: %v", err)
	}

	pendingInside := mediaLibrarySha(701)
	seedLibraryMediaRow(t, env, owner.ID, pendingInside, "audio/mpeg", now.Add(2*time.Minute))
	insideStamp := now.Add(-retention.MediaOrphanGrace + time.Hour)
	stampLibraryOrphan(t, env, pendingInside, insideStamp)

	pendingPast := mediaLibrarySha(702)
	seedLibraryMediaRow(t, env, owner.ID, pendingPast, "video/mp4", now.Add(3*time.Minute))
	pastStamp := now.Add(-retention.MediaOrphanGrace - time.Hour)
	stampLibraryOrphan(t, env, pendingPast, pastStamp)

	restorable := mediaLibrarySha(703)
	seedLibraryMediaRow(t, env, owner.ID, restorable, "font/woff2", now.Add(4*time.Minute))
	restoreNote := seedRefNote(t, env, deckOwned.ID, owner.ID, restorable)
	if err := store.NewNoteStore(env.db).Delete(context.Background(), restoreNote.ID); err != nil {
		t.Fatalf("soft-delete note: %v", err)
	}
	// A stale stamp must not turn restorable into pending.
	stampLibraryOrphan(t, env, restorable, now.Add(-2*time.Hour))

	shared := mediaLibrarySha(704)
	seedLibraryMediaRow(t, env, owner.ID, shared, "application/octet-stream", now.Add(5*time.Minute))
	seedRefNote(t, env, deckGranted.ID, owner.ID, shared)
	if err := store.NewMediaNameStore(env.db).Upsert(context.Background(), deckOwned.ID, "shared-alias.bin", shared); err != nil {
		t.Fatalf("register shared alias: %v", err)
	}

	unreferenced := mediaLibrarySha(705)
	seedLibraryMediaRow(t, env, owner.ID, unreferenced, "text/css", now.Add(6*time.Minute))

	secret := mediaLibrarySha(706)
	seedLibraryMediaRow(t, env, other.ID, secret, "image/png", now.Add(7*time.Minute))
	seedRefNote(t, env, deckPrivate.ID, other.ID, secret)

	key := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead}, nil)
	router := env.router()

	body := fetchMediaLibrary(t, router, key.Plaintext, "?limit=100")
	if len(body.Items) != 6 || body.NextCursor != "" {
		t.Fatalf("page has %d items (cursor %q), want 6 and empty cursor (%v)", len(body.Items), body.NextCursor, itemShas(body.Items))
	}
	assertItemShas(t, "listing", itemShas(body.Items), []string{
		unreferenced, shared, restorable, pendingPast, pendingInside, referenced,
	})
	for _, item := range body.Items {
		if item.Sha256 == secret {
			t.Fatalf("listing leaked another user's unreadable medium %s", secret)
		}
		if item.URL != "/media/"+item.Sha256 {
			t.Errorf("item %s url = %q, want /media/<sha256>", item.Sha256, item.URL)
		}
		if item.Names == nil || item.Decks == nil {
			t.Errorf("item %s has null names or decks; both must serialise as arrays", item.Sha256)
		}
	}

	referencedItem := findItem(t, body.Items, referenced)
	if referencedItem.State != store.MediaStateReferenced || referencedItem.CleanupAt != nil {
		t.Errorf("referenced item = state %q cleanup %v, want referenced and null", referencedItem.State, referencedItem.CleanupAt)
	}
	wantNames := []string{referenced + ".png", "Photo One.PNG"}
	if len(referencedItem.Names) != len(wantNames) || referencedItem.Names[0] != wantNames[0] || referencedItem.Names[1] != wantNames[1] {
		t.Errorf("referenced names = %v, want %v (hidden deck's name must be absent)", referencedItem.Names, wantNames)
	}
	wantDecks := []mediaLibraryDeckBody{{ID: deckOwned.PublicID, Name: "Alpha"}}
	if len(referencedItem.Decks) != 1 || referencedItem.Decks[0] != wantDecks[0] {
		t.Errorf("referenced decks = %+v, want %+v", referencedItem.Decks, wantDecks)
	}

	insideItem := findItem(t, body.Items, pendingInside)
	if insideItem.State != store.MediaStatePending || insideItem.CleanupAt == nil {
		t.Fatalf("just-inside-grace item = state %q cleanup %v, want pending with cleanup_at", insideItem.State, insideItem.CleanupAt)
	}
	if got := parseTimestamp(t, *insideItem.CleanupAt); !got.Equal(insideStamp.Add(retention.MediaOrphanGrace)) {
		t.Errorf("inside cleanup_at = %s, want %s", got, insideStamp.Add(retention.MediaOrphanGrace))
	}
	pastItem := findItem(t, body.Items, pendingPast)
	if pastItem.State != store.MediaStatePending || pastItem.CleanupAt == nil {
		t.Fatalf("past-grace item = state %q cleanup %v, want pending with cleanup_at in the past", pastItem.State, pastItem.CleanupAt)
	}
	if got := parseTimestamp(t, *pastItem.CleanupAt); !got.Equal(pastStamp.Add(retention.MediaOrphanGrace)) || !got.Before(now) {
		t.Errorf("past cleanup_at = %s, want %s (in the past)", got, pastStamp.Add(retention.MediaOrphanGrace))
	}

	restorableItem := findItem(t, body.Items, restorable)
	if restorableItem.State != store.MediaStateRestorable || restorableItem.CleanupAt != nil {
		t.Errorf("restorable item = state %q cleanup %v, want restorable and null", restorableItem.State, restorableItem.CleanupAt)
	}
	unreferencedItem := findItem(t, body.Items, unreferenced)
	if unreferencedItem.State != store.MediaStateUnreferenced || unreferencedItem.CleanupAt != nil {
		t.Errorf("unreferenced item = state %q cleanup %v, want unreferenced and null", unreferencedItem.State, unreferencedItem.CleanupAt)
	}

	sharedItem := findItem(t, body.Items, shared)
	if sharedItem.State != store.MediaStateReferenced || len(sharedItem.Decks) != 2 {
		t.Fatalf("shared item = state %q decks %+v, want referenced with both visible decks", sharedItem.State, sharedItem.Decks)
	}
	// Sorted by name; the names are unique here.
	if sharedItem.Decks[0].Name != "Alpha" || sharedItem.Decks[1].Name != "Beta" {
		t.Errorf("shared decks = %+v, want Alpha then Beta", sharedItem.Decks)
	}
	if len(sharedItem.Names) != 2 || sharedItem.Names[0] != shared+".png" || sharedItem.Names[1] != "shared-alias.bin" {
		t.Errorf("shared names = %v, want sorted canonical name then alias", sharedItem.Names)
	}

	// The other user sees their own medium.
	otherKey := seedKey(t, env.keys, other.ID, []string{store.ScopeRead}, nil)
	otherBody := fetchMediaLibrary(t, router, otherKey.Plaintext, "?limit=100")
	findItem(t, otherBody.Items, secret)
}

// parseTimestamp parses a JSON RFC 3339 timestamp from the API.
func parseTimestamp(t *testing.T, raw string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		t.Fatalf("parse timestamp %q: %v", raw, err)
	}
	return parsed.UTC()
}

// TestMediaLibraryEndpointFilters covers each filter alone and combined, plus filtered keyset paging across a page boundary.
func TestMediaLibraryEndpointFilters(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	user := seedUser(t, env.db, "lib_api_filter", store.RoleUser)
	deckOne := seedDeck(t, env.db, user.ID)
	deckTwo := seedDeck(t, env.db, user.ID)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	image := mediaLibrarySha(801)
	audio := mediaLibrarySha(802)
	video := mediaLibrarySha(803)
	font := mediaLibrarySha(804)
	script := mediaLibrarySha(805)
	style := mediaLibrarySha(806)
	otherMime := mediaLibrarySha(807)
	rows := []struct {
		sha, mime, name string
		deckID          uint64
		at              time.Time
	}{
		{image, "image/png", "Sunset.PNG", deckOne.ID, now.Add(1 * time.Minute)},
		{audio, "audio/mpeg", "Song.mp3", deckOne.ID, now.Add(2 * time.Minute)},
		{video, "video/mp4", "Clip.mp4", deckTwo.ID, now.Add(3 * time.Minute)},
		{font, "font/woff2", "_font.woff2", deckOne.ID, now.Add(4 * time.Minute)},
		{script, "text/javascript", "_script.js", deckOne.ID, now.Add(5 * time.Minute)},
		{style, "text/css", "_style.css", deckOne.ID, now.Add(6 * time.Minute)},
		{otherMime, "application/octet-stream", "notes.bin", deckTwo.ID, now.Add(7 * time.Minute)},
	}
	for _, row := range rows {
		seedLibraryMediaRow(t, env, user.ID, row.sha, row.mime, row.at)
		if err := store.NewMediaNameStore(env.db).Upsert(ctx, row.deckID, row.name, row.sha); err != nil {
			t.Fatalf("register name %q: %v", row.name, err)
		}
	}
	// A live note in deck one makes the image readable through the note branch too (it is already readable as an upload).
	seedRefNote(t, env, deckOne.ID, user.ID, image)

	key := seedKey(t, env.keys, user.ID, []string{store.ScopeRead}, nil)
	router := env.router()

	assertItemShas(t, "deck one filter", itemShas(fetchMediaLibrary(t, router, key.Plaintext, "?deck="+url.QueryEscape(deckOne.PublicID)).Items),
		[]string{style, script, font, audio, image})
	assertItemShas(t, "class image", itemShas(fetchMediaLibrary(t, router, key.Plaintext, "?class=image").Items), []string{image})
	assertItemShas(t, "class audio", itemShas(fetchMediaLibrary(t, router, key.Plaintext, "?class=audio").Items), []string{audio})
	assertItemShas(t, "class other", itemShas(fetchMediaLibrary(t, router, key.Plaintext, "?class=other").Items), []string{otherMime})
	assertItemShas(t, "state unreferenced", itemShas(fetchMediaLibrary(t, router, key.Plaintext, "?state=unreferenced").Items),
		[]string{otherMime, style, script, font, video, audio})
	assertItemShas(t, "state referenced", itemShas(fetchMediaLibrary(t, router, key.Plaintext, "?state=referenced").Items), []string{image})
	assertItemShas(t, "q lowercase", itemShas(fetchMediaLibrary(t, router, key.Plaintext, "?q=sunset").Items), []string{image})
	assertItemShas(t, "q uppercase", itemShas(fetchMediaLibrary(t, router, key.Plaintext, "?q=SUNSET").Items), []string{image})
	assertItemShas(t, "q no match", itemShas(fetchMediaLibrary(t, router, key.Plaintext, "?q=absent").Items), nil)

	combined := fetchMediaLibrary(t, router, key.Plaintext,
		"?deck="+url.QueryEscape(deckOne.PublicID)+"&class=audio&state=unreferenced&q=song")
	assertItemShas(t, "combined filters", itemShas(combined.Items), []string{audio})

	// Filtered keyset paging across a page boundary: the whole unreferenced set at limit 2 takes 3 pages.
	paged := walkFilteredMediaLibrary(t, router, key.Plaintext, "?state=unreferenced", 2)
	assertItemShas(t, "paged state filter", itemShas(paged), []string{otherMime, style, script, font, video, audio})
	seen := map[string]int{}
	for _, sha := range itemShas(paged) {
		seen[sha]++
	}
	for sha, count := range seen {
		if count != 1 {
			t.Errorf("paged listing returned %s %d times, want once", sha, count)
		}
	}
}

// TestMediaLibraryEndpointRejectsInvalidFilters covers the stable 400 codes, the deck access rule and the anonymous 401.
func TestMediaLibraryEndpointRejectsInvalidFilters(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	owner := seedUser(t, env.db, "lib_api_params", store.RoleUser)
	other := seedUser(t, env.db, "lib_api_private", store.RoleUser)
	privateDeck := seedDeck(t, env.db, other.ID)
	key := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead}, nil)
	router := env.router()

	cases := []struct {
		name  string
		query string
	}{
		{"unknown class", "?class=nope"},
		{"unknown state", "?state=nope"},
		{"invalid cursor", "?cursor=" + url.QueryEscape("not-a-cursor")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, raw := doJSON(t, router, http.MethodGet, "/api/v1/media"+tc.query, key.Plaintext, "")
			if code != http.StatusBadRequest {
				t.Fatalf("GET /api/v1/media%s = %d, want 400 (body %s)", tc.query, code, raw)
			}
			assertErrorCode(t, raw, CodeInvalidRequest)
		})
	}

	// Unknown public deck id: same not_found envelope as every other deck-scoped endpoint.
	code, raw := doJSON(t, router, http.MethodGet, "/api/v1/media?deck=does-not-exist", key.Plaintext, "")
	if code != http.StatusNotFound {
		t.Fatalf("unknown deck filter = %d, want 404 (body %s)", code, raw)
	}
	assertErrorCode(t, raw, CodeNotFound)

	// A deck the caller cannot read cannot be used to narrow the listing.
	code, raw = doJSON(t, router, http.MethodGet, "/api/v1/media?deck="+url.QueryEscape(privateDeck.PublicID), key.Plaintext, "")
	if code != http.StatusForbidden {
		t.Fatalf("unreadable deck filter = %d, want 403 (body %s)", code, raw)
	}
	assertErrorCode(t, raw, CodeForbidden)

	// Anonymous: 401, as before this change.
	code, raw = doJSON(t, router, http.MethodGet, "/api/v1/media", "", "")
	if code != http.StatusUnauthorized {
		t.Fatalf("anonymous GET /api/v1/media = %d, want 401 (body %s)", code, raw)
	}
}

// TestMediaLibraryEndpointSearchStaysInsideVisibleNames proves a name registered in an invisible deck cannot be used as
// a search oracle: the medium is still listed when readable, but the invisible name neither matches nor leaks.
func TestMediaLibraryEndpointSearchStaysInsideVisibleNames(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	owner := seedUser(t, env.db, "lib_api_search_owner", store.RoleUser)
	other := seedUser(t, env.db, "lib_api_search_other", store.RoleUser)
	visible := seedDeck(t, env.db, owner.ID)
	hidden := seedDeck(t, env.db, other.ID)
	sha := seedMedia(t, env, owner.ID, "library-search")
	if err := store.NewMediaNameStore(env.db).Upsert(context.Background(), hidden.ID, "top-secret.png", sha); err != nil {
		t.Fatalf("register hidden name: %v", err)
	}
	key := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead}, nil)
	router := env.router()

	// The medium is readable through the uploader branch, so it is listed, but with no visible name and no visible deck.
	body := fetchMediaLibrary(t, router, key.Plaintext, "?limit=100")
	item := findItem(t, body.Items, sha)
	if len(item.Names) != 0 || len(item.Decks) != 0 {
		t.Fatalf("item exposes labels from an invisible deck: names %v decks %+v", item.Names, item.Decks)
	}
	// The invisible name is not a search oracle.
	assertItemShas(t, "hidden name search", itemShas(fetchMediaLibrary(t, router, key.Plaintext, "?q=top-secret").Items), nil)
	// The deck filter cannot reference the invisible deck at all (403 is covered above); a visible deck does not match it.
	assertItemShas(t, "visible deck search", itemShas(fetchMediaLibrary(t, router, key.Plaintext, "?deck="+url.QueryEscape(visible.PublicID)).Items), nil)
}
