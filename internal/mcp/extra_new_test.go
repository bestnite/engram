package mcp

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/store"
)

// TestAddExtraNewCardsMatchesREST 断言 add_extra_new_cards 与 REST POST /decks/:id/extra-new
// 走同一个 service 方法：MCP 追加后的结果与随后 REST GET 读到的状态一致；缺省 count 用每日上限。
func TestAddExtraNewCardsMatchesREST(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	owner := seedUser(t, db, "extra-mcp-owner")
	deck := seedDeck(t, db, owner.ID)
	if err := store.NewDeckStore(db).SetCaps(context.Background(), owner.ID, deck.ID, store.DeckCaps{NewPerDay: 3}); err != nil {
		t.Fatalf("SetCaps() error = %v", err)
	}
	key := newKey(t, keys, owner.ID, []string{store.ScopeReview})
	cs := connect(t, ts.URL, key)

	out, isErr, text := callTool(t, cs, "add_extra_new_cards", map[string]any{"deck_id": deck.PublicID, "count": 2})
	if isErr {
		t.Fatalf("add_extra_new_cards error: %s", text)
	}
	status, restOut := rest(t, ts.URL, http.MethodGet, "/api/v1/decks/"+deck.PublicID+"/extra-new", key, "")
	if status != http.StatusOK {
		t.Fatalf("REST GET extra-new status = %d (%v)", status, restOut)
	}
	if !reflect.DeepEqual(out, restOut) {
		t.Errorf("add_extra_new_cards MCP=%v REST=%v", out, restOut)
	}
	if out["extra_new"] != float64(2) {
		t.Errorf("extra_new = %v, want 2", out["extra_new"])
	}

	// 缺省 count：再追加每日上限 3 张，累加到 5。
	out, isErr, text = callTool(t, cs, "add_extra_new_cards", map[string]any{"deck_id": deck.PublicID})
	if isErr {
		t.Fatalf("add_extra_new_cards (default) error: %s", text)
	}
	if out["extra_new"] != float64(5) {
		t.Errorf("extra_new after default = %v, want 5", out["extra_new"])
	}
}

// TestAddExtraNewCardsNegatives 覆盖负例：非成员 forbidden、不限额卡组与越界 count 返回 invalid_request，
// 只有 read scope 的 key 按名字硬调返回 scope_required。
func TestAddExtraNewCardsNegatives(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	owner := seedUser(t, db, "extra-mcp-neg-owner")
	stranger := seedUser(t, db, "extra-mcp-neg-stranger")
	deck := seedDeck(t, db, owner.ID)
	unlimited := seedDeck(t, db, owner.ID)
	if err := store.NewDeckStore(db).SetCaps(context.Background(), owner.ID, unlimited.ID, store.DeckCaps{}); err != nil {
		t.Fatalf("SetCaps() error = %v", err)
	}
	ownerCS := connect(t, ts.URL, newKey(t, keys, owner.ID, []string{store.ScopeReview}))
	strangerCS := connect(t, ts.URL, newKey(t, keys, stranger.ID, []string{store.ScopeReview}))
	readCS := connect(t, ts.URL, newKey(t, keys, owner.ID, []string{store.ScopeRead}))

	cases := []struct {
		name     string
		cs       *sdkmcp.ClientSession
		args     map[string]any
		wantCode string
	}{
		{"stranger", strangerCS, map[string]any{"deck_id": deck.PublicID}, api.CodeForbidden},
		{"unlimited deck", ownerCS, map[string]any{"deck_id": unlimited.PublicID}, api.CodeInvalidRequest},
		{"count over max", ownerCS, map[string]any{"deck_id": deck.PublicID, "count": 1001}, api.CodeInvalidRequest},
		{"read-only key", readCS, map[string]any{"deck_id": deck.PublicID}, "scope_required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, isErr, text := callTool(t, tc.cs, "add_extra_new_cards", tc.args)
			if !isErr || !strings.Contains(text, tc.wantCode) {
				t.Errorf("isErr=%v text=%q, want an error mentioning %s", isErr, text, tc.wantCode)
			}
		})
	}
}
