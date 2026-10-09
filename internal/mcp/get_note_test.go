package mcp

import (
	"context"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// TestGetNoteToolReadsByPublicID 断言 get_note 按对外 id 读取单条卡片，
// 未知 id 返回 not_found；与 REST `GET /notes/:id` 同源（同一 service 方法）。
func TestGetNoteToolReadsByPublicID(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "note_reader")
	deck := seedDeck(t, db, u.ID)
	note := &store.Note{DeckID: deck.ID, Kind: "basic", CreatedBy: store.Ptr(u.ID)}
	if _, err := store.NewNoteStore(db).Create(context.Background(), note,
		map[string]any{"front": "q", "back": "a"}); err != nil {
		t.Fatalf("create note: %v", err)
	}
	cs := connect(t, ts.URL, newKey(t, keys, u.ID, []string{store.ScopeRead}))

	out, isErr, text := callTool(t, cs, "get_note", map[string]any{"note_id": note.PublicID})
	if isErr {
		t.Fatalf("get_note returned error: %s", text)
	}
	if out["id"] != note.PublicID {
		t.Errorf("get_note id = %v, want %q", out["id"], note.PublicID)
	}
	if out["deck_id"] != deck.PublicID {
		t.Errorf("get_note deck_id = %v, want %q", out["deck_id"], deck.PublicID)
	}

	_, isErr, text = callTool(t, cs, "get_note", map[string]any{"note_id": "missing-note"})
	if !isErr || !strings.Contains(text, "not_found") {
		t.Fatalf("get_note unknown id = (isErr %v, text %q), want a not_found error", isErr, text)
	}
}
