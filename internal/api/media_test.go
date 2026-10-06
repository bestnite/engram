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

// 本文件覆盖 REST `GET /api/v1/media`（分页）：鉴权 / scope / 稳定 JSON 形状 / 游标，
// 以及「只列可读媒体」——他人 private 卡组里引用的媒体不得出现。

// seedRefNote 建一条引用给定媒体 sha 的 basic note，作者为 authorID。
// 走 NoteStore 的写入方法，让 media_notes 映射由写入路径建立（测试夹具约定）。
func seedRefNote(t *testing.T, env *testEnv, deckID, authorID uint64, sha string) *store.Note {
	t.Helper()
	n := store.Note{DeckID: deckID, Kind: "basic", CreatedBy: store.Ptr(authorID)}
	fields := map[string]any{"front": "![](/media/" + sha + ")", "back": "x"}
	if _, err := store.NewNoteStore(env.db).Save(context.Background(), &n, fields); err != nil {
		t.Fatalf("save referencing note: %v", err)
	}
	return &n
}

// mediaPageBody 是 GET /api/v1/media 的响应形态。
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

// walkMediaPages 反复翻页直到 next_cursor 为空，返回全量 sha 顺序与页数。
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

// TestListMediaEndpointScopeShapeAndPagination 覆盖 200 形状、scope、分页与可读范围。
func TestListMediaEndpointScopeShapeAndPagination(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	owner := seedUser(t, env.db, "media_list_owner", store.RoleUser)
	other := seedUser(t, env.db, "media_list_other", store.RoleUser)
	router := env.router()

	// owner 上传 3 份媒体。
	owned := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		owned = append(owned, seedMedia(t, env, owner.ID, fmt.Sprintf("owned-%d", i)))
	}
	// 他人 private 卡组里的媒体：owner 不得看到。
	privateDeck := seedDeck(t, env.db, other.ID)
	secret := seedMedia(t, env, other.ID, "secret-media")
	seedRefNote(t, env, privateDeck.ID, other.ID, secret)

	// owner 自己的一张卡引用了 owned[0]：这份字节两路命中（上传 + 被可见卡引用），
	// 列表必须去重成一份。
	ownerDeck := seedDeck(t, env.db, owner.ID)
	seedRefNote(t, env, ownerDeck.ID, owner.ID, owned[0])

	key := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead}, nil)

	// 第一页 limit=2：形状与游标。
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

	// 走完全部分页：不重不漏，且只含 owner 可读的 3 份。
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

	// 单页取全量（limit 足够大）：并集去重后每份字节只出现一次（keyset 的严格比较只在跨页时
	// 挡住同排序键的重复行，一页装得下时才真正验证 UNION 去重）。
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

	// 末页 next_cursor 必须为空串。
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

	// scope：只持 write 的 key 被拒，稳定 code scope_required。
	writeKey := seedKey(t, env.keys, owner.ID, []string{store.ScopeWrite}, nil)
	code, raw = doJSON(t, router, http.MethodGet, "/api/v1/media", writeKey.Plaintext, "")
	if code != http.StatusForbidden {
		t.Fatalf("GET /media with write scope = %d, want 403 (body %s)", code, raw)
	}
	assertErrorCode(t, raw, CodeScopeRequired)

	// 未鉴权：401。
	code, raw = doJSON(t, router, http.MethodGet, "/api/v1/media", "", "")
	if code != http.StatusUnauthorized {
		t.Fatalf("GET /media without a key = %d, want 401 (body %s)", code, raw)
	}
}

// TestListMediaEndpointRejectsInvalidLimitAndCursor 覆盖非法参数：一律 400，不静默截断。
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
