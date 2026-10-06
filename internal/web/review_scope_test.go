package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// cardIDOfNote 取 note 下第一张 card 的 id；复习页隐藏字段 card_id 就是它。
func cardIDOfNote(t *testing.T, db *gorm.DB, noteID uint64) uint64 {
	t.Helper()
	var c store.Card
	if err := db.Where("note_id = ?", noteID).First(&c).Error; err != nil {
		t.Fatalf("load card for note %d: %v", noteID, err)
	}
	return c.ID
}

// TestReviewPageCarriesAllDeckHiddenFields 断言多卡组复习页为每个选中卡组渲染一个隐藏
// deck 字段（评分/动作请求据此原样带回范围，DESIGN.md §8.2）。
func TestReviewPageCarriesAllDeckHiddenFields(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	// GET /review 已切到 SPA 应用壳；禁用 SPA 以覆盖 SSR 复习页（DESIGN.md §8.5）。
	srv.spa = nil
	deckA := seedReviewDeck(t, db, ownerID, "Deck A")
	deckB := seedReviewDeck(t, db, ownerID, "Deck B")
	seedBasic(t, db, deckA.ID, "Front A", "Back A")
	seedBasic(t, db, deckB.ID, "Front B", "Back B")

	rec := getWithCookies(t, srv, "/review?deck="+u64str(deckA.ID)+"&deck="+u64str(deckB.ID), cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /review?deck=A&deck=B status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	body := rec.Body.String()
	if !strings.Contains(body, `id="review-area"`) {
		t.Fatalf("multi-deck review page did not render a card: %s", snippet(body))
	}
	for _, id := range []uint64{deckA.ID, deckB.ID} {
		if !strings.Contains(body, `name="deck" value="`+u64str(id)+`"`) {
			t.Errorf("review page is missing the hidden deck field for deck %d: %s", id, snippet(body))
		}
	}
}

// TestReviewAnswerKeepsMultiDeckScope 是 M3-13 的回归用例：对来自卡组 A 的卡评分后，
// 返回的片段仍必须带上卡组 B 的卡。修复前 POST 处理器忽略表单里的 deck 字段、用被评卡
// 所属卡组重建队列，队列会在第一次评分后塌缩成单卡组（B 的卡消失）。
func TestReviewAnswerKeepsMultiDeckScope(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	// GET /review 已切到 SPA 应用壳；禁用 SPA 以覆盖 SSR 复习页（DESIGN.md §8.5）。
	srv.spa = nil
	deckA := seedReviewDeck(t, db, ownerID, "Deck A")
	deckB := seedReviewDeck(t, db, ownerID, "Deck B")
	noteA := seedBasic(t, db, deckA.ID, "Front A", "Back A")
	noteB := seedBasic(t, db, deckB.ID, "Front B", "Back B")
	cardA := cardIDOfNote(t, db, noteA.ID)
	cardB := cardIDOfNote(t, db, noteB.ID)

	page := getWithCookies(t, srv, "/review?deck="+u64str(deckA.ID)+"&deck="+u64str(deckB.ID), cookies)
	cardID := attrValue(page.Body.String(), "card_id")
	if cardID == "" {
		t.Fatalf("multi-deck page rendered no card: %s", snippet(page.Body.String()))
	}

	rec := postForm(t, srv, "/review/answer", url.Values{
		"csrf_token":       {csrf},
		"card_id":          {cardID},
		"deck":             {u64str(deckA.ID), u64str(deckB.ID)},
		"rating":           {"3"},
		"expected_version": {"0"},
		"done":             {"0"},
	}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /review/answer status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	body := rec.Body.String()

	other := cardB
	if cardID == u64str(cardB) {
		other = cardA
	}
	next := attrValue(body, "card_id")
	if next != u64str(other) {
		t.Fatalf("after rating a card the next card = %q, want %d (the other deck's card); "+
			"the review scope collapsed to the answered card's deck: %s", next, other, snippet(body))
	}
}

// TestReviewAnswerWithoutDeckKeepsWholeCollection 断言无 deck 参数时评分后仍是全库队列，
// 不会退化成被评卡所属卡组；表单也不携带任何 deck 隐藏字段。
func TestReviewAnswerWithoutDeckKeepsWholeCollection(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	// GET /review 已切到 SPA 应用壳；禁用 SPA 以覆盖 SSR 复习页（DESIGN.md §8.5）。
	srv.spa = nil
	deckA := seedReviewDeck(t, db, ownerID, "Deck A")
	deckB := seedReviewDeck(t, db, ownerID, "Deck B")
	noteA := seedBasic(t, db, deckA.ID, "Front A", "Back A")
	noteB := seedBasic(t, db, deckB.ID, "Front B", "Back B")
	cardA := cardIDOfNote(t, db, noteA.ID)
	cardB := cardIDOfNote(t, db, noteB.ID)

	page := getWithCookies(t, srv, "/review", cookies)
	body := page.Body.String()
	if strings.Contains(body, `name="deck" value="`) {
		t.Errorf("whole-collection review page must not carry deck hidden fields: %s", snippet(body))
	}
	cardID := attrValue(body, "card_id")
	if cardID == "" {
		t.Fatalf("whole-collection page rendered no card: %s", snippet(body))
	}

	rec := postForm(t, srv, "/review/answer", url.Values{
		"csrf_token":       {csrf},
		"card_id":          {cardID},
		"rating":           {"3"},
		"expected_version": {"0"},
		"done":             {"0"},
	}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /review/answer (no deck) status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	next := attrValue(rec.Body.String(), "card_id")
	other := cardB
	if cardID == u64str(cardB) {
		other = cardA
	}
	if next != u64str(other) {
		t.Fatalf("whole-collection review collapsed after one rating: next = %q, want %d", next, other)
	}
}

// TestReviewScopeRejectsUnreadableDeck 覆盖负例：范围里出现用户读不到的卡组时整次请求失败，
// 不得静默丢弃该卡组后继续（由 loadDeckForRole 写出 403/404）。
func TestReviewScopeRejectsUnreadableDeck(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	// GET /review 已切到 SPA 应用壳；禁用 SPA 以覆盖 SSR 复习页（DESIGN.md §8.5）。
	srv.spa = nil
	// 属于另一个用户的私有卡组；当前登录用户既非 owner 也无授权。
	foreign := seedReviewDeck(t, db, ownerID+1, "Foreign deck")

	rec := getWithCookies(t, srv, "/review?deck="+u64str(foreign.ID), cookies)
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Fatalf("GET /review with an unreadable deck status = %d, want 403 or 404 (body %s)",
			rec.Code, snippet(rec.Body.String()))
	}
}

// TestReviewScopeRejectsMixedUnreadableDeck 覆盖混合集合负例：一个自己可读的卡组和一个他人的
// 私有卡组同时出现时，整次请求失败，不得静默丢弃无权限的那个、只渲染可读卡组的卡片。
// 与 REST 的 TestDueCardsFailsWholeRequestForUnreadableDeck 是同一口径（DESIGN.md §3.3）。
func TestReviewScopeRejectsMixedUnreadableDeck(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	// GET /review 已切到 SPA 应用壳；禁用 SPA 以覆盖 SSR 复习页（DESIGN.md §8.5）。
	srv.spa = nil
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
