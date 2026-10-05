package web

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// grantReaderHelper 在测试库里写入一行 reader 授权；失败即测试失败。
func grantReaderHelper(t *testing.T, srv *Server, deckID, userID, by uint64) {
	t.Helper()
	_ = srv
	if err := store.NewGrantStore(srv.db).Grant(context.Background(), deckID, userID, store.RoleReader, &by); err != nil {
		t.Fatalf("grant reader on deck %d to user %d: %v", deckID, userID, err)
	}
}

// TestReviewSharedDeckReaderCanSubmitRating 覆盖 F4 缺陷：共享卡组（reader 授权）的复习提交。
// 队列对该读者可见（review.go 的范围校验只要求 RoleReader），但 loadReviewCard 曾只认 owner，
// 于是同一张卡一提交就 404。修复后读者评分成功，并且进度只写读者自己：
// reviews.user_id 是读者、card_states 键是读者，卡组 owner 的进度行不受影响
// （DESIGN.md §2.2 内容与进度分离、§5 “reader 只读、只能自己复习”、§8.2）。
func TestReviewSharedDeckReaderCanSubmitRating(t *testing.T) {
	srv, db, ownerID, _, _ := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Shared deck")
	note := seedBasic(t, db, deck.ID, "Front", "Back")
	cardID := cardIDOfNote(t, db, note.ID)
	readerID, readerCookies, readerCSRF := createUserAndLogin(t, srv, db, "reader1")
	grantReaderHelper(t, srv, deck.ID, readerID, ownerID)

	rec := postForm(t, srv, "/review/answer", url.Values{
		"csrf_token":       {readerCSRF},
		"card_id":          {u64str(cardID)},
		"deck":             {u64str(deck.ID)},
		"rating":           {"3"},
		"expected_version": {"0"},
		"done":             {"0"},
	}, readerCookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("reader POST /review/answer on a shared deck status = %d, want 200 (body %s)",
			rec.Code, snippet(rec.Body.String()))
	}

	var readerReviews, ownerReviews, readerStates, ownerStates int64
	db.Model(&store.Review{}).Where("user_id = ?", readerID).Count(&readerReviews)
	db.Model(&store.Review{}).Where("user_id = ?", ownerID).Count(&ownerReviews)
	db.Model(&store.CardState{}).Where("user_id = ?", readerID).Count(&readerStates)
	db.Model(&store.CardState{}).Where("user_id = ?", ownerID).Count(&ownerStates)
	if readerReviews != 1 || readerStates != 1 {
		t.Errorf("reader progress rows = (reviews %d, states %d), want (1, 1)", readerReviews, readerStates)
	}
	if ownerReviews != 0 || ownerStates != 0 {
		t.Errorf("reader submission touched the deck owner's progress: reviews=%d states=%d, want 0/0",
			ownerReviews, ownerStates)
	}

	var rev store.Review
	if err := db.Where("user_id = ?", readerID).First(&rev).Error; err != nil {
		t.Fatalf("load reader review row: %v", err)
	}
	if rev.CardID != cardID {
		t.Errorf("reader review card_id = %d, want %d", rev.CardID, cardID)
	}
	var st store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", cardID, readerID).First(&st).Error; err != nil {
		t.Errorf("reader card_states row (card %d, user %d) missing: %v", cardID, readerID, err)
	}
}

// TestReviewSharedDeckUnauthorizedSubmitDenied 覆盖反面用例：对无任何授权的用户，提交既不得
// 放行，也不得写任何进度（DESIGN.md §5、§11）。请求不携带 deck 参数，让拒绝只能来自卡片级判权。
func TestReviewSharedDeckUnauthorizedSubmitDenied(t *testing.T) {
	srv, db, ownerID, _, _ := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Private deck")
	note := seedBasic(t, db, deck.ID, "Front", "Back")
	cardID := cardIDOfNote(t, db, note.ID)
	_, outsiderCookies, outsiderCSRF := createUserAndLogin(t, srv, db, "outsider1")

	rec := postForm(t, srv, "/review/answer", url.Values{
		"csrf_token":       {outsiderCSRF},
		"card_id":          {u64str(cardID)},
		"rating":           {"3"},
		"expected_version": {"0"},
		"done":             {"0"},
	}, outsiderCookies)
	if rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("unauthorized POST /review/answer status = %d, want 4xx (body %s)",
			rec.Code, snippet(rec.Body.String()))
	}
	var reviews, states int64
	db.Model(&store.Review{}).Count(&reviews)
	db.Model(&store.CardState{}).Count(&states)
	if reviews != 0 || states != 0 {
		t.Errorf("denied submission wrote progress: reviews=%d states=%d, want 0/0", reviews, states)
	}
}

// TestReviewSharedDeckReaderCannotSuspend 覆盖 F4 的边界：suspend 写的是共享的
// cards.suspended_at（卡片级、对所有使用者生效，DESIGN.md §3.4），因此即便 reader 能复习，
// 也不能暂停所有人的卡。读者请求 suspend 被拒（4xx），且 cards.suspended_at 保持为空。
func TestReviewSharedDeckReaderCannotSuspend(t *testing.T) {
	srv, db, ownerID, _, _ := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Shared deck")
	note := seedBasic(t, db, deck.ID, "Front", "Back")
	cardID := cardIDOfNote(t, db, note.ID)
	readerID, readerCookies, readerCSRF := createUserAndLogin(t, srv, db, "reader2")
	grantReaderHelper(t, srv, deck.ID, readerID, ownerID)

	rec := postForm(t, srv, "/review/action", url.Values{
		"csrf_token": {readerCSRF},
		"action":     {"suspend"},
		"card_id":    {u64str(cardID)},
		"deck":       {u64str(deck.ID)},
		"done":       {"0"},
	}, readerCookies)
	if rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("reader POST /review/action suspend status = %d, want 4xx (body %s)",
			rec.Code, snippet(rec.Body.String()))
	}
	var card store.Card
	if err := db.Where("id = ?", cardID).First(&card).Error; err != nil {
		t.Fatalf("reload card: %v", err)
	}
	if card.SuspendedAt != nil {
		t.Errorf("reader suspend changed the shared cards.suspended_at = %v, want nil", card.SuspendedAt)
	}
}

// TestReviewOwnerCanStillSuspendOwnCard 保护现有行为：owner 在自己卡组上暂停仍成功，
// cards.suspended_at 被写入（DESIGN.md §3.4）。
func TestReviewOwnerCanStillSuspendOwnCard(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Own deck")
	note := seedBasic(t, db, deck.ID, "Front", "Back")
	cardID := cardIDOfNote(t, db, note.ID)

	rec := postForm(t, srv, "/review/action", url.Values{
		"csrf_token": {ownerCSRF},
		"action":     {"suspend"},
		"card_id":    {u64str(cardID)},
		"deck":       {u64str(deck.ID)},
		"done":       {"0"},
	}, ownerCookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner POST /review/action suspend status = %d, want 200 (body %s)",
			rec.Code, snippet(rec.Body.String()))
	}
	var card store.Card
	if err := db.Where("id = ?", cardID).First(&card).Error; err != nil {
		t.Fatalf("reload card: %v", err)
	}
	if card.SuspendedAt == nil {
		t.Errorf("owner suspend did not set cards.suspended_at")
	}
}
