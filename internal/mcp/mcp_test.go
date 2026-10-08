package mcp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	gorm "gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// ---- 测试装配 ----

// newEnv 装配真实 SQLite + REST + MCP 的同进程 HTTP 服务，复刻 main 的挂载方式。
func newEnv(t *testing.T) (*api.API, *gorm.DB, *store.APIKeyStore, *httptest.Server) {
	t.Helper()
	a, db, keys, _, ts := newEnvWithServer(t)
	return a, db, keys, ts
}

// newEnvWithServer 同 newEnv，另外返回 MCP server 本体，供需要检查内部状态的用例
// （如会话属主表）使用。
func newEnvWithServer(t *testing.T) (*api.API, *gorm.DB, *store.APIKeyStore, *Server, *httptest.Server) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "mcp.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auditor, err := auth.NewAuditor(store.NewAuditStore(db))
	if err != nil {
		t.Fatalf("NewAuditor() error = %v", err)
	}
	keys := store.NewAPIKeyStore(db)
	apiSrv, err := api.New(api.Deps{
		DB: db, Logger: logger, Keys: keys,
		Users: store.NewUserStore(db), Decks: store.NewDeckStore(db), Notes: store.NewNoteStore(db),
		Presets: store.NewPresetStore(db), Cards: store.NewCardStore(db), Auditor: auditor,
		ReadLimit: 100000, WriteLimit: 100000,
	})
	if err != nil {
		t.Fatalf("api.New() error = %v", err)
	}
	mcpSrv, err := New(Deps{API: apiSrv, Logger: logger})
	if err != nil {
		t.Fatalf("mcp.New() error = %v", err)
	}
	router := gin.New()
	apiSrv.Register(router)
	g := router.Group("")
	g.Use(apiSrv.AuthMiddleware())
	endpoint := func(c *gin.Context) {
		u, _ := api.CurrentUser(c)
		k, _ := api.CurrentAPIKey(c)
		mcpSrv.ServeHTTP(c.Writer, c.Request.WithContext(WithIdentity(c.Request.Context(), u, k)))
	}
	g.POST("/mcp", endpoint)
	g.GET("/mcp", endpoint)
	g.DELETE("/mcp", endpoint)
	ts := httptest.NewServer(router)
	t.Cleanup(ts.Close)
	return apiSrv, db, keys, mcpSrv, ts
}

func seedUser(t *testing.T, db *gorm.DB, name string) *store.User {
	t.Helper()
	u := store.User{
		Username: name, Email: name + "@example.com", DisplayName: name,
		Role: store.RoleUser, Status: store.StatusActive, Locale: "en",
		Timezone: "UTC", DayCutoffHour: store.Ptr(4), CreatedAt: time.Now().UTC(),
	}
	if err := db.Create(&u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	return &u
}

func seedDeck(t *testing.T, db *gorm.DB, ownerID uint64) *store.Deck {
	t.Helper()
	preset := store.NewPreset(ownerID, "Default")
	if err := store.NewPresetStore(db).Create(context.Background(), &preset); err != nil {
		t.Fatalf("create preset: %v", err)
	}
	d := store.Deck{OwnerUserID: ownerID, Name: "deck", Visibility: "private", PresetID: preset.ID, CreatedAt: time.Now().UTC()}
	if err := store.NewDeckStore(db).Create(context.Background(), &d); err != nil {
		t.Fatalf("create deck: %v", err)
	}
	return &d
}

func newKey(t *testing.T, keys *store.APIKeyStore, userID uint64, scopes []string) string {
	t.Helper()
	c, err := keys.Create(context.Background(), store.CreateAPIKeyParams{UserID: userID, Name: "test", Scopes: scopes})
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	return c.Plaintext
}

// ---- SDK 客户端 ----

type bearerTransport struct{ key string }

func (bt *bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	rr := r.Clone(r.Context())
	rr.Header.Set("Authorization", "Bearer "+bt.key)
	return http.DefaultTransport.RoundTrip(rr)
}

func connect(t *testing.T, base, key string) *sdkmcp.ClientSession {
	t.Helper()
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	tr := &sdkmcp.StreamableClientTransport{
		Endpoint:             base + "/mcp",
		HTTPClient:           &http.Client{Transport: &bearerTransport{key: key}},
		DisableStandaloneSSE: true,
	}
	cs, err := client.Connect(context.Background(), tr, nil)
	if err != nil {
		t.Fatalf("connect MCP: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func toolNames(t *testing.T, cs *sdkmcp.ClientSession) []string {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := make([]string, 0, len(res.Tools))
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
	}
	sort.Strings(names)
	return names
}

func callTool(t *testing.T, cs *sdkmcp.ClientSession, name string, args any) (map[string]any, bool, string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	var text strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdkmcp.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	if res.IsError {
		return nil, true, text.String()
	}
	out, _ := res.StructuredContent.(map[string]any)
	return out, false, text.String()
}

func rest(t *testing.T, base, method, path, key, body string) (int, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, base+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("rest do: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &m)
	}
	return resp.StatusCode, m
}

// ---- ：scope 过滤 ----

// TestReadOnlyKeySeesNoWriteTools 是核心验收：
// read-only key 在 tools/list 里看不到写/复习工具，按名字硬调返回权限错误。
func TestReadOnlyKeySeesNoWriteTools(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "reader")
	key := newKey(t, keys, u.ID, []string{store.ScopeRead})
	cs := connect(t, ts.URL, key)

	got := toolNames(t, cs)
	want := []string{"export_deck", "get_stats", "list_decks", "search_notes"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("read-only tools/list = %v, want %v", got, want)
	}

	_, isErr, text := callTool(t, cs, "create_notes", map[string]any{"deck_id": 1, "notes": []any{}})
	if !isErr {
		t.Fatalf("hard-calling create_notes with a read-only key must fail")
	}
	if !strings.Contains(text, "scope_required") || !strings.Contains(text, store.ScopeWrite) {
		t.Fatalf("permission error text = %q, want scope_required mentioning %s", text, store.ScopeWrite)
	}
}

// TestWriteAndReviewKeysSeeOwnTools 校验另外两档 scope 的可见集合。
func TestWriteAndReviewKeysSeeOwnTools(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "writer")

	w := connect(t, ts.URL, newKey(t, keys, u.ID, []string{store.ScopeWrite}))
	wantW := []string{"bulk_notes", "create_deck", "create_notes", "delete_note", "import_deck", "update_note"}
	if got := toolNames(t, w); !reflect.DeepEqual(got, wantW) {
		t.Fatalf("write tools/list = %v, want %v", got, wantW)
	}
	if _, err := w.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: "get_due_cards", Arguments: map[string]any{}}); err != nil {
		t.Fatalf("CallTool transport error: %v", err)
	}

	r := connect(t, ts.URL, newKey(t, keys, u.ID, []string{store.ScopeReview}))
	wantR := []string{"get_due_cards", "submit_review"}
	if got := toolNames(t, r); !reflect.DeepEqual(got, wantR) {
		t.Fatalf("review tools/list = %v, want %v", got, wantR)
	}
}

// TestAdminScopeSeesAllTools admin 蕴含其余三档。
func TestAdminScopeSeesAllTools(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "admin")
	cs := connect(t, ts.URL, newKey(t, keys, u.ID, []string{store.ScopeAdmin}))
	if got := len(toolNames(t, cs)); got != 12 {
		t.Fatalf("admin sees %d tools, want 12", got)
	}
}
