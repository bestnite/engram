package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// reviewActionBody is the external shape shared by the five review action responses (answer, grade, undo, suspend,
// bury): the same-scope queue counts, the single card the response serializes, and that card's inline render.
type reviewActionBody struct {
	CardID       string              `json:"card_id"`
	UndoneCardID string              `json:"undone_card_id"`
	Remaining    int                 `json:"remaining"`
	QueueSize    int                 `json:"queue_size"`
	NewRemaining int                 `json:"new_remaining"`
	Cards        []api.DueCard       `json:"cards"`
	RenderedCard *reviewRenderedCard `json:"rendered_card"`
}

// decodeReviewAction decodes an action response; a decode failure fails the test.
func decodeReviewAction(t *testing.T, rec *httptest.ResponseRecorder) reviewActionBody {
	t.Helper()
	var body reviewActionBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode review action response: %v (body %s)", err, snippet(rec.Body.String()))
	}
	return body
}

// assertInlineRender checks the inline render against the one card the response serializes: the fields are the render
// endpoint's, so the client can apply the response without a second request.
func assertInlineRender(t *testing.T, body reviewActionBody) {
	t.Helper()
	if len(body.Cards) != 1 {
		t.Fatalf("cards = %d, want exactly 1 (body decoded %+v)", len(body.Cards), body)
	}
	if body.RenderedCard == nil {
		t.Fatalf("rendered_card is missing for card %s", body.Cards[0].CardID)
	}
	if body.RenderedCard.CardID != body.Cards[0].CardID {
		t.Errorf("rendered_card.card_id = %q, want %q", body.RenderedCard.CardID, body.Cards[0].CardID)
	}
	if body.RenderedCard.Front.HTML == "" || body.RenderedCard.Back.HTML == "" {
		t.Errorf("rendered_card faces are empty: front %q back %q", body.RenderedCard.Front.HTML, body.RenderedCard.Back.HTML)
	}
	if body.RenderedCard.EditHref == "" {
		t.Error("rendered_card.edit_href is empty")
	}
}

// TestReviewAnswerSerializesOneCardAndCountsTheQueue covers the slimmed answer response: a three-card queue comes back
// as one card plus the totals over the complete queue, and that one card is rendered through the render endpoint's
// frame-ready path.
func TestReviewAnswerSerializesOneCardAndCountsTheQueue(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Slim answer deck")
	answeredNote := seedBasic(t, db, deck.ID, "Q1", "A1")
	seedBasic(t, db, deck.ID, "Q2", "A2")
	seedBasic(t, db, deck.ID, "Q3", "A3")
	answeredPub := cardPublicIDOfNote(t, db, answeredNote.ID)

	// The frame header asks for frame-ready HTML, the same request the render endpoint receives from the review page.
	rec := postJSON(srv, "/api/v1/review/answer", map[string]any{
		"card_id": answeredPub, "rating": 4, "expected_version": 0, "deck": []string{deck.PublicID},
	}, cookies, map[string]string{auth.CSRFHeaderName: csrf, frameOutputHeader: "1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("answer = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	body := decodeReviewAction(t, rec)

	// Easy takes the answered new card out of today's queue; the other two new cards remain.
	if body.QueueSize != 2 || body.Remaining != 2 {
		t.Errorf("queue_size/remaining = %d/%d, want 2/2 (the answered card left the queue)", body.QueueSize, body.Remaining)
	}
	if body.NewRemaining != 2 {
		t.Errorf("new_remaining = %d, want 2", body.NewRemaining)
	}
	assertInlineRender(t, body)
	if body.Cards[0].CardID == answeredPub {
		t.Errorf("cards[0] = the answered card %s, want the next card", answeredPub)
	}
}

// TestReviewGradeSerializesOneCard covers the same contract for the machine-graded entry point.
func TestReviewGradeSerializesOneCard(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Slim grade deck")
	graded := seedGradedNote(t, db, deck.ID, "numeric", map[string]any{
		"prompt": "分数 1/2 化成百分数是多少？", "value": 50.0, "unit": "%",
	})
	seedBasic(t, db, deck.ID, "Q2", "A2")
	seedBasic(t, db, deck.ID, "Q3", "A3")

	rec := postJSON(srv, "/api/v1/review/grade", map[string]any{
		"card_id": cardPublicIDOfNote(t, db, graded.ID), "expected_version": 0,
		"deck": []string{deck.PublicID}, "answer": "50",
	}, cookies, map[string]string{auth.CSRFHeaderName: csrf, frameOutputHeader: "1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("grade = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	body := decodeReviewAction(t, rec)
	if body.QueueSize != 2 || body.Remaining != 2 {
		t.Errorf("queue_size/remaining = %d/%d, want 2/2", body.QueueSize, body.Remaining)
	}
	if body.NewRemaining != 2 {
		t.Errorf("new_remaining = %d, want 2", body.NewRemaining)
	}
	assertInlineRender(t, body)
}

// TestReviewBuryAndSuspendSerializeOneCard covers the two queue-only actions: the returned queue still totals two
// actions' worth of counts, but only the next card travels in the body.
func TestReviewBuryAndSuspendSerializeOneCard(t *testing.T) {
	for _, action := range []struct {
		name string
		path string
	}{
		{"bury", "/api/v1/review/bury"},
		{"suspend", "/api/v1/review/suspend"},
	} {
		t.Run(action.name, func(t *testing.T) {
			srv, db, ownerID, cookies, csrf := newNotesServer(t)
			deck := seedReviewDeck(t, db, ownerID, "Slim "+action.name+" deck")
			target := seedBasic(t, db, deck.ID, "Q1", "A1")
			other := seedBasic(t, db, deck.ID, "Q2", "A2")

			rec := postJSON(srv, action.path, map[string]any{
				"card_id": cardPublicIDOfNote(t, db, target.ID), "deck": []string{deck.PublicID},
			}, cookies, map[string]string{auth.CSRFHeaderName: csrf, frameOutputHeader: "1"})
			if rec.Code != http.StatusOK {
				t.Fatalf("%s = %d, want 200 (body %s)", action.name, rec.Code, snippet(rec.Body.String()))
			}
			body := decodeReviewAction(t, rec)
			if body.QueueSize != 1 || body.Remaining != 1 {
				t.Errorf("%s: queue_size/remaining = %d/%d, want 1/1", action.name, body.QueueSize, body.Remaining)
			}
			if body.NewRemaining != 1 {
				t.Errorf("%s: new_remaining = %d, want 1", action.name, body.NewRemaining)
			}
			assertInlineRender(t, body)
			if want := cardPublicIDOfNote(t, db, other.ID); body.Cards[0].CardID != want {
				t.Errorf("%s: cards[0] = %s, want the surviving card %s", action.name, body.Cards[0].CardID, want)
			}
		})
	}
}

// TestReviewUndoCardsHoldTheUndoneCard covers the undo contract: cards carries the restored card itself, even when the
// queue's due_at order puts a different card first, and undone_card_id names the same card.
func TestReviewUndoCardsHoldTheUndoneCard(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Slim undo deck")
	// A learning card due an hour ago sits at the head of the queue ahead of every new card, so the card restored by
	// undo cannot be the head once the queue is rebuilt.
	headNote := seedBasic(t, db, deck.ID, "Q-head", "A-head")
	headCardID := cardIDOfNote(t, db, headNote.ID)
	past := time.Now().UTC().Add(-time.Hour)
	if err := db.Create(&store.CardState{
		CardID: headCardID, UserID: ownerID, State: store.CardStateLearning, DueAt: &past, StepIndex: 1,
	}).Error; err != nil {
		t.Fatalf("seed learning state: %v", err)
	}
	headPub := cardPublicIDOfNote(t, db, headNote.ID)

	undoneNote := seedBasic(t, db, deck.ID, "Q-undone", "A-undone")
	undonePub := cardPublicIDOfNote(t, db, undoneNote.ID)
	submitSelfReview(t, srv, cookies, csrf, undonePub, deck.PublicID, 3, 0)
	var reviewed store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", cardIDOfNote(t, db, undoneNote.ID), ownerID).First(&reviewed).Error; err != nil {
		t.Fatalf("load state after review: %v", err)
	}

	rec := postJSON(srv, "/api/v1/review/undo", map[string]any{
		"card_id": undonePub, "deck": []string{deck.PublicID}, "expected_version": reviewed.Version,
	}, cookies, map[string]string{auth.CSRFHeaderName: csrf, frameOutputHeader: "1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("undo = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	body := decodeReviewAction(t, rec)
	if body.UndoneCardID != undonePub {
		t.Errorf("undone_card_id = %q, want %q", body.UndoneCardID, undonePub)
	}
	if body.QueueSize != 2 || body.Remaining != 2 {
		t.Errorf("queue_size/remaining = %d/%d, want 2/2", body.QueueSize, body.Remaining)
	}
	if body.NewRemaining != 1 {
		t.Errorf("new_remaining = %d, want 1 (only the restored card is new)", body.NewRemaining)
	}
	assertInlineRender(t, body)
	if body.Cards[0].CardID != undonePub {
		t.Errorf("cards[0] = %q, want the undone card %q", body.Cards[0].CardID, undonePub)
	}

	// The rebuilt queue itself is ordered by due_at: the learning card is first, so cards[0] above can only be the
	// undone card because the handler located it inside the queue.
	due := getWithCookies(t, srv, "/api/v1/review/due?deck="+deck.PublicID, cookies)
	if due.Code != http.StatusOK {
		t.Fatalf("GET due after undo = %d, want 200 (body %s)", due.Code, snippet(due.Body.String()))
	}
	var dueBody struct {
		Cards []struct {
			CardID string `json:"card_id"`
		} `json:"cards"`
	}
	if err := json.Unmarshal(due.Body.Bytes(), &dueBody); err != nil {
		t.Fatalf("decode due response: %v", err)
	}
	if len(dueBody.Cards) != 2 || dueBody.Cards[0].CardID != headPub {
		t.Fatalf("queue after undo = %+v, want the learning card %s first and the undone card second", dueBody.Cards, headPub)
	}
	if dueBody.Cards[1].CardID != undonePub {
		t.Errorf("queue after undo = %+v, want the undone card %s at index 1", dueBody.Cards, undonePub)
	}
}

// TestReviewUndoOutsideTheQueueStillShowsTheCard covers the undo case the queue cannot serve: the restored card is due
// later than today, so the rebuilt queue does not contain it. The response must still carry that card, while the counts
// keep describing the real queue.
//
// The card is graded twice from new: the first grade leaves it in a learning step due in a minute, the second one is
// undone. Undo restores the learning step's due time from the review row's snapshot, which is still in the future, so
// the card is outside the queue. (Undoing the first grade of a non-new card is refused, because the state before it
// cannot be restored from previous memory - that rule is unchanged here.)
func TestReviewUndoOutsideTheQueueStillShowsTheCard(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Undo outside queue deck")
	note := seedBasic(t, db, deck.ID, "Q", "A")
	cardID := cardIDOfNote(t, db, note.ID)
	cardPub := cardPublicIDOfNote(t, db, note.ID)

	submitSelfReview(t, srv, cookies, csrf, cardPub, deck.PublicID, 3, 0)
	var afterFirst store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", cardID, ownerID).First(&afterFirst).Error; err != nil {
		t.Fatalf("load state after first review: %v", err)
	}
	submitSelfReview(t, srv, cookies, csrf, cardPub, deck.PublicID, 3, afterFirst.Version)
	var reviewed store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", cardID, ownerID).First(&reviewed).Error; err != nil {
		t.Fatalf("load state after second review: %v", err)
	}
	// The graded card waits for its next learning step, so it is not in the queue right now.
	queued := getWithCookies(t, srv, "/api/v1/review/due?deck="+deck.PublicID, cookies)
	var queuedBody struct {
		Cards []struct {
			CardID string `json:"card_id"`
		} `json:"cards"`
	}
	if err := json.Unmarshal(queued.Body.Bytes(), &queuedBody); err != nil {
		t.Fatalf("decode due response: %v", err)
	}
	if len(queuedBody.Cards) != 0 {
		t.Fatalf("queue while the card waits for its next step = %+v, want no cards", queuedBody.Cards)
	}

	rec := postJSON(srv, "/api/v1/review/undo", map[string]any{
		"card_id": cardPub, "deck": []string{deck.PublicID}, "expected_version": reviewed.Version,
	}, cookies, map[string]string{auth.CSRFHeaderName: csrf, frameOutputHeader: "1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("undo = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	body := decodeReviewAction(t, rec)
	if body.UndoneCardID != cardPub {
		t.Errorf("undone_card_id = %q, want %q", body.UndoneCardID, cardPub)
	}
	// The counts describe the real queue, which holds nothing, while the card the user restored still travels.
	if body.QueueSize != 0 || body.Remaining != 0 || body.NewRemaining != 0 {
		t.Errorf("counts = queue_size %d, remaining %d, new_remaining %d; want 0/0/0", body.QueueSize, body.Remaining, body.NewRemaining)
	}
	assertInlineRender(t, body)
	if body.Cards[0].CardID != cardPub {
		t.Errorf("cards[0] = %q, want the undone card %q", body.Cards[0].CardID, cardPub)
	}
	if body.Cards[0].State != store.CardStateLearning {
		t.Errorf("cards[0].state = %q, want the restored %q", body.Cards[0].State, store.CardStateLearning)
	}
	if !body.Cards[0].DueAt.After(time.Now().UTC()) {
		t.Errorf("cards[0].due_at = %v, want the restored future due time", body.Cards[0].DueAt)
	}

	// The read is for this response only: the queue still does not contain the card.
	after := getWithCookies(t, srv, "/api/v1/review/due?deck="+deck.PublicID, cookies)
	var afterBody struct {
		Cards []struct {
			CardID string `json:"card_id"`
		} `json:"cards"`
	}
	if err := json.Unmarshal(after.Body.Bytes(), &afterBody); err != nil {
		t.Fatalf("decode due response after undo: %v", err)
	}
	if len(afterBody.Cards) != 0 {
		t.Errorf("queue after undo = %+v, want no cards (the undo must not change the queue rules)", afterBody.Cards)
	}
}

// TestReviewResponseSizeDoesNotGrowWithTheQueue is the size regression for the slim response: a large queue used to make
// the answer body grow with every queued card. The test builds the body the old handler returned (the whole queue) and
// compares it with the response the handler returns now; the new body must stay a small fraction of the old one and
// still carry one complete card face.
func TestReviewResponseSizeDoesNotGrowWithTheQueue(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Large queue deck")
	// The deck's new-per-day allowance caps the queue by default; raise it so the whole queue is built.
	if err := db.Model(&store.Deck{}).Where("id = ?", deck.ID).Update("new_per_day", reviewQueueLimit).Error; err != nil {
		t.Fatalf("raise new_per_day: %v", err)
	}
	const cardCount = 240
	for i := 0; i < cardCount; i++ {
		seedBasic(t, db, deck.ID, fmt.Sprintf("front %d", i), fmt.Sprintf("back %d", i))
	}
	var firstCard store.Card
	if err := db.Table("cards").
		Joins("JOIN notes ON notes.id = cards.note_id").
		Where("notes.deck_id = ?", deck.ID).
		Order("cards.id ASC").Take(&firstCard).Error; err != nil {
		t.Fatalf("load first card: %v", err)
	}
	answeredPub := firstCard.PublicID

	rec := postJSON(srv, "/api/v1/review/answer", map[string]any{
		"card_id": answeredPub, "rating": 4, "expected_version": 0, "deck": []string{deck.PublicID},
	}, cookies, map[string]string{auth.CSRFHeaderName: csrf, frameOutputHeader: "1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("answer = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	slim := rec.Body.Bytes()

	// The pre-slim body: the complete rebuilt queue plus the same state fields the handler wrote back then.
	user, err := store.NewUserStore(db).ByID(context.Background(), ownerID)
	if err != nil {
		t.Fatalf("load user: %v", err)
	}
	queue, err := srv.api.DueCards(context.Background(), user, []uint64{deck.ID}, nil, reviewQueueLimit)
	if err != nil {
		t.Fatalf("DueCards() error = %v", err)
	}
	full, err := json.Marshal(map[string]any{"cards": queue, "remaining": len(queue)})
	if err != nil {
		t.Fatalf("marshal the full queue body: %v", err)
	}
	if len(queue) != cardCount-1 {
		t.Fatalf("full queue = %d cards, want %d (the answered card left the queue)", len(queue), cardCount-1)
	}
	// A floor on the baseline keeps the ratio meaningful: the comparison is only evidence while the old body really was
	// large. It is not a limit on the new body, whose size follows the one card face it carries.
	if len(full) < 20_000 {
		t.Fatalf("full queue body is only %d bytes; the size comparison needs a genuinely large queue", len(full))
	}

	body := decodeReviewAction(t, rec)
	if body.QueueSize != cardCount-1 || body.NewRemaining != cardCount-1 {
		t.Errorf("queue_size/new_remaining = %d/%d, want %d/%d", body.QueueSize, body.NewRemaining, cardCount-1, cardCount-1)
	}
	assertInlineRender(t, body)
	t.Logf("slim body = %d bytes, full-queue body = %d bytes", len(slim), len(full))
	if len(slim)*20 > len(full) {
		t.Errorf("slim body = %d bytes, full body = %d bytes; want the slim body under 5%% of the full one",
			len(slim), len(full))
	}
}

// TestReviewRenderFailureDoesNotFailTheWrite covers the render failure contract: the next card's face cannot be
// produced (its note type config is unreadable), so the response omits rendered_card but still commits and reports the
// write, and the client keeps the render endpoint as its fallback.
func TestReviewRenderFailureDoesNotFailTheWrite(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	answerDeck := seedReviewDeck(t, db, ownerID, "Writable deck")
	brokenDeck := seedReviewDeck(t, db, ownerID, "Broken deck")
	answerNote := seedBasic(t, db, answerDeck.ID, "Q1", "A1")
	brokenNote := seedBasic(t, db, brokenDeck.ID, "Q2", "A2")
	// Only the next card's render fails: its note type can no longer be parsed, while the answered card stays valid.
	if err := db.Model(&store.NoteType{}).Where("deck_id = ?", brokenDeck.ID).
		Update("config_json", "{not json").Error; err != nil {
		t.Fatalf("corrupt note type config: %v", err)
	}
	answeredPub := cardPublicIDOfNote(t, db, answerNote.ID)
	brokenPub := cardPublicIDOfNote(t, db, brokenNote.ID)

	rec := postJSON(srv, "/api/v1/review/answer", map[string]any{
		"card_id": answeredPub, "rating": 4, "expected_version": 0,
		"deck": []string{answerDeck.PublicID, brokenDeck.PublicID},
	}, cookies, map[string]string{auth.CSRFHeaderName: csrf, frameOutputHeader: "1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("answer with an unrenderable next card = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	body := decodeReviewAction(t, rec)
	if body.QueueSize != 1 || body.NewRemaining != 1 {
		t.Errorf("queue_size/new_remaining = %d/%d, want 1/1", body.QueueSize, body.NewRemaining)
	}
	if len(body.Cards) != 1 || body.Cards[0].CardID != brokenPub {
		t.Fatalf("cards = %+v, want only the next card %s", body.Cards, brokenPub)
	}
	if body.RenderedCard != nil {
		t.Errorf("rendered_card = %+v, want it omitted when the render fails", body.RenderedCard)
	}

	// The grade itself was written: the write does not depend on a face that could not be produced.
	var reviews int64
	if err := db.Model(&store.Review{}).Where("card_id = ?", cardIDOfNote(t, db, answerNote.ID)).Count(&reviews).Error; err != nil {
		t.Fatalf("count reviews: %v", err)
	}
	if reviews != 1 {
		t.Errorf("reviews rows for the answered card = %d, want 1", reviews)
	}
}

// TestReviewAnswerLastCardHasNoInlineRender covers the empty-queue boundary: the answered card leaves the queue, so no
// card is serialized and no render is attempted.
func TestReviewAnswerLastCardHasNoInlineRender(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Last card deck")
	note := seedBasic(t, db, deck.ID, "Q", "A")

	rec := postJSON(srv, "/api/v1/review/answer", map[string]any{
		"card_id": cardPublicIDOfNote(t, db, note.ID), "rating": 4, "expected_version": 0,
		"deck": []string{deck.PublicID},
	}, cookies, map[string]string{auth.CSRFHeaderName: csrf})
	if rec.Code != http.StatusOK {
		t.Fatalf("answer = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	body := decodeReviewAction(t, rec)
	if body.QueueSize != 0 || body.Remaining != 0 || body.NewRemaining != 0 {
		t.Errorf("counts = queue_size %d, remaining %d, new_remaining %d; want 0/0/0", body.QueueSize, body.Remaining, body.NewRemaining)
	}
	if len(body.Cards) != 0 {
		t.Errorf("cards = %+v, want none", body.Cards)
	}
	if body.RenderedCard != nil {
		t.Errorf("rendered_card = %+v, want it omitted without a next card", body.RenderedCard)
	}
}
