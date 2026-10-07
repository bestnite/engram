package web

import (
	"net/http"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// cardIDOfNote 取 note 下第一张 card 的 id；SPA 的 JSON 请求与卡面渲染测试都用它定位卡片。
func cardIDOfNote(t *testing.T, db *gorm.DB, noteID uint64) uint64 {
	t.Helper()
	var c store.Card
	if err := db.Where("note_id = ?", noteID).First(&c).Error; err != nil {
		t.Fatalf("load card for note %d: %v", noteID, err)
	}
	return c.ID
}

// TestReviewScopeRejectsUnreadableDeck 覆盖负例：范围里出现用户读不到的卡组时整次请求失败，
// 不得静默丢弃该卡组后继续（由 loadDeckForRole 写出 403/404）。
func TestReviewScopeRejectsUnreadableDeck(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	// 属于另一个用户的私有卡组；当前登录用户既非 owner 也无授权。
	foreign := seedReviewDeck(t, db, ownerID+1, "Foreign deck")

	rec := getWithCookies(t, srv, "/review?deck="+u64str(foreign.ID), cookies)
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Fatalf("GET /review with an unreadable deck status = %d, want 403 or 404 (body %s)",
			rec.Code, snippet(rec.Body.String()))
	}
}

// TestReviewScopeRejectsMixedUnreadableDeck 覆盖混合集合负例：一个自己可读的卡组和一个他人的
// 私有卡组同时出现时，整次请求失败，不得静默丢弃无权限的那个。
// 与 REST 的 TestDueCardsFailsWholeRequestForUnreadableDeck 是同一口径。
func TestReviewScopeRejectsMixedUnreadableDeck(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	readable := seedReviewDeck(t, db, ownerID, "Mine")
	foreign := seedReviewDeck(t, db, ownerID+1, "Foreign deck")
	seedBasic(t, db, readable.ID, "Front", "Back")

	rec := getWithCookies(t, srv,
		"/review?deck="+u64str(readable.ID)+"&deck="+u64str(foreign.ID), cookies)
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Fatalf("GET /review with a readable+foreign deck status = %d, want 403 or 404 (body %s)",
			rec.Code, snippet(rec.Body.String()))
	}
}
