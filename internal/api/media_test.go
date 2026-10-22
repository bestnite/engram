package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// This file covers REST `GET /api/v1/media` (paging): auth / scope / stable JSON shape / cursor,
// and "only readable media is listed" - media referenced in someone else's private deck must not appear.

// seedRefNote creates a basic note referencing the given media sha, authored by authorID.
// It goes through NoteStore's write method so the media_notes mapping is built by the write path (a fixture convention).
func seedRefNote(t *testing.T, env *testEnv, deckID, authorID uint64, sha string) *store.Note {
	t.Helper()
	n := store.Note{DeckID: deckID, Kind: "basic", CreatedBy: store.Ptr(authorID)}
	fields := map[string]any{"front": "![](/media/" + sha + ")", "back": "x"}
	if _, err := store.NewNoteStore(env.db).Save(context.Background(), &n, fields); err != nil {
		t.Fatalf("save referencing note: %v", err)
	}
	return &n
}

// mediaPageBody is the response shape of GET /api/v1/media.
type mediaPageBody struct {
	Items []struct {
		Sha256    string `json:"sha256"`
		Mime      string `json:"mime"`
		Bytes     int64  `json:"bytes"`
		Width     *int   `json:"width"`
		Height    *int   `json:"height"`
		CreatedAt string `json:"created_at"`
	} `json:"items"`
	NextCursor string `json:"next_cursor"`
}

// walkMediaPages pages repeatedly until next_cursor is empty, returning the full sha order and the page count.
func walkMediaPages(t *testing.T, router http.Handler, key string, limit int) ([]string, int) {
	t.Helper()
	var out []string
	cursor := ""
	pages := 0
	for i := 0; i < 50; i++ {
		path := fmt.Sprintf("/api/v1/media?limit=%d", limit)
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		code, raw := doJSON(t, router, http.MethodGet, path, key, "")
		if code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200 (body %s)", path, code, raw)
		}
		var body mediaPageBody
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("decode media page: %v (%s)", err, raw)
		}
		pages++
		for _, it := range body.Items {
			out = append(out, it.Sha256)
		}
		if body.NextCursor == "" {
			return out, pages
		}
		cursor = body.NextCursor
	}
	t.Fatalf("media pagination did not terminate")
	return nil, pages
}

// TestListMediaEndpointScopeShapeAndPagination covers the 200 shape, scope, paging and the readable range.
func TestListMediaEndpointScopeShapeAndPagination(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	owner := seedUser(t, env.db, "media_list_owner", store.RoleUser)
	other := seedUser(t, env.db, "media_list_other", store.RoleUser)
	router := env.router()

	// owner uploads 3 media files.
	owned := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		owned = append(owned, seedMedia(t, env, owner.ID, fmt.Sprintf("owned-%d", i)))
	}
	// Media in someone else's private deck: owner must not see it.
	privateDeck := seedDeck(t, env.db, other.ID)
	secret := seedMedia(t, env, other.ID, "secret-media")
	seedRefNote(t, env, privateDeck.ID, other.ID, secret)

	// One of owner's own cards references owned[0]: those bytes match on two paths (uploaded + referenced by a visible card),
	// so the list must deduplicate them into one entry.
	ownerDeck := seedDeck(t, env.db, owner.ID)
	seedRefNote(t, env, ownerDeck.ID, owner.ID, owned[0])

	key := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead}, nil)

	// First page with limit=2: shape and cursor.
	code, raw := doJSON(t, router, http.MethodGet, "/api/v1/media?limit=2", key.Plaintext, "")
	if code != http.StatusOK {
		t.Fatalf("GET /media = %d, want 200 (body %s)", code, raw)
	}
	var first mediaPageBody
	if err := json.Unmarshal(raw, &first); err != nil {
		t.Fatalf("decode first page: %v (%s)", err, raw)
	}
	if len(first.Items) != 2 {
		t.Fatalf("first page has %d items, want 2 (body %s)", len(first.Items), raw)
	}
	if first.NextCursor == "" {
		t.Fatalf("first page next_cursor is empty while a further page exists")
	}
	if it := first.Items[0]; it.Mime != "image/png" || it.Bytes <= 0 || it.Sha256 == "" || it.CreatedAt == "" {
		t.Errorf("media item shape is incomplete: %+v", it)
	}

	// Walk every page: no duplicates, no gaps, and only the 3 entries owner can read.
	got, pages := walkMediaPages(t, router, key.Plaintext, 2)
	if pages != 2 {
		t.Errorf("pagination used %d pages for 3 items at limit 2, want 2", pages)
	}
	seen := map[string]int{}
	for _, sha := range got {
		seen[sha]++
	}
	if len(got) != len(owned) {
		t.Fatalf("paged media = %v (%d), want only owner's %d", got, len(got), len(owned))
	}
	for _, sha := range owned {
		if seen[sha] != 1 {
			t.Errorf("owned media %s appears %d times, want exactly once (all: %v)", sha, seen[sha], got)
		}
	}
	if seen[secret] != 0 {
		t.Errorf("list leaked another user's private media %s: %v", secret, got)
	}

	// Fetch everything in one page (a large enough limit): after union dedupe each byte sequence appears once (keyset's strict comparison only
	// blocks duplicate rows sharing a sort key across pages, so UNION dedupe is only really verified when one page holds it all).
	code, raw = doJSON(t, router, http.MethodGet, "/api/v1/media?limit=100", key.Plaintext, "")
	if code != http.StatusOK {
		t.Fatalf("GET /media?limit=100 = %d, want 200 (body %s)", code, raw)
	}
	var full mediaPageBody
	if err := json.Unmarshal(raw, &full); err != nil {
		t.Fatalf("decode full page: %v (%s)", err, raw)
	}
	if len(full.Items) != len(owned) || full.NextCursor != "" {
		t.Fatalf("full page has %d items (cursor %q), want %d and empty cursor", len(full.Items), full.NextCursor, len(owned))
	}
	fullSeen := map[string]int{}
	for _, it := range full.Items {
		fullSeen[it.Sha256]++
	}
	for sha, n := range fullSeen {
		if n != 1 {
			t.Errorf("deduped page shows media %s %d times, want once", sha, n)
		}
	}

	// The last page's next_cursor must be an empty string.
	code, raw = doJSON(t, router, http.MethodGet, "/api/v1/media?limit=2&cursor="+url.QueryEscape(first.NextCursor), key.Plaintext, "")
	if code != http.StatusOK {
		t.Fatalf("GET second page = %d, want 200 (body %s)", code, raw)
	}
	var second mediaPageBody
	if err := json.Unmarshal(raw, &second); err != nil {
		t.Fatalf("decode second page: %v (%s)", err, raw)
	}
	if second.NextCursor != "" {
		t.Errorf("last page next_cursor = %q, want empty", second.NextCursor)
	}

	// scope: a key holding only write is rejected, stable code scope_required.
	writeKey := seedKey(t, env.keys, owner.ID, []string{store.ScopeWrite}, nil)
	code, raw = doJSON(t, router, http.MethodGet, "/api/v1/media", writeKey.Plaintext, "")
	if code != http.StatusForbidden {
		t.Fatalf("GET /media with write scope = %d, want 403 (body %s)", code, raw)
	}
	assertErrorCode(t, raw, CodeScopeRequired)

	// Unauthenticated: 401.
	code, raw = doJSON(t, router, http.MethodGet, "/api/v1/media", "", "")
	if code != http.StatusUnauthorized {
		t.Fatalf("GET /media without a key = %d, want 401 (body %s)", code, raw)
	}
}

// TestListMediaEndpointRejectsInvalidLimitAndCursor covers invalid parameters: always 400, never a silent clamp.
func TestListMediaEndpointRejectsInvalidLimitAndCursor(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	user := seedUser(t, env.db, "media_param_user", store.RoleUser)
	key := seedKey(t, env.keys, user.ID, []string{store.ScopeRead}, nil)
	router := env.router()

	cases := []struct {
		name  string
		query string
	}{
		{"zero limit", "?limit=0"},
		{"negative limit", "?limit=-3"},
		{"non-numeric limit", "?limit=abc"},
		{"over max limit", "?limit=100000"},
		{"invalid cursor", "?cursor=" + url.QueryEscape("not-a-valid-cursor")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, raw := doJSON(t, router, http.MethodGet, "/api/v1/media"+tc.query, key.Plaintext, "")
			if code != http.StatusBadRequest {
				t.Fatalf("GET /media%s = %d, want 400 (body %s)", tc.query, code, raw)
			}
			assertErrorCode(t, raw, CodeInvalidRequest)
		})
	}
}
