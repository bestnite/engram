package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// TestMcpSessionUsesTheCurrentAccountRow covers a role change after the handshake. The
// session keeps the identity it read at initialize; a tool call must still judge the
// account from its current row, as REST does on every request. A demoted administrator
// must issue an upload ticket with the same allow_others_progress decision on both
// transports.
func TestMcpSessionUsesTheCurrentAccountRow(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "mcp_stale_role")
	if err := db.Model(&store.User{}).Where("id = ?", u.ID).Update("role", store.RoleAdmin).Error; err != nil {
		t.Fatalf("promote user: %v", err)
	}
	key := newKey(t, keys, u.ID, []string{store.ScopeRead, store.ScopeWrite})
	cs := connect(t, ts.URL, key)

	// Demote after the handshake: the session's stored user still carries the admin role.
	if err := db.Model(&store.User{}).Where("id = ?", u.ID).Update("role", store.RoleUser).Error; err != nil {
		t.Fatalf("demote user: %v", err)
	}

	if _, isErr, text := callTool(t, cs, "create_import_upload", map[string]any{
		"target": "new_deck", "allow_others_progress": true,
	}); isErr {
		t.Fatalf("create_import_upload: %s", text)
	}
	mcpAllowed := latestUploadTicketAllowsOthers(t, db)

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/decks/import-uploads",
		bytes.NewBufferString(`{"target":"new_deck","allow_others_progress":true}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("REST ticket request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("REST ticket status = %d, want 201", resp.StatusCode)
	}
	restAllowed := latestUploadTicketAllowsOthers(t, db)

	if mcpAllowed != restAllowed {
		t.Fatalf("MCP ticket allow_others_progress = %v, REST = %v: the session kept the handshake role",
			mcpAllowed, restAllowed)
	}
	if mcpAllowed {
		t.Fatalf("a demoted account issued an allow_others_progress ticket")
	}
}

// latestUploadTicketAllowsOthers decodes the newest upload ticket's payload, which fixes
// the import options at issuance.
func latestUploadTicketAllowsOthers(t *testing.T, db *gorm.DB) bool {
	t.Helper()
	var tok store.ActionToken
	if err := db.Where("purpose = ?", "deck_import_upload").Order("id DESC").First(&tok).Error; err != nil {
		t.Fatalf("load upload ticket: %v", err)
	}
	var payload struct {
		AllowOthersProgress bool `json:"allow_others_progress"`
	}
	if err := json.Unmarshal([]byte(tok.Payload), &payload); err != nil {
		t.Fatalf("decode upload ticket: %v", err)
	}
	return payload.AllowOthersProgress
}
