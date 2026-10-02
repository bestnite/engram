package web

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"gorm.io/gorm"

	"example.com/engram/internal/store"
)

// TestDeckCloneCopiesContentWithoutProgress 是 M5-4 的主验收：
// 克隆后 note/card 数量一致、新 owner 名下 card_states 为 0、原卡组不受影响。
func TestDeckCloneCopiesContentWithoutProgress(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Source deck")
	// 三种题型：basic(1 张)、basic_both(2 张)、cloze(2 张) —— 覆盖一 note 多 card。
	seedBasic(t, db, deck.ID, "Q1", "A1", "tag-one")
	both := &store.Note{DeckID: deck.ID, Kind: "basic_both"}
	if _, err := store.NewNoteStore(db).Create(context.Background(), both, map[string]any{"front": "Q2", "back": "A2"}); err != nil {
		t.Fatalf("create basic_both: %v", err)
	}
	cloze := &store.Note{DeckID: deck.ID, Kind: "cloze"}
	if _, err := store.NewNoteStore(db).Create(context.Background(), cloze, map[string]any{"text": "{{c1::one}} and {{c2::two}}"}); err != nil {
		t.Fatalf("create cloze: %v", err)
	}

	srcNotes := countNotes(t, db, deck.ID)
	srcCards := countCards(t, db, deck.ID)
	if srcNotes != 3 || srcCards != 5 {
		t.Fatalf("seed produced (%d notes, %d cards), want (3, 5)", srcNotes, srcCards)
	}

	// owner 克隆自己的卡组。
	rec := postForm(t, srv, "/decks/"+u64str(deck.ID)+"/clone", url.Values{"csrf_token": {ownerCSRF}}, ownerCookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST clone status = %d, want 303 (body %s)", rec.Code, rec.Body.String())
	}
	newID := parseDeckIDFromLocation(t, rec.Header().Get("Location"))

	// 新卡组归属调用者、名字带副本后缀、预设另建一份、默认私有。
	var cloned store.Deck
	if err := db.First(&cloned, "id = ?", newID).Error; err != nil {
		t.Fatalf("load cloned deck: %v", err)
	}
	if cloned.OwnerUserID != ownerID {
		t.Errorf("cloned deck owner = %d, want %d", cloned.OwnerUserID, ownerID)
	}
	if cloned.ID == deck.ID {
		t.Fatal("clone reused the source deck id")
	}
	if cloned.PresetID == deck.PresetID {
		t.Errorf("clone shares the source preset id %d, want a copied preset", cloned.PresetID)
	}
	if cloned.Name == deck.Name || !strings.Contains(cloned.Name, deck.Name) {
		t.Errorf("cloned deck name = %q, want it to contain %q", cloned.Name, deck.Name)
	}

	// 内容数量一致。
	if got := countNotes(t, db, newID); got != srcNotes {
		t.Errorf("cloned note count = %d, want %d", got, srcNotes)
	}
	if got := countCards(t, db, newID); got != srcCards {
		t.Errorf("cloned card count = %d, want %d", got, srcCards)
	}

	// 进度为零：新 owner 在新 card 上没有 card_states 行。
	var states int64
	if err := db.Model(&store.CardState{}).
		Where("user_id = ? AND card_id IN (?)", ownerID, cardIDSubquery(db, newID)).
		Count(&states).Error; err != nil {
		t.Fatalf("count cloned card_states: %v", err)
	}
	if states != 0 {
		t.Errorf("cloned deck carries progress: card_states rows = %d, want 0", states)
	}

	// 原卡组不受影响：数量不变。
	if got := countNotes(t, db, deck.ID); got != srcNotes {
		t.Errorf("source note count changed to %d, want %d", got, srcNotes)
	}
	if got := countCards(t, db, deck.ID); got != srcCards {
		t.Errorf("source card count changed to %d, want %d", got, srcCards)
	}

	// 克隆也写一行审计。
	n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionDeckClone)
	if err != nil {
		t.Fatalf("count clone audit: %v", err)
	}
	if n != 1 {
		t.Errorf("audit rows for %s = %d, want 1", store.ActionDeckClone, n)
	}
}

// TestDeckCloneAllowedForReader 覆盖「自己可读即可克隆」：被授权 reader 能把内容复制到自己名下。
func TestDeckCloneAllowedForReader(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Readable deck")
	seedBasic(t, db, deck.ID, "Q", "A")
	user2ID, u2Cookies, u2CSRF := createUserAndLogin(t, srv, db, "cloner2")
	if rec := postForm(t, srv, "/decks/"+u64str(deck.ID)+"/sharing/grant", url.Values{
		"csrf_token": {ownerCSRF}, "username": {"cloner2"}, "role": {store.RoleReader},
	}, ownerCookies); rec.Code != http.StatusSeeOther {
		t.Fatalf("owner grant reader status = %d, want 303", rec.Code)
	}

	rec := postForm(t, srv, "/decks/"+u64str(deck.ID)+"/clone", url.Values{"csrf_token": {u2CSRF}}, u2Cookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("reader POST clone status = %d, want 303 (body %s)", rec.Code, rec.Body.String())
	}
	newID := parseDeckIDFromLocation(t, rec.Header().Get("Location"))
	var cloned store.Deck
	if err := db.First(&cloned, "id = ?", newID).Error; err != nil {
		t.Fatalf("load cloned deck: %v", err)
	}
	if cloned.OwnerUserID != user2ID {
		t.Errorf("cloned deck owner = %d, want reader id %d", cloned.OwnerUserID, user2ID)
	}
	if got := countNotes(t, db, newID); got != 1 {
		t.Errorf("cloned note count = %d, want 1", got)
	}
	var states int64
	if err := db.Model(&store.CardState{}).Where("user_id = ?", user2ID).Count(&states).Error; err != nil {
		t.Fatalf("count reader card_states: %v", err)
	}
	if states != 0 {
		t.Errorf("reader clone carries progress: %d rows, want 0", states)
	}
}

// countNotes 统计卡组下未软删除的 note 数。
func countNotes(t *testing.T, db *gorm.DB, deckID uint64) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&store.Note{}).Where("deck_id = ?", deckID).Count(&n).Error; err != nil {
		t.Fatalf("count notes: %v", err)
	}
	return n
}

// countCards 统计卡组下可见的 card 数（join notes 排除软删除内容）。
func countCards(t *testing.T, db *gorm.DB, deckID uint64) int64 {
	t.Helper()
	var n int64
	if err := db.Table("cards").
		Joins("JOIN notes ON notes.id = cards.note_id AND notes.deleted_at IS NULL").
		Where("notes.deck_id = ? AND cards.deleted_at IS NULL", deckID).
		Count(&n).Error; err != nil {
		t.Fatalf("count cards: %v", err)
	}
	return n
}

// cardIDSubquery 返回某卡组下 card id 的子查询，用于统计 card_states。
func cardIDSubquery(db *gorm.DB, deckID uint64) *gorm.DB {
	return db.Table("cards").
		Select("cards.id").
		Joins("JOIN notes ON notes.id = cards.note_id AND notes.deleted_at IS NULL").
		Where("notes.deck_id = ? AND cards.deleted_at IS NULL", deckID)
}

// parseDeckIDFromLocation 从 /decks/<id>/notes 形式的 Location 中取出卡组 id。
func parseDeckIDFromLocation(t *testing.T, location string) uint64 {
	t.Helper()
	parts := strings.Split(strings.Trim(location, "/"), "/")
	if len(parts) < 2 || parts[0] != "decks" {
		t.Fatalf("unexpected clone redirect Location %q", location)
	}
	id, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil || id == 0 {
		t.Fatalf("cannot parse deck id from Location %q: %v", location, err)
	}
	return id
}
