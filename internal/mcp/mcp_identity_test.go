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

// 本文件的用例覆盖这条规则：/mcp 只接受 API key，且会话身份必须是每次请求现算的。
// 为手工复用会话号，这里发原始 JSON-RPC（SDK 客户端无法指定 Mcp-Session-Id）。

const rawInitBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"raw","version":"0.0.1"}}}`

// sseData 从 text/event-stream 响应体里拼出所有 data: 行；非 SSE 时原样返回。
// 默认 StreamableHTTPOptions 不启用 JSONResponse，因此响应是 SSE 帧。
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

// rawPost 发一条原始 JSON-RPC 请求；key 为空时不带 Authorization 头。
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

// rawInitialize 走一次握手并返回会话号。
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
	// MCP 协议要求握手后再发 initialized 通知，声明进入正常操作阶段。
	rawPost(t, base, key, sid, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	return sid
}

// TestMcpRejectsCrossUserSessionHijack 覆盖规则 2：B 用自己的有效 key 复用 A 握手得到的
// 会话号，必须被拒，且不得返回 A 的任何数据、不得落下任何写入。
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

// TestMcpScopeFollowsCurrentRequestKey 覆盖规则 3：同一用户的低 scope key 复用高 scope
// 会话，工具可见性与权限必须按本次 key 计算，不得提权。
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

// TestMcpRejectsSessionCookieChannel 覆盖规则 1：仅有用户身份、没有 API key 的会话通道
// 请求 /mcp 必须被拒（401）。
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
		// 复刻 web 的会话通道：身份里只有用户、没有 API key。
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

// TestMcpOwnKeyFullFlow 覆盖正向路径：A 用自己的 key 走完 initialize → tools/list → tools/call。
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

// TestMcpUnknownSessionKeepsSDKBehavior 回归：不存在的会话号仍按 SDK 原有行为返回 404。
func TestMcpUnknownSessionKeepsSDKBehavior(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	a := seedUser(t, db, "solo")
	key := newKey(t, keys, a.ID, []string{store.ScopeRead})

	status, _, body := rawPost(t, ts.URL, key, "no-such-session", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if status != http.StatusNotFound {
		t.Fatalf("unknown session status = %d, want 404; raw=%s", status, body)
	}
}

// TestMcpRevokedCredentialFailsOnNextRequest 确认撤销即时生效：key 被撤销、或账号被禁用后，
// 下一个 /mcp 请求立刻 401（鉴权中间件每次请求都现查 key 与账号状态，不缓存）。
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

	// 换一把新 key 走一次握手，然后禁用账号。
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
