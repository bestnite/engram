package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// create_import_upload 的验收：MCP 签发的票据走与 REST 相同的 service 方法，
// 包字节随后不带任何凭据直接 PUT 到票据 URL，不经过工具参数。

func TestCreateImportUploadThenPut(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "mcp_upload")
	key := newKey(t, keys, u.ID, []string{store.ScopeRead, store.ScopeWrite})
	cs := connect(t, ts.URL, key)

	out, isErr, text := callTool(t, cs, "create_import_upload", map[string]any{"target": "new_deck"})
	if isErr {
		t.Fatalf("create_import_upload error: %s", text)
	}
	uploadURL, _ := out["upload_url"].(string)
	if out["method"] != http.MethodPut || !strings.HasPrefix(uploadURL, "/api/v1/decks/import-uploads/") {
		t.Fatalf("create_import_upload = %v, want a PUT upload URL", out)
	}

	req, err := http.NewRequest(http.MethodPut, ts.URL+uploadURL, bytes.NewReader(mcpMinimalPackageZip(t)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT upload: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT upload = %d, want 200 (body %s)", resp.StatusCode, raw)
	}
	var report store.PackageImportReport
	if err := json.Unmarshal(raw, &report); err != nil || report.NotesCreated != 1 {
		t.Fatalf("report = %s (err %v), want one note created", raw, err)
	}
}

func TestCreateImportUploadRequiresWriteScope(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "mcp_upload_ro")
	key := newKey(t, keys, u.ID, []string{store.ScopeRead})
	cs := connect(t, ts.URL, key)

	_, isErr, text := callTool(t, cs, "create_import_upload", map[string]any{})
	if !isErr || !strings.Contains(text, "scope_required") {
		t.Fatalf("read-only create_import_upload = (isErr=%v, %q), want scope_required", isErr, text)
	}
	var n int64
	db.Model(&store.ActionToken{}).Count(&n)
	if n != 0 {
		t.Fatalf("rejected issuance left %d tickets, want 0", n)
	}
}
