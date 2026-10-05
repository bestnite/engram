package mcp

import (
	"context"
	"strings"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// 本文件覆盖 MCP 入口的 L2 越权注入：update_note 与 REST 共用 api.UpdateNote，写前校验同样
// 必须拒绝把别人媒体的引用注入自己可编辑的 note。MCP 是独立入口（工具包壳不同），故单列一条用例。

// seedMCPMedia 落盘一份媒体并返回 sha256（登记上传者归属）。
func seedMCPMedia(t *testing.T, db *gorm.DB, ownerID uint64) string {
	t.Helper()
	raw := append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, []byte("mcp-injection")...)
	m, err := store.NewMediaStore(db).SaveBytes(context.Background(), t.TempDir(), "image/png", raw, store.Ptr(ownerID))
	if err != nil {
		t.Fatalf("SaveBytes: %v", err)
	}
	return m.Sha256
}

// TestMCPUpdateNoteRejectsUnreadableMediaInjection 覆盖 MCP `update_note` 的写前校验。
func TestMCPUpdateNoteRejectsUnreadableMediaInjection(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	owner := seedUser(t, db, "mcpmed_owner")
	attacker := seedUser(t, db, "mcpmed_editor")
	deck := seedDeck(t, db, owner.ID)
	if err := store.NewGrantStore(db).Grant(context.Background(), deck.ID, attacker.ID, store.RoleEditor, store.Ptr(owner.ID)); err != nil {
		t.Fatalf("grant editor: %v", err)
	}
	sha := seedMCPMedia(t, db, owner.ID)
	note := store.Note{DeckID: deck.ID, Kind: "basic", CreatedBy: store.Ptr(owner.ID)}
	if _, err := store.NewNoteStore(db).Save(context.Background(), &note, map[string]any{"front": "plain", "back": "x"}); err != nil {
		t.Fatalf("save note: %v", err)
	}

	key := newKey(t, keys, attacker.ID, []string{store.ScopeWrite})
	cs := connect(t, ts.URL, key)
	_, isErr, text := callTool(t, cs, "update_note", map[string]any{
		"note_id": note.ID,
		"fields":  map[string]any{"front": "![](/media/" + sha + ")", "back": "x"},
	})
	if !isErr {
		t.Fatalf("MCP update_note accepted an unreadable media reference (text %q)", text)
	}
	if !strings.Contains(text, store.CodeMediaNotReadable) {
		t.Errorf("MCP error text = %q, want it to mention %q", text, store.CodeMediaNotReadable)
	}

	reloaded, err := store.NewNoteStore(db).ByID(context.Background(), note.ID)
	if err != nil {
		t.Fatalf("reload note: %v", err)
	}
	if strings.Contains(reloaded.FieldsJSON, sha) {
		t.Errorf("rejected MCP update wrote the reference: %s", reloaded.FieldsJSON)
	}
	var mapping int64
	if err := db.Model(&store.MediaNote{}).Where("media_sha = ? AND note_id = ?", sha, note.ID).Count(&mapping).Error; err != nil {
		t.Fatalf("count media_notes: %v", err)
	}
	if mapping != 0 {
		t.Errorf("rejected MCP update left %d media_notes rows, want 0", mapping)
	}
}
