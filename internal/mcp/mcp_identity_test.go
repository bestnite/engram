package mcp

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/store"
)

// The cases in this file cover that rule: /mcp accepts API keys only, and the session identity must be computed afresh on every request.
// To reuse a session number by hand, it sends raw JSON-RPC here (an SDK client cannot specify Mcp-Session-Id).

const rawInitBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"raw","version":"0.0.1"}}}`

// sseData assembles all data: lines from a text/event-stream response body; returns it as-is when it is not SSE.
// The default StreamableHTTPOptions do not enable JSONResponse, so responses are SSE frames.
func sseData(body string) string {
	if !strings.Contains(body, "data:") {
		return body
	}
	var b strings.Builder
	for _, line := range strings.Split(body, "\n") {
		if v, ok := strings.CutPrefix(line, "data:"); ok {
			b.WriteString(strings.TrimSpace(v))
		}
	}
	return b.String()
}

// rawPost sends one raw JSON-RPC request; no Authorization header when key is empty.
func rawPost(t *testing.T, base, key, sessionID, body string) (int, http.Header, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("raw post: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, sseData(string(raw))
}

// rawInitialize performs one handshake and returns the session number.
func rawInitialize(t *testing.T, base, key string) string {
	t.Helper()
	status, hdr, body := rawPost(t, base, key, "", rawInitBody)
	if status != http.StatusOK {
		t.Fatalf("initialize status = %d, want 200; body=%s", status, body)
	}
	sid := hdr.Get("Mcp-Session-Id")
	if sid == "" {
		t.Fatalf("initialize returned no Mcp-Session-Id; body=%s", body)
	}
	// The MCP protocol requires an initialized notification after the handshake, declaring entry into normal operation.
	rawPost(t, base, key, sid, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	return sid
}

// TestMcpRejectsCrossUserSessionHijack covers rule 2: when B uses their own valid key with the session number A obtained in the handshake,
// it must be refused, and must not return any of A's data nor leave any write behind.
func TestMcpRejectsCrossUserSessionHijack(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	a := seedUser(t, db, "alice")
	b := seedUser(t, db, "bob")
	deckA := seedDeck(t, db, a.ID)
	keyA := newKey(t, keys, a.ID, []string{store.ScopeRead, store.ScopeWrite})
	keyB := newKey(t, keys, b.ID, []string{store.ScopeRead})

	sid := rawInitialize(t, ts.URL, keyA)

	status, _, body := rawPost(t, ts.URL, keyB, sid, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if status == http.StatusOK {
		t.Fatalf("cross-user tools/list succeeded (status=%d), want rejection; raw=%s", status, body)
	}
	if strings.Contains(body, "create_deck") || strings.Contains(body, "list_decks") {
		t.Fatalf("cross-user tools/list leaked A's tool list; raw=%s", body)
	}

	status, _, body = rawPost(t, ts.URL, keyB, sid, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_decks","arguments":{}}}`)
	if status == http.StatusOK {
		t.Fatalf("cross-user list_decks succeeded (status=%d), want rejection; raw=%s", status, body)
	}
	if strings.Contains(body, deckA.Name) {
		t.Fatalf("cross-user list_decks leaked A's deck; raw=%s", body)
	}

	status, _, body = rawPost(t, ts.URL, keyB, sid, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"create_deck","arguments":{"name":"hijacked"}}}`)
	if status == http.StatusOK {
		t.Fatalf("cross-user create_deck succeeded (status=%d), want rejection; raw=%s", status, body)
	}
	var n int64
	if err := db.Model(&store.Deck{}).Where("owner_user_id = ? AND name = ?", a.ID, "hijacked").Count(&n).Error; err != nil {
		t.Fatalf("count decks: %v", err)
	}
	if n != 0 {
		t.Fatalf("cross-user create_deck wrote %d deck(s) under A, want 0", n)
	}
}

// TestMcpScopeFollowsCurrentRequestKey covers rule 3: when a low-scope key of the same user reuses a high-scope
// session, tool visibility and permissions must be computed from this request's key, with no privilege escalation.
func TestMcpScopeFollowsCurrentRequestKey(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	a := seedUser(t, db, "alice")
	high := newKey(t, keys, a.ID, []string{store.ScopeRead, store.ScopeWrite})
	low := newKey(t, keys, a.ID, []string{store.ScopeRead})

	sid := rawInitialize(t, ts.URL, high)

	status, _, body := rawPost(t, ts.URL, low, sid, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if status != http.StatusOK {
		t.Fatalf("tools/list with the user's own low-scope key status = %d, want 200; raw=%s", status, body)
	}
	if strings.Contains(body, "create_deck") {
		t.Fatalf("low-scope key saw write tools on a high-scope session; raw=%s", body)
	}
	if !strings.Contains(body, "list_decks") {
		t.Fatalf("low-scope key lost its read tools; raw=%s", body)
	}

	status, _, body = rawPost(t, ts.URL, low, sid, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"create_deck","arguments":{"name":"escalated"}}}`)
	if status != http.StatusOK {
		t.Fatalf("hard-called create_deck status = %d, want 200 with a tool error; raw=%s", status, body)
	}
	if !strings.Contains(body, "scope_required") {
		t.Fatalf("low-scope key hard-called create_deck did not get scope_required; raw=%s", body)
	}
	var n int64
	if err := db.Model(&store.Deck{}).Where("owner_user_id = ? AND name = ?", a.ID, "escalated").Count(&n).Error; err != nil {
		t.Fatalf("count decks: %v", err)
	}
	if n != 0 {
		t.Fatalf("low-scope key created %d deck(s), want 0", n)
	}
}

// TestMcpRejectsSessionCookieChannel covers rule 1: a session channel request to /mcp with only a user identity
// and no API key must be refused (401).
func TestMcpRejectsSessionCookieChannel(t *testing.T) {
	apiSrv, db, _, _ := newEnv(t)
	u := seedUser(t, db, "cookie-user")
	mcpSrv, err := New(Deps{API: apiSrv, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/mcp", func(c *gin.Context) {
		// Reproduces web's session channel: the identity has only a user, no API key.
		ctx := WithIdentity(c.Request.Context(), u, nil)
		mcpSrv.ServeHTTP(c.Writer, c.Request.WithContext(ctx))
	})
	ts := httptest.NewServer(router)
	t.Cleanup(ts.Close)

	status, _, body := rawPost(t, ts.URL, "", "", rawInitBody)
	if status != http.StatusUnauthorized {
		t.Fatalf("session-channel /mcp status = %d, want 401; raw=%s", status, body)
	}
}

// TestMcpOwnKeyFullFlow covers the happy path: A goes through initialize → tools/list → tools/call with their own key.
func TestMcpOwnKeyFullFlow(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	a := seedUser(t, db, "flow")
	seedDeck(t, db, a.ID)
	key := newKey(t, keys, a.ID, []string{store.ScopeRead, store.ScopeWrite})
	cs := connect(t, ts.URL, key)

	names := toolNames(t, cs)
	for _, want := range []string{"list_decks", "create_deck", "search_notes"} {
		if !contains(names, want) {
			t.Fatalf("own key tools/list = %v, missing %s", names, want)
		}
	}

	out, isErr, text := callTool(t, cs, "list_decks", map[string]any{})
	if isErr {
		t.Fatalf("list_decks failed: %s", text)
	}
	decks, _ := out["decks"].([]any)
	if len(decks) == 0 {
		t.Fatalf("list_decks returned no decks: %#v", out)
	}

	if _, isErr, text = callTool(t, cs, "create_deck", map[string]any{"name": "flow-created"}); isErr {
		t.Fatalf("create_deck failed: %s", text)
	}
	var n int64
	if err := db.Model(&store.Deck{}).Where("owner_user_id = ? AND name = ?", a.ID, "flow-created").Count(&n).Error; err != nil {
		t.Fatalf("count decks: %v", err)
	}
	if n != 1 {
		t.Fatalf("own key created %d decks named flow-created, want 1", n)
	}
}

// TestMcpUnknownSessionKeepsSDKBehavior regression: an unknown session number still returns 404 per the SDK's existing behavior.
func TestMcpUnknownSessionKeepsSDKBehavior(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	a := seedUser(t, db, "solo")
	key := newKey(t, keys, a.ID, []string{store.ScopeRead})

	status, _, body := rawPost(t, ts.URL, key, "no-such-session", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if status != http.StatusNotFound {
		t.Fatalf("unknown session status = %d, want 404; raw=%s", status, body)
	}
}

// TestMcpRevokedCredentialFailsOnNextRequest confirms revocation takes effect immediately: once the key is revoked or the account disabled,
// the next /mcp request is 401 at once (the auth middleware looks up the key and account state on every request, with no caching).
func TestMcpRevokedCredentialFailsOnNextRequest(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	a := seedUser(t, db, "revoker")
	ctx := context.Background()

	created, err := keys.Create(ctx, store.CreateAPIKeyParams{UserID: a.ID, Name: "t", Scopes: []string{store.ScopeRead}})
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	sid := rawInitialize(t, ts.URL, created.Plaintext)

	if err := keys.RevokeByID(ctx, created.Key.ID, time.Now().UTC()); err != nil {
		t.Fatalf("RevokeByID: %v", err)
	}
	status, _, body := rawPost(t, ts.URL, created.Plaintext, sid, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if status != http.StatusUnauthorized {
		t.Fatalf("revoked key status = %d, want 401; raw=%s", status, body)
	}

	// Swap in a new key for one handshake, then disable the account.
	created2, err := keys.Create(ctx, store.CreateAPIKeyParams{UserID: a.ID, Name: "t2", Scopes: []string{store.ScopeRead}})
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	sid2 := rawInitialize(t, ts.URL, created2.Plaintext)
	if err := store.NewUserStore(db).SetStatus(ctx, a.ID, store.StatusDisabled); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	status, _, body = rawPost(t, ts.URL, created2.Plaintext, sid2, `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`)
	if status != http.StatusUnauthorized {
		t.Fatalf("disabled-account key status = %d, want 401; raw=%s", status, body)
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
