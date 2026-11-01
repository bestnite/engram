package mcp

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// TestMcpEvictedSessionIsRefused covers the cap-eviction case: when the owner table drops a session that the SDK
// still holds, nobody may keep using it. Before the fix an unregistered session id was passed through, so the SDK
// served the handshake identity and a second account presenting the id read the first account's decks.
func TestMcpEvictedSessionIsRefused(t *testing.T) {
	_, db, keys, srv, ts := newEnvWithServer(t)
	a := seedUser(t, db, "evict_owner")
	b := seedUser(t, db, "evict_other")
	deckA := seedDeck(t, db, a.ID)
	keyA := newKey(t, keys, a.ID, []string{store.ScopeRead})
	keyB := newKey(t, keys, b.ID, []string{store.ScopeRead})

	sid := rawInitialize(t, ts.URL, keyA)

	// Age A's record to the oldest slot and fill the table past its cap, so the real eviction path drops it
	// while the SDK still holds the session object.
	srv.mu.Lock()
	entry := srv.sessions[sid]
	entry.seenAt = time.Unix(0, 0)
	srv.sessions[sid] = entry
	srv.mu.Unlock()
	for i := 0; i < mcpSessionCap; i++ {
		srv.rememberSession(fmt.Sprintf("filler-%d", i), a.ID)
	}
	if sessionRemembered(srv, sid) {
		t.Fatalf("session %q was not evicted after the owner table passed its cap", sid)
	}

	// The other account presenting the evicted session id gets nothing.
	status, _, body := rawPost(t, ts.URL, keyB, sid, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_decks","arguments":{}}}`)
	if status == http.StatusOK {
		t.Fatalf("evicted session served another account (status=%d); raw=%s", status, body)
	}
	if strings.Contains(body, deckA.Name) {
		t.Fatalf("evicted session leaked the owner's deck; raw=%s", body)
	}

	// The owner is refused as well: the session no longer exists for anyone, so the client must initialize again.
	status, _, body = rawPost(t, ts.URL, keyA, sid, `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`)
	if status != http.StatusNotFound {
		t.Fatalf("owner using its evicted session status = %d, want 404; raw=%s", status, body)
	}

	// A revoked key is refused at once even while its session is still registered.
	created, err := keys.Create(context.Background(), store.CreateAPIKeyParams{UserID: a.ID, Name: "revoked", Scopes: []string{store.ScopeRead}})
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	sid2 := rawInitialize(t, ts.URL, created.Plaintext)
	if !sessionRemembered(srv, sid2) {
		t.Fatalf("session %q not registered before revocation", sid2)
	}
	if err := keys.RevokeByID(context.Background(), created.Key.ID, time.Now().UTC()); err != nil {
		t.Fatalf("RevokeByID: %v", err)
	}
	status, _, body = rawPost(t, ts.URL, created.Plaintext, sid2, `{"jsonrpc":"2.0","id":4,"method":"tools/list"}`)
	if status != http.StatusUnauthorized {
		t.Fatalf("revoked key with a live session status = %d, want 401; raw=%s", status, body)
	}
}
