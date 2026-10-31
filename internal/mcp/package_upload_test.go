package mcp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

func TestImportToolsPresetSelection(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "mcp_preset_modes")
	key := newKey(t, keys, u.ID, []string{store.ScopeRead, store.ScopeWrite})
	cs := connect(t, ts.URL, key)
	p := store.NewPreset(u.ID, "Chosen")
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	raw := mcpMinimalPackageZip(t)
	out, isErr, text := callTool(t, cs, "import_deck", map[string]any{"package": base64.StdEncoding.EncodeToString(raw), "preset_mode": "reuse", "preset_id": p.PublicID})
	if isErr || out["preset_mode"] != "reuse" || out["preset_name"] != p.Name {
		t.Fatalf("import: %v %v %s", out, isErr, text)
	}
	deck, err := store.NewDeckStore(db).ByPublicID(t.Context(), out["deck_id"].(string))
	if err != nil || deck.PresetID != p.ID {
		t.Fatalf("deck %v err %v", deck, err)
	}
	out, isErr, text = callTool(t, cs, "create_import_upload", map[string]any{"preset_mode": "copy", "preset_id": p.PublicID})
	if isErr {
		t.Fatal(text)
	}
	req, err := http.NewRequest(http.MethodPut, ts.URL+out["upload_url"].(string), bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var report store.PackageImportReport
	if err := json.Unmarshal(body, &report); err != nil || resp.StatusCode != http.StatusOK || report.PresetMode != "copy" || report.PresetName != "Chosen (copy)" {
		t.Fatalf("upload %d %s %v", resp.StatusCode, body, err)
	}
	deck, err = store.NewDeckStore(db).ByPublicID(t.Context(), report.DeckPublicID)
	if err != nil || deck.PresetID == p.ID {
		t.Fatalf("deck %v err %v", deck, err)
	}
}

// Acceptance for create_import_upload: the ticket MCP issues goes through the same service method as REST,
// and the package bytes are then PUT straight to the ticket URL with no credentials at all, never passing through tool arguments.

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
