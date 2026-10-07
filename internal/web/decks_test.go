package web

import (
	"net/http"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// TestDeckListRedirectsAnonymousToLogin 覆盖匿名访问被重定向到登录页的验收点。
func TestDeckListRedirectsAnonymousToLogin(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	rec := getWithCookies(t, srv, "/decks", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("GET /decks (anonymous) status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Fatalf("GET /decks (anonymous) Location = %q, want /login", loc)
	}
}

// seedCardRow 建一张 basic note + forward card，返回 card id（队列计数夹具用，不渲染卡片内容）。
// 供 decks 与 card 设置相关的多个测试文件共用。
func seedCardRow(t *testing.T, db *gorm.DB, deckID uint64, front string, createdAt time.Time) uint64 {
	t.Helper()
	note := store.Note{DeckID: deckID, Kind: "basic",
		FieldsJSON: `{"front":"` + front + `","back":"b"}`, TagsJSON: "[]",
		CreatedAt: createdAt, UpdatedAt: createdAt}
	if err := db.Create(&note).Error; err != nil {
		t.Fatalf("create note: %v", err)
	}
	card := store.Card{NoteID: note.ID, Template: "forward", CreatedAt: createdAt}
	if err := db.Create(&card).Error; err != nil {
		t.Fatalf("create card: %v", err)
	}
	return card.ID
}
