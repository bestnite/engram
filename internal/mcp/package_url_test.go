package mcp

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/store"
)

// import_deck 的直链来源（url）验收：与 package 互斥，且 url 走的是与 REST
// POST /decks/import-url 相同的 service 方法（下载器用受控实现替换，不碰真实网络）。

// stubPackageFetcher 是受控的直链下载器：返回预设字节并记录调用。
type stubPackageFetcher struct {
	body  []byte
	calls int
	urls  []string
}

func (f *stubPackageFetcher) Fetch(_ context.Context, rawURL string, _ int64) (io.ReadCloser, error) {
	f.calls++
	f.urls = append(f.urls, rawURL)
	return io.NopCloser(bytes.NewReader(f.body)), nil
}

// mcpMinimalPackageZip 构造一个最小合法 .edeck（一条 basic note）。
func mcpMinimalPackageZip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	entries := map[string]string{
		"manifest.json": `{"format_version":1,"exported_at":"2026-10-02T00:00:00Z","deck":{"name":"imported"},"include_progress":false,"include_media":false,"include_reviews":false,"counts":{"notes":1,"cards":1}}`,
		"notes.json":    `[{"kind":"basic","fields":{"front":"q","back":"a"}}]`,
		"cards.json":    `[]`,
		"preset.json":   `{"desired_retention":0.9,"learning_steps":"1m","relearning_steps":"10m","maximum_interval_days":100,"enable_fuzz":true,"weights":null,"weights_optimized_at":null,"weights_review_count":null}`,
	}
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func TestImportDeckURLMutualExclusion(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "url_excl")
	key := newKey(t, keys, u.ID, []string{store.ScopeRead, store.ScopeWrite})
	cs := connect(t, ts.URL, key)

	_, isErr, text := callTool(t, cs, "import_deck", map[string]any{
		"package": map[string]any{"manifest.json": map[string]any{}},
		"url":     "https://example.com/d.edeck",
	})
	if !isErr || !strings.Contains(text, "mutually exclusive") {
		t.Fatalf("import_deck(package+url) = (isErr=%v, %q), want a mutual-exclusion error", isErr, text)
	}

	_, isErr, text = callTool(t, cs, "import_deck", map[string]any{"target": "new_deck"})
	if !isErr || !strings.Contains(text, "required") {
		t.Fatalf("import_deck(no source) = (isErr=%v, %q), want a missing-source error", isErr, text)
	}
}

func TestImportDeckURLDownloadsAndImports(t *testing.T) {
	f := &stubPackageFetcher{body: mcpMinimalPackageZip(t)}
	_, db, keys, _, ts := newEnvWithPackageFetcher(t, f)
	u := seedUser(t, db, "url_import")
	key := newKey(t, keys, u.ID, []string{store.ScopeRead, store.ScopeWrite})
	cs := connect(t, ts.URL, key)

	out, isErr, text := callTool(t, cs, "import_deck", map[string]any{
		"url":     "https://example.com/deck.edeck",
		"target":  "new_deck",
		"dry_run": true,
	})
	if isErr {
		t.Fatalf("import_deck(url) error: %s", text)
	}
	if out["dry_run"] != true || out["notes_created"] != float64(1) {
		t.Errorf("import_deck(url) report = %v, want dry_run=true notes_created=1", out)
	}
	if f.calls != 1 || len(f.urls) != 1 || f.urls[0] != "https://example.com/deck.edeck" {
		t.Errorf("fetcher calls/urls = %d/%v, want one call to the given url", f.calls, f.urls)
	}
}

// TestImportURLRateLimitSharedAcrossRESTAndMCP 证明 REST 与 MCP 的直链导入共用同一条按用户
// 计数的限流池：同一用户交替走两条传输，配额用尽后第 11 次无论走哪条都被拦下，且不再下载。
// 每次调用 newEnvWithPackageFetcher 都是全新 fixture（独立 SQLite 库 + 独立限流器）。
func TestImportURLRateLimitSharedAcrossRESTAndMCP(t *testing.T) {
	f := &stubPackageFetcher{body: mcpMinimalPackageZip(t)}
	_, db, keys, _, ts := newEnvWithPackageFetcher(t, f)
	u := seedUser(t, db, "url_pool")
	key := newKey(t, keys, u.ID, []string{store.ScopeRead, store.ScopeWrite})
	cs := connect(t, ts.URL, key)

	const restCalls, mcpCalls = 6, 4
	restBody := `{"url":"https://example.com/deck.edeck","target":"new_deck","dry_run":true}`

	for i := 0; i < restCalls; i++ {
		if code, _ := rest(t, ts.URL, http.MethodPost, "/api/v1/decks/import-url", key, restBody); code != http.StatusOK {
			t.Fatalf("REST call %d = %d, want 200", i+1, code)
		}
	}
	for i := 0; i < mcpCalls; i++ {
		if _, isErr, text := callTool(t, cs, "import_deck", map[string]any{
			"url": "https://example.com/deck.edeck", "target": "new_deck", "dry_run": true,
		}); isErr {
			t.Fatalf("MCP call %d failed: %s", i+1, text)
		}
	}
	if f.calls != restCalls+mcpCalls {
		t.Fatalf("fetcher calls = %d, want %d after the allowed REST+MCP mix", f.calls, restCalls+mcpCalls)
	}

	// 第 11 次：MCP 与 REST 都必须被同一池拦下，且都不再触发下载。
	if _, isErr, text := callTool(t, cs, "import_deck", map[string]any{
		"url": "https://example.com/deck.edeck", "target": "new_deck", "dry_run": true,
	}); !isErr || !strings.Contains(text, api.CodeRateLimited) {
		t.Fatalf("11th call over MCP = (isErr=%v, %q), want a rate_limited error", isErr, text)
	}
	if code, _ := rest(t, ts.URL, http.MethodPost, "/api/v1/decks/import-url", key, restBody); code != http.StatusTooManyRequests {
		t.Fatalf("11th call over REST = %d, want 429", code)
	}
	if f.calls != restCalls+mcpCalls {
		t.Fatalf("fetcher calls = %d, want %d: a rate-limited request must not download", f.calls, restCalls+mcpCalls)
	}
}
