package mcp

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/store"
)

// TestDeleteDeckMatchesREST is the core acceptance for delete_deck: the owner deleting through MCP and through
// REST `DELETE /decks/:id` returns the same body and removes the deck row, and the negative cases REST enforces
// (owner only, write scope, unknown deck) hold through the tool with the same error codes.
func TestDeleteDeckMatchesREST(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	owner := seedUser(t, db, "deckowner")
	deck := seedDeck(t, db, owner.ID)
	key := newKey(t, keys, owner.ID, []string{store.ScopeRead, store.ScopeWrite})
	cs := connect(t, ts.URL, key)
	decks := store.NewDeckStore(db)

	// A granted reader can see the deck but is not its owner: the tool answers forbidden and the deck stays.
	reader := seedUser(t, db, "deckreader")
	grant := store.DeckGrant{DeckID: deck.ID, UserID: reader.ID, Role: store.RoleReader, CreatedAt: time.Now().UTC()}
	if err := db.Create(&grant).Error; err != nil {
		t.Fatalf("create grant: %v", err)
	}
	readerCS := connect(t, ts.URL, newKey(t, keys, reader.ID, []string{store.ScopeRead, store.ScopeWrite}))
	_, isErr, text := callTool(t, readerCS, "delete_deck", map[string]any{"deck_id": deck.PublicID})
	if !isErr || !strings.Contains(text, api.CodeForbidden) {
		t.Fatalf("delete_deck by a granted reader isErr=%v text=%q, want %s", isErr, text, api.CodeForbidden)
	}
	if _, err := decks.ByID(context.Background(), deck.ID); err != nil {
		t.Fatalf("deck is unreadable after a refused delete: %v", err)
	}

	// A key without write scope cannot delete even when it calls the tool by name.
	roCS := connect(t, ts.URL, newKey(t, keys, owner.ID, []string{store.ScopeRead}))
	_, isErr, text = callTool(t, roCS, "delete_deck", map[string]any{"deck_id": deck.PublicID})
	if !isErr || !strings.Contains(text, api.CodeScopeRequired) {
		t.Fatalf("delete_deck with a read-only key isErr=%v text=%q, want %s", isErr, text, api.CodeScopeRequired)
	}

	// The owner deletes through MCP: the deck row is gone, and the body matches REST's for the same operation.
	mcpOut, isErr, text := callTool(t, cs, "delete_deck", map[string]any{"deck_id": deck.PublicID})
	if isErr {
		t.Fatalf("delete_deck error: %s", text)
	}
	if _, err := decks.ByID(context.Background(), deck.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("deck row survives the delete: err = %v, want gorm.ErrRecordNotFound", err)
	}
	other := seedDeck(t, db, owner.ID)
	st, restOut := rest(t, ts.URL, http.MethodDelete, "/api/v1/decks/"+other.PublicID, key, "")
	if st != http.StatusOK {
		t.Fatalf("REST delete status = %d body %v", st, restOut)
	}
	if !reflect.DeepEqual(mcpOut, restOut) {
		t.Errorf("delete_deck MCP body = %v, REST body = %v, want the same", mcpOut, restOut)
	}

	// Deleting the same deck twice is not_found, the same code REST returns.
	_, isErr, text = callTool(t, cs, "delete_deck", map[string]any{"deck_id": deck.PublicID})
	if !isErr || !strings.Contains(text, api.CodeNotFound) {
		t.Errorf("repeated delete_deck isErr=%v text=%q, want %s", isErr, text, api.CodeNotFound)
	}
}
