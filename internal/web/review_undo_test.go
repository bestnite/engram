package web

import (
	"encoding/json"
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// undoBody is the external shape of the undo endpoint: the queue rebuilt over the same scope + the public id of the undone card.
// undone_card_id is what the frontend uses to locate the current card again (the queue is ordered by due_at, so it is not guaranteed to be first).
type undoBody struct {
	UndoneCardID string `json:"undone_card_id"`
	Remaining    int    `json:"remaining"`
	Cards        []struct {
		CardID string `json:"card_id"`
	} `json:"cards"`
}

// submitSelfReview grades a card through the SPA self-assessment entry point (writes one reviews row and advances card_states).
func submitSelfReview(t *testing.T, srv *Server, cookies []*http.Cookie, csrf, cardPub, deckPub string, rating, expectedVersion int) {
	t.Helper()
	rec := postJSONWithCSRF(t, srv, "/api/v1/review/answer", map[string]any{
		"card_id": cardPub, "rating": rating, "expected_version": expectedVersion,
		"deck": []string{deckPub},
	}, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("self review rating=%d version=%d = %d, want 200 (body %s)",
			rating, expectedVersion, rec.Code, snippet(rec.Body.String()))
	}
}

// TestReviewUndoRestoresStateAndDeletesReview asserts the core behavior of undo: it deletes the undone reviews row,
// restores card_states exactly to before that grade (due date included), writes one review.undo audit row, and the response explicitly carries
// the public id of the undone card (undone_card_id).
func TestReviewUndoRestoresStateAndDeletesReview(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Undo deck")
	note := seedBasic(t, db, deck.ID, "Q", "A")
	cardID := cardIDOfNote(t, db, note.ID)
	cardPub := cardPublicIDOfNote(t, db, note.ID)

	submitSelfReview(t, srv, cookies, csrf, cardPub, deck.PublicID, 3, 0)
	var afterFirst store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", cardID, ownerID).First(&afterFirst).Error; err != nil {
		t.Fatalf("load state after first review: %v", err)
	}
	submitSelfReview(t, srv, cookies, csrf, cardPub, deck.PublicID, 3, afterFirst.Version)
	var beforeUndo store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", cardID, ownerID).First(&beforeUndo).Error; err != nil {
		t.Fatalf("load state before undo: %v", err)
	}

	var reviewsBefore int64
	db.Model(&store.Review{}).Where("card_id = ?", cardID).Count(&reviewsBefore)
	if reviewsBefore != 2 {
		t.Fatalf("reviews before undo = %d, want 2", reviewsBefore)
	}

	rec := postJSONWithCSRF(t, srv, "/api/v1/review/undo", map[string]any{
		"card_id": cardPub, "deck": []string{deck.PublicID}, "expected_version": beforeUndo.Version,
	}, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("undo = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var body undoBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode undo response: %v (body %s)", err, snippet(rec.Body.String()))
	}
	if body.UndoneCardID != cardPub {
		t.Errorf("undone_card_id = %q, want %q", body.UndoneCardID, cardPub)
	}

	// The undone reviews row is deleted, leaving only the first one (the sole exception to append-only).
	var reviewsAfter int64
	db.Model(&store.Review{}).Where("card_id = ?", cardID).Count(&reviewsAfter)
	if reviewsAfter != 1 {
		t.Errorf("reviews after undo = %d, want 1", reviewsAfter)
	}

	// card_states is restored exactly to the state after the first grade (that is, before the second grade).
	var restored store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", cardID, ownerID).First(&restored).Error; err != nil {
		t.Fatalf("load state after undo: %v", err)
	}
	if restored.State != afterFirst.State {
		t.Errorf("state after undo = %q, want %q", restored.State, afterFirst.State)
	}
	if restored.DueAt == nil || afterFirst.DueAt == nil || restored.DueAt.UnixMilli() != afterFirst.DueAt.UnixMilli() {
		t.Errorf("due_at after undo = %v, want %v (exact restore)", restored.DueAt, afterFirst.DueAt)
	}
	if restored.ScheduledDays != afterFirst.ScheduledDays {
		t.Errorf("scheduled_days after undo = %d, want %d", restored.ScheduledDays, afterFirst.ScheduledDays)
	}
	// version advances monotonically: undo itself must also invalidate in-flight submits.
	var afterSecond store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", cardID, ownerID).First(&afterSecond).Error; err != nil {
		t.Fatalf("reload state after undo: %v", err)
	}
	if afterSecond.Version != restored.Version {
		t.Errorf("state version read twice differs: %d vs %d", afterSecond.Version, restored.Version)
	}
	if restored.Version <= afterFirst.Version {
		t.Errorf("version after undo = %d, want > %d (monotonic)", restored.Version, afterFirst.Version)
	}

	// Audit row: undo writes one review.undo.
	var audits int64
	db.Model(&store.AuditLog{}).Where("action = ?", store.ActionReviewUndo).Count(&audits)
	if audits != 1 {
		t.Errorf("review.undo audit rows = %d, want 1", audits)
	}
}

// TestReviewUndoReplayIsRejectedWithoutDeletingAnotherReview is the HTTP-level acceptance for AUDIT-02:
// resending the same undo request a second time must give 409 and delete only one grade — neither a network retry nor a window opened twice can wrongly delete another historical grade.
func TestReviewUndoReplayIsRejectedWithoutDeletingAnotherReview(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Undo replay deck")
	note := seedBasic(t, db, deck.ID, "Q", "A")
	cardID := cardIDOfNote(t, db, note.ID)
	cardPub := cardPublicIDOfNote(t, db, note.ID)

	submitSelfReview(t, srv, cookies, csrf, cardPub, deck.PublicID, 3, 0)
	var v1 store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", cardID, ownerID).First(&v1).Error; err != nil {
		t.Fatalf("load state after first review: %v", err)
	}
	submitSelfReview(t, srv, cookies, csrf, cardPub, deck.PublicID, 3, v1.Version)
	var v2 store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", cardID, ownerID).First(&v2).Error; err != nil {
		t.Fatalf("load state after second review: %v", err)
	}

	body := map[string]any{"card_id": cardPub, "deck": []string{deck.PublicID}, "expected_version": v2.Version}
	if rec := postJSONWithCSRF(t, srv, "/api/v1/review/undo", body, cookies, csrf); rec.Code != http.StatusOK {
		t.Fatalf("first undo = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	rec := postJSONWithCSRF(t, srv, "/api/v1/review/undo", body, cookies, csrf)
	if rec.Code != http.StatusConflict {
		t.Fatalf("replayed undo = %d, want 409 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var reviews int64
	db.Model(&store.Review{}).Where("card_id = ?", cardID).Count(&reviews)
	if reviews != 1 {
		t.Errorf("reviews after replayed undo = %d, want 1 (must not delete another review)", reviews)
	}
}

// TestReviewUndoReturnsCardToQueue asserts undo puts the card back into the queue and the undone_card_id in the response points at it:
// the frontend locates the current card from it instead of assuming it sits first in the rebuilt queue.
func TestReviewUndoReturnsCardToQueue(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Undo queue deck")
	note := seedBasic(t, db, deck.ID, "Q", "A")
	cardPub := cardPublicIDOfNote(t, db, note.ID)
	submitSelfReview(t, srv, cookies, csrf, cardPub, deck.PublicID, 3, 0)
	var beforeUndo store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", cardIDOfNote(t, db, note.ID), ownerID).First(&beforeUndo).Error; err != nil {
		t.Fatalf("load state before undo: %v", err)
	}

	rec := postJSONWithCSRF(t, srv, "/api/v1/review/undo", map[string]any{
		"card_id": cardPub, "deck": []string{deck.PublicID}, "expected_version": beforeUndo.Version,
	}, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("undo = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var body undoBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.UndoneCardID != cardPub {
		t.Errorf("undone_card_id = %q, want %q", body.UndoneCardID, cardPub)
	}
	found := false
	for _, c := range body.Cards {
		if c.CardID == cardPub {
			found = true
		}
	}
	if !found {
		t.Errorf("undone card %s missing from the rebuilt queue: %s", cardPub, snippet(rec.Body.String()))
	}
	if body.Remaining != len(body.Cards) {
		t.Errorf("remaining = %d, want %d (len(cards))", body.Remaining, len(body.Cards))
	}
}

// TestReviewUndoRejections covers the rejection paths of the undo endpoint: the card has no review log 409, no session/missing CSRF 403,
// card outside the scope 400, a stranger who cannot read the deck 4xx. No rejection may delete a reviews row or write an audit.
func TestReviewUndoRejections(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Undo guard deck")
	other := seedReviewDeck(t, db, ownerID, "Undo other deck")
	reviewedNote := seedBasic(t, db, deck.ID, "Reviewed Q", "A")
	bareNote := seedBasic(t, db, deck.ID, "Bare Q", "A")
	reviewedID := cardIDOfNote(t, db, reviewedNote.ID)
	reviewedPub := cardPublicIDOfNote(t, db, reviewedNote.ID)
	barePub := cardPublicIDOfNote(t, db, bareNote.ID)
	submitSelfReview(t, srv, cookies, csrf, reviewedPub, deck.PublicID, 3, 0)
	valid := map[string]any{"card_id": reviewedPub, "deck": []string{deck.PublicID}}

	// ① The card has no review log at all: 409 conflict (schedule.ErrNothingToUndo).
	rec := postJSONWithCSRF(t, srv, "/api/v1/review/undo", map[string]any{
		"card_id": barePub, "deck": []string{deck.PublicID},
	}, cookies, csrf)
	if rec.Code != http.StatusConflict {
		t.Fatalf("undo without review log = %d, want 409 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	// ② No session: the CSRF middleware rejects with 403 csrf_no_session before the handler, same criterion as bury/render
	// (the 401 branch inside the handler is unreachable for sessionless requests).
	if rec := postJSONWithCSRF(t, srv, "/api/v1/review/undo", valid, nil, ""); rec.Code != http.StatusForbidden {
		t.Errorf("undo without session = %d, want 403", rec.Code)
	}
	// ③ Missing CSRF and wrong CSRF: 403.
	if rec := postJSONWithCSRF(t, srv, "/api/v1/review/undo", valid, cookies, ""); rec.Code != http.StatusForbidden {
		t.Errorf("undo without CSRF = %d, want 403", rec.Code)
	}
	if rec := postJSONWithCSRF(t, srv, "/api/v1/review/undo", valid, cookies, "not-the-token"); rec.Code != http.StatusForbidden {
		t.Errorf("undo with bad CSRF = %d, want 403", rec.Code)
	}
	// ④ Card outside the selected scope: 400 (same criterion as bury).
	if rec := postJSONWithCSRF(t, srv, "/api/v1/review/undo", map[string]any{
		"card_id": reviewedPub, "deck": []string{other.PublicID},
	}, cookies, csrf); rec.Code != http.StatusBadRequest {
		t.Errorf("undo out-of-scope card = %d, want 400", rec.Code)
	}
	// ⑤ A stranger who cannot read the deck: 4xx.
	_, outsiderCookies, outsiderCSRF := createUserAndLogin(t, srv, db, "undo-outsider")
	if rec := postJSONWithCSRF(t, srv, "/api/v1/review/undo", valid, outsiderCookies, outsiderCSRF); rec.Code < 400 || rec.Code >= 500 {
		t.Errorf("undo by outsider = %d, want 4xx (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	// A rejected request undid nothing: the graded card still has exactly one log entry, no review.undo audit, no new state row.
	var reviews, audits, states int64
	db.Model(&store.Review{}).Where("card_id = ?", reviewedID).Count(&reviews)
	db.Model(&store.AuditLog{}).Where("action = ?", store.ActionReviewUndo).Count(&audits)
	db.Model(&store.CardState{}).Where("card_id = ?", bareNote.ID).Count(&states)
	if reviews != 1 || audits != 0 || states != 0 {
		t.Fatalf("denied undo changed data: reviews=%d audits=%d bareStates=%d, want 1/0/0", reviews, audits, states)
	}
}
