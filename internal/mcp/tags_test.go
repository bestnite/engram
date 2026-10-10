package mcp

import (
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/store"
)

// seedTaggedCardMCP 建一个带标签的 note 与它的一张 card（新卡），返回 note 的对外 id。
func seedTaggedCardMCP(t *testing.T, db *gorm.DB, deckID uint64, tags ...string) string {
	t.Helper()
	now := time.Now().UTC()
	n := store.Note{DeckID: deckID, Kind: "basic", FieldsJSON: `{"front":"q","back":"a"}`,
		TagsJSON: store.TagsJSON(tags), CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&n).Error; err != nil {
		t.Fatalf("create note: %v", err)
	}
	c := store.Card{NoteID: n.ID, Template: "forward", Ordinal: 0, CreatedAt: now}
	if err := db.Create(&c).Error; err != nil {
		t.Fatalf("create card: %v", err)
	}
	return n.PublicID
}

// TestGetDueCardsTagsMatchesREST 断言 get_due_cards 的 tags 与 REST 的重复 tag 参数是同一口径，
// 且带标签却跨多个卡组时返回 invalid_request。
func TestGetDueCardsTagsMatchesREST(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "tag-mcp")
	deck := seedDeck(t, db, u.ID)
	other := seedDeck(t, db, u.ID)
	culture := seedTaggedCardMCP(t, db, deck.ID, "文化传承")
	seedTaggedCardMCP(t, db, deck.ID, "创新")
	seedTaggedCardMCP(t, db, other.ID, "文化传承")
	key := newKey(t, keys, u.ID, []string{store.ScopeRead, store.ScopeReview})
	cs := connect(t, ts.URL, key)

	mcpOut, isErr, text := callTool(t, cs, "get_due_cards", map[string]any{
		"deck_id": deck.PublicID, "tags": []any{"文化传承"}, "limit": 50,
	})
	if isErr {
		t.Fatalf("get_due_cards(tags) error: %s", text)
	}
	q := url.Values{"deck": {deck.PublicID}, "tag": {"文化传承"}, "limit": {"50"}}
	_, restOut := rest(t, ts.URL, http.MethodGet, "/api/v1/review/due?"+q.Encode(), key, "")
	if !reflect.DeepEqual(normalizeCards(mcpOut), normalizeCards(restOut)) {
		t.Errorf("get_due_cards(tags) MCP=%v REST=%v", normalizeCards(mcpOut), normalizeCards(restOut))
	}
	cards, _ := mcpOut["cards"].([]any)
	if len(cards) != 1 || cards[0].(map[string]any)["note_id"] != culture {
		t.Fatalf("get_due_cards(tags) cards = %v, want only note %s", cards, culture)
	}

	for _, args := range []map[string]any{
		{"tags": []any{"文化传承"}},
		{"deck_ids": []any{deck.PublicID, other.PublicID}, "tags": []any{"文化传承"}},
	} {
		_, isErr, text := callTool(t, cs, "get_due_cards", args)
		if !isErr {
			t.Errorf("get_due_cards(%v) succeeded, want invalid_request", args)
			continue
		}
		if !strings.Contains(text, api.CodeInvalidRequest) {
			t.Errorf("get_due_cards(%v) error text = %q, want code %s", args, text, api.CodeInvalidRequest)
		}
	}
}

// TestListDeckTagsMatchesREST 断言 list_deck_tags 与 REST GET /decks/:id/tags 同源；
// 别人的私有卡组整次失败。
func TestListDeckTagsMatchesREST(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	owner := seedUser(t, db, "tag-list-owner")
	stranger := seedUser(t, db, "tag-list-stranger")
	deck := seedDeck(t, db, owner.ID)
	seedTaggedCardMCP(t, db, deck.ID, "创新", "文化传承")
	seedTaggedCardMCP(t, db, deck.ID, "创新")
	key := newKey(t, keys, owner.ID, []string{store.ScopeRead})
	cs := connect(t, ts.URL, key)

	mcpOut, isErr, text := callTool(t, cs, "list_deck_tags", map[string]any{"deck_id": deck.PublicID})
	if isErr {
		t.Fatalf("list_deck_tags error: %s", text)
	}
	_, restOut := rest(t, ts.URL, http.MethodGet, "/api/v1/decks/"+deck.PublicID+"/tags", key, "")
	if !reflect.DeepEqual(mcpOut, restOut) {
		t.Errorf("list_deck_tags MCP=%v REST=%v", mcpOut, restOut)
	}
	tags, _ := mcpOut["tags"].([]any)
	if len(tags) != 2 {
		t.Fatalf("list_deck_tags tags = %v, want 2 entries", tags)
	}

	sc := connect(t, ts.URL, newKey(t, keys, stranger.ID, []string{store.ScopeRead}))
	_, isErr, text = callTool(t, sc, "list_deck_tags", map[string]any{"deck_id": deck.PublicID})
	if !isErr || !strings.Contains(text, api.CodeForbidden) {
		t.Errorf("stranger list_deck_tags isErr=%v text=%q, want %s", isErr, text, api.CodeForbidden)
	}
}
